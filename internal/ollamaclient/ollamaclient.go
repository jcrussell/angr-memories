// Package ollamaclient adapts the official Ollama Go client
// (github.com/ollama/ollama/api) to the two narrow jobs this tool has: embed
// memory texts and suggest a label for a topic cluster. Commands depend on
// their own small interfaces (byob-interfaces.1); this package satisfies
// them. Errors are translated into actionable hints (byob-errors.2):
// "model not found" → `ollama pull`, "connection refused" → `ollama serve`.
package ollamaclient

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
	"syscall"

	"github.com/ollama/ollama/api"
	"github.com/ollama/ollama/envconfig"

	"github.com/jcrussell/angr-memories/internal/embeddings"
	"github.com/jcrussell/angr-memories/pkg/cmdutil"
)

// DefaultEndpoint is where `ollama serve` listens unless OLLAMA_HOST says
// otherwise.
const DefaultEndpoint = "http://127.0.0.1:11434"

// EndpointFromEnv returns $OLLAMA_HOST as the Ollama CLI itself would read
// it (bare host, host:port, scheme optional, default port 11434; the SDK's
// envconfig.Host owns those rules), or DefaultEndpoint when unset.
func EndpointFromEnv() string {
	if strings.TrimSpace(os.Getenv("OLLAMA_HOST")) == "" {
		return DefaultEndpoint
	}
	return envconfig.Host().String()
}

// ParseEndpoint validates an endpoint flag value.
func ParseEndpoint(s string) (*url.URL, error) {
	u, err := url.Parse(s)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return nil, fmt.Errorf("endpoint %q must be an http(s) URL like %s", s, DefaultEndpoint)
	}
	return u, nil
}

// New builds an api.Client for endpoint over hc (the Factory's shared
// client, so timeouts/retries/User-Agent are the tool's, not the SDK's).
func New(endpoint string, hc *http.Client) (*api.Client, error) {
	u, err := ParseEndpoint(endpoint)
	if err != nil {
		return nil, err
	}
	return api.NewClient(u, hc), nil
}

// Heartbeat checks the server is up, with a hint when it is not.
func Heartbeat(ctx context.Context, c *api.Client, endpoint string) error {
	if err := c.Heartbeat(ctx); err != nil {
		return wrap(err, "", endpoint)
	}
	return nil
}

// Embedder computes unit-length embeddings with one model.
type Embedder struct {
	Client   *api.Client
	Model    string
	Dims     int // 0 = model native
	Endpoint string
	// NumCtx is the context window requested from the server. The runner
	// clamps it to the model's trained context (2048 tokens for the
	// nomic-embed-text GGUF Ollama ships, whatever the model card says), so
	// the real guard against "input length exceeds the context length" is
	// MaxChars below.
	NumCtx int
	// MaxChars splits a text longer than this into pieces that are embedded
	// separately and averaged, so a body longer than the window still gets
	// a vector instead of an error. 0 = DefaultMaxChars.
	MaxChars int
}

const (
	DefaultNumCtx = 8192
	// DefaultMaxChars keeps a piece under ~1,700 tokens at the ~3 chars per
	// token this code-heavy English tokenizes to, inside a 2,048 window.
	// Only a handful of memory bodies exceed it.
	DefaultMaxChars = 5000
)

// Embed returns one vector per text, L2-normalized, all with the same
// length (Dims when set). Truncation is disabled so an over-long piece is a
// loud error rather than a silently partial embedding; pieces are kept
// short by MaxChars instead.
func (e *Embedder) Embed(ctx context.Context, texts []string) ([][]float32, error) {
	if len(texts) == 0 {
		return nil, nil
	}
	maxChars := e.MaxChars
	if maxChars <= 0 {
		maxChars = DefaultMaxChars
	}
	numCtx := e.NumCtx
	if numCtx <= 0 {
		numCtx = DefaultNumCtx
	}
	var inputs []string
	var owner []int // inputs[i] belongs to texts[owner[i]]
	for ti, t := range texts {
		for _, piece := range splitText(t, maxChars) {
			inputs = append(inputs, piece)
			owner = append(owner, ti)
		}
	}
	f := false
	req := &api.EmbedRequest{Model: e.Model, Input: inputs, Truncate: &f, Options: map[string]any{"num_ctx": numCtx}}
	if e.Dims > 0 {
		req.Dimensions = e.Dims
	}
	resp, err := e.Client.Embed(ctx, req)
	if err != nil {
		return nil, wrap(err, e.Model, e.Endpoint)
	}
	if len(resp.Embeddings) != len(inputs) {
		return nil, fmt.Errorf("ollama returned %d embeddings for %d inputs", len(resp.Embeddings), len(inputs))
	}
	want := e.Dims
	out := make([][]float32, len(texts))
	for i, v := range resp.Embeddings {
		if want == 0 {
			want = len(v)
		}
		if len(v) != want || len(v) == 0 {
			return nil, fmt.Errorf("ollama returned a %d-dim vector for input %d, want %d", len(v), i, want)
		}
		embeddings.Normalize(v)
		o := owner[i]
		if out[o] == nil {
			out[o] = make([]float32, want)
		}
		for j := range v {
			out[o][j] += v[j] // mean of unit vectors; renormalized below
		}
	}
	for _, v := range out {
		embeddings.Normalize(v)
	}
	return out, nil
}

// splitText cuts s into pieces of at most maxChars runes, preferring to
// break at a newline in the last fifth of a piece so paragraphs stay whole.
func splitText(s string, maxChars int) []string {
	r := []rune(s)
	if len(r) <= maxChars {
		return []string{s}
	}
	var out []string
	for len(r) > maxChars {
		cut := maxChars
		for i := maxChars - 1; i >= maxChars*4/5; i-- {
			if r[i] == '\n' {
				cut = i + 1
				break
			}
		}
		out = append(out, string(r[:cut]))
		r = r[cut:]
	}
	if len(r) > 0 {
		out = append(out, string(r))
	}
	return out
}

// ClusterBrief is what the chat model sees about one cluster.
type ClusterBrief struct {
	Size       int
	Remembered int
	Prefixes   []string // "invariant 12", "z3 4"
	Keys       []string // all member keys
	Excerpts   []string // "key: first ~300 chars of body", exemplars first
	Examples   []string // a few existing labels, for style
	Avoid      []string // labels already taken (set on a retry)
}

// Labeler asks a chat model for a short topic label.
type Labeler struct {
	Client   *api.Client
	Model    string
	Endpoint string
}

var labelSchema = json.RawMessage(`{"type":"object","properties":{"label":{"type":"string"}},"required":["label"]}`)

// Suggest returns the model's label for b, trimmed. The response is forced
// to a JSON object {"label": ...} via Ollama's structured output.
func (l *Labeler) Suggest(ctx context.Context, b ClusterBrief) (string, error) {
	stream := false
	req := &api.ChatRequest{
		Model:  l.Model,
		Stream: &stream,
		Format: labelSchema,
		// Reasoning models (qwen3, gemma3, deepseek-r1, …) would otherwise
		// think for minutes per cluster; a six-word label does not need it,
		// and structured output is more reliable without it. Models that
		// cannot think ignore the field.
		Think: &api.ThinkValue{Value: false},
		Options: map[string]any{
			"temperature": 0,
		},
		Messages: []api.Message{
			{Role: "system", Content: systemPrompt},
			{Role: "user", Content: b.prompt()},
		},
	}
	var content strings.Builder
	err := l.Client.Chat(ctx, req, func(r api.ChatResponse) error {
		content.WriteString(r.Message.Content)
		return nil
	})
	if err != nil {
		return "", wrap(err, l.Model, l.Endpoint)
	}
	var out struct {
		Label string `json:"label"`
	}
	if err := json.Unmarshal([]byte(content.String()), &out); err != nil {
		return "", fmt.Errorf("model %s returned non-JSON output %q: %w", l.Model, content.String(), err)
	}
	return strings.TrimSpace(out.Label), nil
}

const systemPrompt = `You name topic clusters of engineering notes from the angr symbolic-execution project (a Rust+Python rewrite: Z3 solver, VEX lifting, SimProcedures, memory models, benchmarks, CI).
Given one cluster's member keys and excerpts, answer with a JSON object {"label": "..."}: a specific noun phrase of at most 6 words that a maintainer would recognise, in the style of the examples. No trailing period, no quotes inside, no generic labels like "Miscellaneous" or "Various notes".`

func (b ClusterBrief) prompt() string {
	var sb strings.Builder
	fmt.Fprintf(&sb, "Cluster of %d notes (%d still remembered).\n", b.Size, b.Remembered)
	if len(b.Prefixes) > 0 {
		fmt.Fprintf(&sb, "Key prefixes: %s\n", strings.Join(b.Prefixes, ", "))
	}
	if len(b.Examples) > 0 {
		fmt.Fprintf(&sb, "Examples of good labels for other clusters: %s\n", strings.Join(b.Examples, "; "))
	}
	if len(b.Avoid) > 0 {
		fmt.Fprintf(&sb, "These labels are already used by other clusters; choose something more specific: %s\n", strings.Join(b.Avoid, "; "))
	}
	sb.WriteString("\nMember keys:\n")
	for _, k := range b.Keys {
		sb.WriteString("- " + k + "\n")
	}
	sb.WriteString("\nExcerpts:\n")
	for _, x := range b.Excerpts {
		sb.WriteString("- " + x + "\n")
	}
	return sb.String()
}

// wrap turns SDK/network errors into hinted errors.
func wrap(err error, model, endpoint string) error {
	var se api.StatusError
	if errors.As(err, &se) && se.StatusCode == http.StatusNotFound && model != "" {
		return cmdutil.WithHint(fmt.Errorf("ollama: model %q not found", model), fmt.Sprintf("run `ollama pull %s`", model))
	}
	// Only a failure to connect (dial, DNS) means the server is not there;
	// an HTTP status, a read timeout or a reset mid-request is reported as
	// what it is.
	var opErr *net.OpError
	var dnsErr *net.DNSError
	if (errors.As(err, &opErr) && opErr.Op == "dial") || errors.As(err, &dnsErr) || errors.Is(err, syscall.ECONNREFUSED) {
		return cmdutil.WithHint(fmt.Errorf("ollama: cannot reach %s: %w", endpoint, err), "is Ollama running? start it with `ollama serve` (or set OLLAMA_HOST / --endpoint)")
	}
	if se, ok := errors.AsType[api.StatusError](err); ok {
		return fmt.Errorf("ollama: %s", se.Error())
	}
	return fmt.Errorf("ollama: %w", err)
}
