package ollamaclient

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"math"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/jcrussell/angr-memories/pkg/cmdutil"
)

func TestEmbedder_SendsRequestAndNormalizes(t *testing.T) {
	var got map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/embed" {
			t.Errorf("path = %s", r.URL.Path)
		}
		body, _ := io.ReadAll(r.Body)
		json.Unmarshal(body, &got)
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `{"model":"m","embeddings":[[3,4],[0,2]]}`)
	}))
	defer srv.Close()
	c, err := New(srv.URL, srv.Client())
	if err != nil {
		t.Fatal(err)
	}
	e := &Embedder{Client: c, Model: "m", Dims: 2, Endpoint: srv.URL}
	vecs, err := e.Embed(context.Background(), []string{"a", "b"})
	if err != nil {
		t.Fatal(err)
	}
	if got["model"] != "m" || got["truncate"] != false || got["dimensions"] != float64(2) {
		t.Errorf("request = %v", got)
	}
	if opts, _ := got["options"].(map[string]any); opts["num_ctx"] != float64(DefaultNumCtx) {
		t.Errorf("options = %v, want num_ctx %d", got["options"], DefaultNumCtx)
	}
	if math.Abs(float64(vecs[0][0])-0.6) > 1e-6 || math.Abs(float64(vecs[1][1])-1) > 1e-6 {
		t.Errorf("not normalized: %v", vecs)
	}
}

func TestEmbedder_DimMismatchIsAnError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, `{"model":"m","embeddings":[[1,0],[1,0,0]]}`)
	}))
	defer srv.Close()
	c, _ := New(srv.URL, srv.Client())
	_, err := (&Embedder{Client: c, Model: "m"}).Embed(context.Background(), []string{"a", "b"})
	if err == nil || !strings.Contains(err.Error(), "dim") {
		t.Fatalf("got %v, want a dims error", err)
	}
}

func TestEmbedder_ModelNotFoundHint(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		io.WriteString(w, `{"error":"model 'nope' not found"}`)
	}))
	defer srv.Close()
	c, _ := New(srv.URL, srv.Client())
	_, err := (&Embedder{Client: c, Model: "nope", Endpoint: srv.URL}).Embed(context.Background(), []string{"a"})
	var h *cmdutil.ErrHint
	if !errors.As(err, &h) || !strings.Contains(h.Hint, "ollama pull nope") {
		t.Fatalf("got %v, want an ErrHint pointing at ollama pull", err)
	}
}

func TestHeartbeat_ConnectionRefusedHint(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	url := srv.URL
	srv.Close()
	c, _ := New(url, &http.Client{})
	err := Heartbeat(context.Background(), c, url)
	var h *cmdutil.ErrHint
	if !errors.As(err, &h) || !strings.Contains(h.Hint, "ollama serve") {
		t.Fatalf("got %v, want an ErrHint pointing at ollama serve", err)
	}
}

func TestLabeler_StructuredOutput(t *testing.T) {
	var got map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/chat" {
			t.Errorf("path = %s", r.URL.Path)
		}
		body, _ := io.ReadAll(r.Body)
		json.Unmarshal(body, &got)
		io.WriteString(w, `{"model":"c","message":{"role":"assistant","content":"{\"label\": \"Z3 solver context discipline \"}"},"done":true}`)
	}))
	defer srv.Close()
	c, _ := New(srv.URL, srv.Client())
	l := &Labeler{Client: c, Model: "c", Endpoint: srv.URL}
	label, err := l.Suggest(context.Background(), ClusterBrief{Size: 3, Keys: []string{"z3-a", "z3-b"}, Excerpts: []string{"z3-a: ctx"}, Examples: []string{"Paged memory & permissions"}, Avoid: []string{"Solver"}})
	if err != nil {
		t.Fatal(err)
	}
	if label != "Z3 solver context discipline" {
		t.Errorf("label = %q", label)
	}
	if got["stream"] != false || got["format"] == nil || got["model"] != "c" || got["think"] != false {
		t.Errorf("request = %v", got)
	}
	msgs := got["messages"].([]any)
	user := msgs[1].(map[string]any)["content"].(string)
	for _, want := range []string{"z3-a", "Paged memory", "already used", "Solver"} {
		if !strings.Contains(user, want) {
			t.Errorf("prompt missing %q:\n%s", want, user)
		}
	}
}

func TestLabeler_NonJSONIsAnError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, `{"model":"c","message":{"role":"assistant","content":"Solver stuff"},"done":true}`)
	}))
	defer srv.Close()
	c, _ := New(srv.URL, srv.Client())
	_, err := (&Labeler{Client: c, Model: "c"}).Suggest(context.Background(), ClusterBrief{})
	if err == nil || !strings.Contains(err.Error(), "non-JSON") {
		t.Fatalf("got %v", err)
	}
}

func TestEndpointParsing(t *testing.T) {
	t.Setenv("OLLAMA_HOST", "")
	if EndpointFromEnv() != DefaultEndpoint {
		t.Errorf("default = %q", EndpointFromEnv())
	}
	for env, want := range map[string]string{
		"gpu-box:11434":        "http://gpu-box:11434",
		"0.0.0.0":              "http://0.0.0.0:11434",
		"https://gpu-box":      "https://gpu-box:443",
		"http://gpu-box:12345": "http://gpu-box:12345",
	} {
		t.Setenv("OLLAMA_HOST", env)
		if got := EndpointFromEnv(); got != want {
			t.Errorf("OLLAMA_HOST=%q -> %q, want %q", env, got, want)
		}
	}
	for _, bad := range []string{"", "11434", "ftp://x", "localhost:11434"} {
		if _, err := ParseEndpoint(bad); err == nil {
			t.Errorf("ParseEndpoint(%q) should fail", bad)
		}
	}
}

func TestEmbedder_SplitsLongTextsAndAveragesPieces(t *testing.T) {
	var inputs []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req map[string]any
		body, _ := io.ReadAll(r.Body)
		json.Unmarshal(body, &req)
		for _, in := range req["input"].([]any) {
			inputs = append(inputs, in.(string))
		}
		// one vector per input: pieces of the long text point along x and y,
		// the short text along y.
		io.WriteString(w, `{"model":"m","embeddings":[[1,0],[0,1],[0,1]]}`)
	}))
	defer srv.Close()
	c, _ := New(srv.URL, srv.Client())
	e := &Embedder{Client: c, Model: "m", MaxChars: 10}
	long := "aaaaaaaa\nbbbbbbbb" // 17 runes -> two pieces, split after the newline
	vecs, err := e.Embed(context.Background(), []string{long, "short"})
	if err != nil {
		t.Fatal(err)
	}
	if len(inputs) != 3 || inputs[0] != "aaaaaaaa\n" || inputs[1] != "bbbbbbbb" || inputs[2] != "short" {
		t.Fatalf("inputs = %q", inputs)
	}
	if len(vecs) != 2 {
		t.Fatalf("got %d vectors, want 2", len(vecs))
	}
	if math.Abs(float64(vecs[0][0])-math.Sqrt(0.5)) > 1e-6 || math.Abs(float64(vecs[0][1])-math.Sqrt(0.5)) > 1e-6 {
		t.Errorf("long text should be the normalized mean of its pieces, got %v", vecs[0])
	}
	if vecs[1][1] != 1 {
		t.Errorf("short text vector = %v", vecs[1])
	}
}

func TestSplitText(t *testing.T) {
	if got := splitText("abc", 10); len(got) != 1 || got[0] != "abc" {
		t.Errorf("short: %q", got)
	}
	got := splitText(strings.Repeat("x", 25), 10)
	if len(got) != 3 || got[0] != strings.Repeat("x", 10) || got[2] != strings.Repeat("x", 5) {
		t.Errorf("no newlines: %q", got)
	}
	got = splitText("12345678\n12345678\n1", 10)
	if len(got) != 2 || got[0] != "12345678\n" || got[1] != "12345678\n1" {
		t.Errorf("newline preference: %q", got)
	}
}

func TestEmbedder_ServerErrorIsNotReportedAsUnreachable(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		io.WriteString(w, `{"error":"runner crashed"}`)
	}))
	defer srv.Close()
	c, _ := New(srv.URL, srv.Client())
	_, err := (&Embedder{Client: c, Model: "m", Endpoint: srv.URL}).Embed(context.Background(), []string{"a"})
	if err == nil || !strings.Contains(err.Error(), "runner crashed") {
		t.Fatalf("expected the server's message, got %v", err)
	}
	var h *cmdutil.ErrHint
	if errors.As(err, &h) {
		t.Errorf("a 500 from a running server must not carry the 'is Ollama running?' hint: %v", h.Hint)
	}
}

func TestWrap_ReadTimeoutIsNotUnreachable(t *testing.T) {
	timeout := &net.OpError{Op: "read", Net: "tcp", Err: errors.New("i/o timeout")}
	err := wrap(timeout, "m", "http://x:1")
	var h *cmdutil.ErrHint
	if errors.As(err, &h) {
		t.Errorf("a read timeout must not be reported as the server being down: %v", err)
	}
	dial := &net.OpError{Op: "dial", Net: "tcp", Err: errors.New("connect: connection refused")}
	if err := wrap(dial, "m", "http://x:1"); !errors.As(err, &h) {
		t.Errorf("a dial failure should carry the hint: %v", err)
	}
}
