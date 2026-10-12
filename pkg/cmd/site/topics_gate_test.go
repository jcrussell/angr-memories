package site

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jcrussell/angr-memories/internal/embeddings"
	"github.com/jcrussell/angr-memories/internal/memory"
	"github.com/jcrussell/angr-memories/pkg/cmdutil"
	"github.com/jcrussell/angr-memories/pkg/iostreams"
)

func gateFixture(t *testing.T) (*Options, *strings.Builder) {
	t.Helper()
	dir := t.TempDir()
	mem := filepath.Join(dir, "memories.jsonl")
	recs := []*memory.Memory{
		{Key: "invariant-a", Body: "A", FirstSeen: "2026-06-01T00:00:00Z"},
		{Key: "invariant-b", Body: "B", FirstSeen: "2026-06-01T00:00:00Z"},
		{Key: "z3-c", Body: "C", FirstSeen: "2026-06-01T00:00:00Z", DecidedAt: "2026-06-02T00:00:00Z", Reason: "r"},
	}
	if err := memory.Save(mem, recs); err != nil {
		t.Fatal(err)
	}
	f := embeddings.Empty("fake", 2)
	f.Rows["invariant-a"] = embeddings.Row{Key: "invariant-a", BodyHash: embeddings.BodyHash("A"), Vec: []float32{1, 0}}
	f.Rows["invariant-b"] = embeddings.Row{Key: "invariant-b", BodyHash: embeddings.BodyHash("B"), Vec: []float32{0.9, 0.1}}
	f.Rows["z3-c"] = embeddings.Row{Key: "z3-c", BodyHash: embeddings.BodyHash("C"), Vec: []float32{0, 1}}
	emb := filepath.Join(dir, "embeddings.jsonl")
	if err := embeddings.Write(emb, f); err != nil {
		t.Fatal(err)
	}
	var errOut strings.Builder
	io := &iostreams.IOStreams{Out: &strings.Builder{}, ErrOut: &errOut}
	return &Options{IO: io, MemoriesPath: mem, EmbeddingsPath: emb, LabelsPath: filepath.Join(dir, "topic-labels.jsonl"),
		OutDir: filepath.Join(dir, "_site"), TopicK: 2}, &errOut
}

func TestSiteRun_BuildsTopicsFromEmbeddings(t *testing.T) {
	opts, _ := gateFixture(t)
	if err := siteRun(opts); err != nil {
		t.Fatal(err)
	}
	idx, err := os.ReadFile(filepath.Join(opts.OutDir, "topics", "index.html"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(idx), "<svg class=\"dendro\"") || !strings.Contains(string(idx), "<code>fake</code>") {
		t.Errorf("topics page lacks the dendrogram or model name:\n%s", idx)
	}
	for _, n := range []string{"1", "2"} {
		page, err := os.ReadFile(filepath.Join(opts.OutDir, "topics", n, "index.html"))
		if err != nil {
			t.Fatalf("topic page %s: %v", n, err)
		}
		if !strings.Contains(string(page), "unlabeled") {
			t.Errorf("topic %s should be unlabeled without a labels file", n)
		}
	}
}

func TestSiteRun_StaleEmbeddingsFailWithHintUnlessAllowed(t *testing.T) {
	opts, errOut := gateFixture(t)
	recs, _ := memory.Load(opts.MemoriesPath)
	recs[0].Body = "A edited"
	if err := memory.Save(opts.MemoriesPath, recs); err != nil {
		t.Fatal(err)
	}
	err := siteRun(opts)
	var h *cmdutil.ErrHint
	if !errors.As(err, &h) || !strings.Contains(err.Error(), "1 stale") || !strings.Contains(err.Error(), "invariant-a") {
		t.Fatalf("got %v, want a hinted stale-embeddings error naming invariant-a", err)
	}
	if _, serr := os.Stat(filepath.Join(opts.OutDir, "index.html")); serr == nil {
		t.Error("site must not be written when the gate fails")
	}

	opts.AllowStale = true
	if err := siteRun(opts); err != nil {
		t.Fatalf("allow-stale build failed: %v", err)
	}
	if !strings.Contains(errOut.String(), "warning:") {
		t.Errorf("expected a warning on stderr, got %q", errOut.String())
	}
	idx, _ := os.ReadFile(filepath.Join(opts.OutDir, "topics", "index.html"))
	if strings.Contains(string(idx), "invariant-a") {
		t.Error("stale key should be excluded from the clustering")
	}
}

func TestSiteRun_MissingEmbeddingsRendersEmptyState(t *testing.T) {
	opts, errOut := gateFixture(t)
	os.Remove(opts.EmbeddingsPath)
	if err := siteRun(opts); err != nil {
		t.Fatal(err)
	}
	idx, _ := os.ReadFile(filepath.Join(opts.OutDir, "topics", "index.html"))
	if !strings.Contains(string(idx), "No topic clusters yet") {
		t.Errorf("expected the empty state, got:\n%s", idx)
	}
	if !strings.Contains(errOut.String(), "not found") {
		t.Errorf("expected a note on stderr, got %q", errOut.String())
	}
}
