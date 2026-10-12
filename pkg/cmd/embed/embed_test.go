package embed

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/jcrussell/angr-memories/internal/embeddings"
	"github.com/jcrussell/angr-memories/internal/memory"
	"github.com/jcrussell/angr-memories/pkg/cmdutil"
	"github.com/jcrussell/angr-memories/pkg/iostreams"
)

// fakeEmbedder returns a deterministic 2-dim vector per text and records
// every batch it saw.
type fakeEmbedder struct {
	batches [][]string
	fail    error
}

func (f *fakeEmbedder) Embed(ctx context.Context, texts []string) ([][]float32, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if f.fail != nil {
		return nil, f.fail
	}
	f.batches = append(f.batches, append([]string(nil), texts...))
	out := make([][]float32, len(texts))
	for i, t := range texts {
		out[i] = []float32{float32(len(t)), 1}
	}
	return out, nil
}

func writeCorpus(t *testing.T, dir string, recs []*memory.Memory) string {
	t.Helper()
	path := filepath.Join(dir, "memories.jsonl")
	if err := memory.Save(path, recs); err != nil {
		t.Fatal(err)
	}
	return path
}

func testOpts(t *testing.T, fe *fakeEmbedder) (*Options, *iostreams.IOStreams, func() (string, string)) {
	t.Helper()
	io, _, out, errOut := iostreams.Test()
	dir := t.TempDir()
	recs := []*memory.Memory{
		{Key: "a-current", Body: "A", FirstSeen: "2026-06-01T00:00:00Z"},
		{Key: "b-stale", Body: "B new", FirstSeen: "2026-06-01T00:00:00Z"},
		{Key: "c-missing", Body: "C", FirstSeen: "2026-06-01T00:00:00Z"},
	}
	mem := writeCorpus(t, dir, recs)
	emb := filepath.Join(dir, "embeddings.jsonl")
	f := embeddings.Empty("m", 2)
	f.Rows["a-current"] = embeddings.Row{Key: "a-current", BodyHash: embeddings.BodyHash("A"), Vec: []float32{1, 0}}
	f.Rows["b-stale"] = embeddings.Row{Key: "b-stale", BodyHash: embeddings.BodyHash("B old"), Vec: []float32{1, 0}}
	f.Rows["z-orphan"] = embeddings.Row{Key: "z-orphan", BodyHash: "x", Vec: []float32{1, 0}}
	if err := embeddings.Write(emb, f); err != nil {
		t.Fatal(err)
	}
	connects := 0
	opts := &Options{
		IO: io, MemoriesPath: mem, EmbeddingsPath: emb, Endpoint: "http://127.0.0.1:11434", Model: "m", Batch: 1,
		Connect: func(context.Context, *Options) (Embedder, error) { connects++; return fe, nil },
	}
	return opts, io, func() (string, string) { return out.String(), errOut.String() }
}

func TestNewCmdEmbed_runFCapturesFlags(t *testing.T) {
	io, _, _, _ := iostreams.Test()
	f := &cmdutil.Factory{IOStreams: io}
	var got *Options
	cmd := NewCmdEmbed(f, func(_ context.Context, o *Options) error { got = o; return nil })
	cmd.SetArgs([]string{"--model", "x", "--dims", "256", "--batch", "8", "--dry-run", "--endpoint", "http://h:1"})
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	if got.Model != "x" || got.Dims != 256 || got.Batch != 8 || !got.DryRun || got.Endpoint != "http://h:1" {
		t.Errorf("Options = %+v", got)
	}
	if got.EmbeddingsPath != "embeddings.jsonl" || got.MemoriesPath != "memories.jsonl" {
		t.Errorf("defaults = %+v", got)
	}
}

func TestValidate(t *testing.T) {
	ok := Options{MemoriesPath: "m", EmbeddingsPath: "e", Endpoint: "http://x:1", Model: "m", Batch: 1}
	cases := map[string]func(*Options){
		"empty model":  func(o *Options) { o.Model = "" },
		"bad endpoint": func(o *Options) { o.Endpoint = "x:1" },
		"neg dims":     func(o *Options) { o.Dims = -1 },
		"batch 0":      func(o *Options) { o.Batch = 0 },
		"batch huge":   func(o *Options) { o.Batch = 1000 },
		"no path":      func(o *Options) { o.EmbeddingsPath = "" },
	}
	if err := ok.Validate(); err != nil {
		t.Fatalf("valid options rejected: %v", err)
	}
	for name, mut := range cases {
		o := ok
		mut(&o)
		err := o.Validate()
		var fe *cmdutil.FlagError
		if !errors.As(err, &fe) {
			t.Errorf("%s: got %v, want FlagError", name, err)
		}
	}
}

func TestEmbedRun_EmbedsMissingAndStaleDropsOrphans(t *testing.T) {
	fe := &fakeEmbedder{}
	opts, _, outputs := testOpts(t, fe)
	if err := embedRun(context.Background(), opts); err != nil {
		t.Fatal(err)
	}
	out, errOut := outputs()
	if want := "embedded 2, reused 1, removed 1 -> "; !strings.HasPrefix(out, want) {
		t.Errorf("stdout = %q, want prefix %q", out, want)
	}
	if !strings.Contains(errOut, "1 current, 1 missing, 1 stale, 1 orphaned") {
		t.Errorf("stderr = %q", errOut)
	}
	wantBatches := [][]string{{"b stale\nB new"}, {"c missing\nC"}} // sorted by key, batch size 1
	if diff := cmp.Diff(wantBatches, fe.batches); diff != "" {
		t.Errorf("batches (-want +got):\n%s", diff)
	}
	f, err := embeddings.Load(opts.EmbeddingsPath)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := f.Rows["z-orphan"]; ok {
		t.Error("orphan row not removed")
	}
	if f.Rows["b-stale"].BodyHash != embeddings.BodyHash("B new") || len(f.Rows) != 3 {
		t.Errorf("file rows = %+v", f.Rows)
	}
	records, _ := memory.Load(opts.MemoriesPath)
	if st := embeddings.Check(records, f); !st.Clean() {
		t.Errorf("file not clean after embed: %+v", st)
	}
}

func TestEmbedRun_DryRunListsKeysWithoutConnecting(t *testing.T) {
	fe := &fakeEmbedder{}
	opts, _, outputs := testOpts(t, fe)
	opts.DryRun = true
	opts.Connect = func(context.Context, *Options) (Embedder, error) { t.Fatal("must not connect"); return nil, nil }
	if err := embedRun(context.Background(), opts); err != nil {
		t.Fatal(err)
	}
	out, _ := outputs()
	if out != "b-stale\nc-missing\n" {
		t.Errorf("stdout = %q", out)
	}
}

func TestEmbedRun_ModelMismatchIsHintedError(t *testing.T) {
	opts, _, _ := testOpts(t, &fakeEmbedder{})
	opts.Model = "other"
	err := embedRun(context.Background(), opts)
	var h *cmdutil.ErrHint
	if !errors.As(err, &h) || !strings.Contains(err.Error(), `model "m"`) {
		t.Fatalf("got %v, want a hinted model-mismatch error", err)
	}
}

func TestEmbedRun_NothingToDoDoesNotRewrite(t *testing.T) {
	fe := &fakeEmbedder{}
	opts, _, outputs := testOpts(t, fe)
	if err := embedRun(context.Background(), opts); err != nil {
		t.Fatal(err)
	}
	info1, _ := os.Stat(opts.EmbeddingsPath)
	if err := embedRun(context.Background(), opts); err != nil {
		t.Fatal(err)
	}
	info2, _ := os.Stat(opts.EmbeddingsPath)
	if !info1.ModTime().Equal(info2.ModTime()) {
		t.Error("file rewritten although nothing changed")
	}
	out, _ := outputs()
	if !strings.Contains(out, "embedded 0, reused 3, removed 0") {
		t.Errorf("stdout = %q", out)
	}
}

func TestEmbedRun_CancelSavesPartialProgress(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	fe := &fakeEmbedder{}
	opts, _, outputs := testOpts(t, fe)
	// Cancel after the first batch succeeds.
	inner := opts.Connect
	opts.Connect = func(c context.Context, o *Options) (Embedder, error) {
		e, _ := inner(c, o)
		return &cancelAfterFirst{Embedder: e, cancel: cancel}, nil
	}
	err := embedRun(ctx, opts)
	if !errors.Is(err, cmdutil.ErrCancel) {
		t.Fatalf("got %v, want ErrCancel", err)
	}
	f, _ := embeddings.Load(opts.EmbeddingsPath)
	if f.Rows["b-stale"].BodyHash != embeddings.BodyHash("B new") {
		t.Error("first batch's result was not saved")
	}
	if _, ok := f.Rows["c-missing"]; ok {
		t.Error("second batch should not have run")
	}
	_, errOut := outputs()
	if !strings.Contains(errOut, "interrupted after 1 of 2") {
		t.Errorf("stderr = %q", errOut)
	}
}

type cancelAfterFirst struct {
	Embedder
	cancel context.CancelFunc
	n      int
}

func (c *cancelAfterFirst) Embed(ctx context.Context, texts []string) ([][]float32, error) {
	c.n++
	v, err := c.Embedder.Embed(ctx, texts)
	if c.n == 1 {
		c.cancel()
	}
	return v, err
}
