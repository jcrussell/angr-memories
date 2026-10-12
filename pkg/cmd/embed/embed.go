// Package embed implements `angr-memories embed`: compute (or refresh) the
// committed embedding vectors in embeddings.jsonl using a local Ollama
// server. The site build only reads that file, so this is the one command
// that talks to a model for embeddings.
package embed

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"sort"
	"time"

	"github.com/spf13/cobra"

	"github.com/jcrussell/angr-memories/internal/embeddings"
	"github.com/jcrussell/angr-memories/internal/memory"
	"github.com/jcrussell/angr-memories/internal/ollamaclient"
	"github.com/jcrussell/angr-memories/pkg/cmdutil"
	"github.com/jcrussell/angr-memories/pkg/iostreams"
)

// Embedder is the narrow seam this command needs from a model server.
type Embedder interface {
	Embed(ctx context.Context, texts []string) ([][]float32, error)
}

type Options struct {
	IO         *iostreams.IOStreams
	HTTPClient func() *http.Client
	// Connect reaches the model server and returns an Embedder. Production
	// builds an Ollama client and heartbeats it; tests inject a fake.
	Connect func(ctx context.Context, opts *Options) (Embedder, error)

	MemoriesPath   string
	EmbeddingsPath string
	Endpoint       string
	Model          string
	Dims           int
	Batch          int
	DryRun         bool
}

const (
	DefaultModel = "nomic-embed-text"
	maxBatch     = 256
)

func NewCmdEmbed(f *cmdutil.Factory, runF func(context.Context, *Options) error) *cobra.Command {
	opts := &Options{IO: f.IOStreams, HTTPClient: f.HTTPClient, Connect: connectOllama}
	cmd := &cobra.Command{
		Use:   "embed",
		Short: "Compute or refresh the committed embedding vectors",
		Long: `Embed every memory whose vector in embeddings.jsonl is missing or was
computed from an older body, using an embedding model served by a local
Ollama (https://ollama.com). Rows for keys that left the corpus are dropped.
Up-to-date rows are reused, so after the first run only new or edited
memories cost anything.

embeddings.jsonl is committed: the site build ("angr-memories site", and the
Pages workflow) reads it and refuses to build when it is out of date, so
run this after "sync" or any edit to memories.jsonl and commit the result.
The file records the model and vector size; changing either means
re-embedding everything (delete the file first).

The server address comes from --endpoint, else $OLLAMA_HOST, else
http://127.0.0.1:11434. No credentials are involved.`,
		Example: `  # first time: pull the model, then embed the whole corpus
  ollama pull nomic-embed-text
  angr-memories embed

  # after a sync: only new/changed keys are embedded
  angr-memories sync --source ~/repos/angr && angr-memories embed

  # see what would be embedded without touching the server
  angr-memories embed --dry-run`,
		Args: cobra.NoArgs,
		RunE: func(c *cobra.Command, args []string) error {
			if runF != nil {
				return runF(c.Context(), opts)
			}
			return embedRun(c.Context(), opts)
		},
	}
	cmd.Flags().StringVar(&opts.MemoriesPath, "memories", "memories.jsonl", "path to memories.jsonl")
	cmd.Flags().StringVar(&opts.EmbeddingsPath, "embeddings", "embeddings.jsonl", "path to the embeddings file to update")
	cmd.Flags().StringVar(&opts.Endpoint, "endpoint", ollamaclient.EndpointFromEnv(), "Ollama server URL")
	cmd.Flags().StringVar(&opts.Model, "model", DefaultModel, "embedding model name (as `ollama pull` knows it)")
	cmd.Flags().IntVar(&opts.Dims, "dims", 0, "truncate vectors to this many dimensions (0 = model native)")
	cmd.Flags().IntVar(&opts.Batch, "batch", 32, "texts per request")
	cmd.Flags().BoolVar(&opts.DryRun, "dry-run", false, "list what would be embedded and exit without contacting the server")
	return cmd
}

// Validate checks flag values before any side effect (byob-input-validation.5).
func (o *Options) Validate() error {
	if o.MemoriesPath == "" || o.EmbeddingsPath == "" {
		return cmdutil.FlagErrorf("--memories and --embeddings must not be empty")
	}
	if _, err := ollamaclient.ParseEndpoint(o.Endpoint); err != nil {
		return cmdutil.FlagErrorf("--endpoint: %v", err)
	}
	if o.Model == "" {
		return cmdutil.FlagErrorf("--model must not be empty")
	}
	if o.Dims < 0 {
		return cmdutil.FlagErrorf("--dims must be >= 0, got %d", o.Dims)
	}
	if o.Batch < 1 || o.Batch > maxBatch {
		return cmdutil.FlagErrorf("--batch must be 1-%d, got %d", maxBatch, o.Batch)
	}
	return nil
}

func connectOllama(ctx context.Context, opts *Options) (Embedder, error) {
	c, err := ollamaclient.New(opts.Endpoint, opts.HTTPClient())
	if err != nil {
		return nil, err
	}
	if err := ollamaclient.Heartbeat(ctx, c, opts.Endpoint); err != nil {
		return nil, err
	}
	return &ollamaclient.Embedder{Client: c, Model: opts.Model, Dims: opts.Dims, Endpoint: opts.Endpoint}, nil
}

func embedRun(ctx context.Context, opts *Options) error {
	if err := opts.Validate(); err != nil {
		return err
	}
	records, err := memory.Load(opts.MemoriesPath)
	if err != nil {
		return err
	}
	f, err := embeddings.Load(opts.EmbeddingsPath)
	switch {
	case errors.Is(err, os.ErrNotExist):
		f = embeddings.Empty(opts.Model, opts.Dims)
	case err != nil:
		return err
	case f.Header.Model != "" && f.Header.Model != opts.Model:
		return cmdutil.WithHint(
			fmt.Errorf("%s was embedded with model %q, but --model is %q", opts.EmbeddingsPath, f.Header.Model, opts.Model),
			"vectors from different models cannot be mixed: pass the same --model, or delete the file to re-embed everything")
	case opts.Dims > 0 && f.Header.Dims > 0 && f.Header.Dims != opts.Dims:
		return cmdutil.WithHint(
			fmt.Errorf("%s holds %d-dim vectors, but --dims is %d", opts.EmbeddingsPath, f.Header.Dims, opts.Dims),
			"delete the file to re-embed everything at the new size")
	}

	st := embeddings.Check(records, f)
	todo := append(append([]string(nil), st.Missing...), st.Stale...)
	sort.Strings(todo)
	fmt.Fprintf(opts.IO.ErrOut, "%d current, %d missing, %d stale, %d orphaned (model %s)\n",
		st.Current, len(st.Missing), len(st.Stale), len(st.Orphan), opts.Model)

	if opts.DryRun {
		for _, k := range todo {
			fmt.Fprintln(opts.IO.Out, k)
		}
		return nil
	}
	if len(todo) == 0 && len(st.Orphan) == 0 {
		fmt.Fprintf(opts.IO.Out, "embedded 0, reused %d, removed 0 -> %s unchanged\n", st.Current, opts.EmbeddingsPath)
		return nil
	}

	latest := memory.Latest(records)
	embedded := 0
	var runErr error
	if len(todo) > 0 {
		var e Embedder
		e, runErr = opts.Connect(ctx, opts)
		if runErr != nil {
			return runErr
		}
		prog := newProgress(opts.IO, len(todo))
		for start := 0; start < len(todo); start += opts.Batch {
			end := min(start+opts.Batch, len(todo))
			keys := todo[start:end]
			texts := make([]string, len(keys))
			for i, k := range keys {
				texts[i] = embeddings.Text(latest[k])
			}
			vecs, err := e.Embed(ctx, texts)
			if err != nil {
				runErr = err
				break
			}
			if f.Header.Dims == 0 && len(vecs) > 0 {
				f.Header.Dims = len(vecs[0])
			}
			for i, k := range keys {
				if len(vecs[i]) != f.Header.Dims {
					runErr = cmdutil.WithHint(
						fmt.Errorf("model returned %d-dim vectors but %s holds %d-dim vectors", len(vecs[i]), opts.EmbeddingsPath, f.Header.Dims),
						fmt.Sprintf("pass --dims %d to match the file, or delete it to re-embed everything at the model's size", f.Header.Dims))
					break
				}
				f.Rows[k] = embeddings.Row{Key: k, BodyHash: embeddings.BodyHash(latest[k].Body), Vec: vecs[i]}
			}
			if runErr != nil {
				break
			}
			embedded += len(keys)
			prog.update(embedded)
		}
	}
	for _, k := range st.Orphan {
		delete(f.Rows, k)
	}
	f.Header.Model = opts.Model
	if embedded > 0 || len(st.Orphan) > 0 {
		if err := embeddings.Write(opts.EmbeddingsPath, f); err != nil {
			return err
		}
	}
	if runErr != nil {
		if ctx.Err() != nil {
			fmt.Fprintf(opts.IO.ErrOut, "interrupted after %d of %d; progress saved to %s\n", embedded, len(todo), opts.EmbeddingsPath)
			return cmdutil.ErrCancel
		}
		return runErr
	}
	fmt.Fprintf(opts.IO.Out, "embedded %d, reused %d, removed %d -> %s (%d rows, model %s, %d dims)\n",
		embedded, st.Current, len(st.Orphan), opts.EmbeddingsPath, len(f.Rows), f.Header.Model, f.Header.Dims)
	return nil
}

// progress prints rate-limited plain chatter lines to ErrOut
// (byob-progress.2's off-TTY shape; no spinner, no ANSI).
type progress struct {
	io    *iostreams.IOStreams
	total int
	last  time.Time
	now   func() time.Time
}

func newProgress(io *iostreams.IOStreams, total int) *progress {
	return &progress{io: io, total: total, now: time.Now}
}

func (p *progress) update(n int) {
	if t := p.now(); t.Sub(p.last) >= 2*time.Second {
		fmt.Fprintf(p.io.ErrOut, "embedded %d/%d\n", n, p.total)
		p.last = t
	}
}
