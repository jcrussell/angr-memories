package topics

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"

	"github.com/jcrussell/angr-memories/internal/embeddings"
	"github.com/jcrussell/angr-memories/internal/memory"
	"github.com/jcrussell/angr-memories/internal/ollamaclient"
	"github.com/jcrussell/angr-memories/internal/topics"
	"github.com/jcrussell/angr-memories/pkg/cmdutil"
	"github.com/jcrussell/angr-memories/pkg/iostreams"
)

type paths struct{ mem, emb, labels string }

// fixture: three planted groups (invariant-*, z3-*, avoid-*) in 3 dims, one
// forgotten key per group, and one human label anchored to the z3 group.
func fixture(t *testing.T) paths {
	t.Helper()
	dir := t.TempDir()
	p := paths{filepath.Join(dir, "memories.jsonl"), filepath.Join(dir, "embeddings.jsonl"), filepath.Join(dir, "topic-labels.jsonl")}
	var recs []*memory.Memory
	f := embeddings.Empty("fake", 3)
	add := func(key, body string, remembered bool, v []float32) {
		m := &memory.Memory{Key: key, Body: body, FirstSeen: "2026-06-01T00:00:00Z"}
		if !remembered {
			m.DecidedAt, m.Reason = "2026-06-02T00:00:00Z", "r"
		}
		recs = append(recs, m)
		f.Rows[key] = embeddings.Row{Key: key, BodyHash: embeddings.BodyHash(body), Vec: v}
	}
	add("invariant-a", "memory pages must be mapped", true, []float32{1, 0, 0})
	add("invariant-b", "page permissions are checked", true, []float32{0.98, 0.2, 0})
	add("invariant-c", "old page note", false, []float32{0.97, 0.1, 0.2})
	add("z3-d", "solver context discipline", true, []float32{0, 1, 0})
	add("z3-e", "incremental solving", true, []float32{0.2, 0.98, 0})
	add("z3-f", "stale ctx", false, []float32{0.1, 0.97, 0.2})
	add("avoid-g", "do not update baselines blindly", true, []float32{0, 0, 1})
	add("avoid-h", "bench noise floor", true, []float32{0, 0.2, 0.98})
	add("avoid-i", "retired bench", false, []float32{0.2, 0.1, 0.97})
	if err := memory.Save(p.mem, recs); err != nil {
		t.Fatal(err)
	}
	if err := embeddings.Write(p.emb, f); err != nil {
		t.Fatal(err)
	}
	lf := &topics.LabelsFile{SchemaVersion: 1, Labels: []topics.Label{
		{Text: "Z3 solver discipline", Anchors: []string{"z3-d", "z3-e", "z3-f"}, LabeledBy: "human", At: "2026-10-01T00:00:00Z"},
		{Text: "Gone cluster", Anchors: []string{"x-1", "x-2", "x-3"}, LabeledBy: "llm:old", At: "2026-10-01T00:00:00Z"},
	}}
	if err := topics.WriteLabels(p.labels, lf); err != nil {
		t.Fatal(err)
	}
	return p
}

func newIO() (*iostreams.IOStreams, *strings.Builder, *strings.Builder) {
	out, errOut := &strings.Builder{}, &strings.Builder{}
	return &iostreams.IOStreams{Out: out, ErrOut: errOut}, out, errOut
}

func TestNewCmdTopics_runFCapturesFlags(t *testing.T) {
	io, _, _ := newIO()
	var got *Options
	cmd := NewCmdTopics(&cmdutil.Factory{IOStreams: io}, func(_ context.Context, o *Options) error { got = o; return nil })
	cmd.SetArgs([]string{"--unlabeled", "--json", "index,label", "--topics", "5", "--topic-labels", "l.jsonl"})
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	if !got.Unlabeled || got.JSON != "index,label" || got.K != 5 || got.LabelsPath != "l.jsonl" || got.EmbeddingsPath != "embeddings.jsonl" {
		t.Errorf("Options = %+v", got)
	}
}

func TestTopicsRun_TableFilterAndOrphanWarning(t *testing.T) {
	p := fixture(t)
	io, out, errOut := newIO()
	opts := &Options{IO: io, MemoriesPath: p.mem, EmbeddingsPath: p.emb, LabelsPath: p.labels, K: 3}
	if err := topicsRun(context.Background(), opts); err != nil {
		t.Fatal(err)
	}
	table := out.String()
	if !strings.Contains(table, "Z3 solver discipline") || strings.Count(table, "(unlabeled)") != 2 {
		t.Errorf("table:\n%s", table)
	}
	if !strings.Contains(errOut.String(), "no longer match any cluster: Gone cluster") {
		t.Errorf("stderr = %q", errOut.String())
	}

	io2, out2, _ := newIO()
	opts.IO, opts.Unlabeled = io2, true
	if err := topicsRun(context.Background(), opts); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out2.String(), "Z3 solver") || strings.Count(out2.String(), "(unlabeled)") != 2 {
		t.Errorf("--unlabeled table:\n%s", out2.String())
	}
}

func TestTopicsRun_JSONFields(t *testing.T) {
	p := fixture(t)
	io, out, _ := newIO()
	opts := &Options{IO: io, MemoriesPath: p.mem, EmbeddingsPath: p.emb, LabelsPath: p.labels, K: 3, JSON: "index,label,members"}
	if err := topicsRun(context.Background(), opts); err != nil {
		t.Fatal(err)
	}
	var rows []map[string]any
	if err := json.Unmarshal([]byte(out.String()), &rows); err != nil {
		t.Fatalf("not JSON: %v\n%s", err, out.String())
	}
	if len(rows) != 3 {
		t.Fatalf("got %d rows", len(rows))
	}
	for _, r := range rows {
		if len(r) != 3 {
			t.Errorf("row has extra fields: %v", r)
		}
		if r["label"] == "Z3 solver discipline" {
			var keys []string
			for _, m := range r["members"].([]any) {
				keys = append(keys, m.(string))
			}
			if diff := cmp.Diff([]string{"z3-d", "z3-e", "z3-f"}, keys); diff != "" {
				t.Errorf("members (-want +got):\n%s", diff)
			}
		}
	}
	opts.JSON = "index,bogus"
	err := topicsRun(context.Background(), opts)
	var fe *cmdutil.FlagError
	if !errors.As(err, &fe) {
		t.Errorf("bogus field: got %v, want FlagError", err)
	}
}

func TestTopicsRun_MissingEmbeddingsIsHinted(t *testing.T) {
	p := fixture(t)
	io, _, _ := newIO()
	opts := &Options{IO: io, MemoriesPath: p.mem, EmbeddingsPath: filepath.Join(t.TempDir(), "none.jsonl"), LabelsPath: p.labels, K: 3}
	err := topicsRun(context.Background(), opts)
	var h *cmdutil.ErrHint
	if !errors.As(err, &h) || !strings.Contains(h.Hint, "angr-memories embed") {
		t.Fatalf("got %v", err)
	}
}

func unlabeledIndex(t *testing.T, p paths, prefix string) int {
	t.Helper()
	io, _, _ := newIO()
	c, err := load(io, p.mem, p.emb, p.labels, 3)
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range c.rows() {
		if r.Unlabeled && strings.HasPrefix(r.Members[0], prefix) {
			return r.Index
		}
	}
	t.Fatalf("no unlabeled %s cluster", prefix)
	return 0
}

func TestLabelRun_WritesAnchoredLabel(t *testing.T) {
	p := fixture(t)
	idx := unlabeledIndex(t, p, "invariant")
	io, out, _ := newIO()
	now := time.Date(2026, 10, 12, 9, 0, 0, 0, time.UTC)
	opts := &LabelOptions{IO: io, Now: func() time.Time { return now }, MemoriesPath: p.mem, EmbeddingsPath: p.emb, LabelsPath: p.labels, K: 3,
		Index: idx, Text: "  Paged memory & permissions ", By: "human"}
	if err := labelRun(context.Background(), opts); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), `as "Paged memory & permissions" (3 anchors)`) {
		t.Errorf("stdout = %q", out.String())
	}
	lf, err := topics.LoadLabels(p.labels)
	if err != nil {
		t.Fatal(err)
	}
	var got *topics.Label
	for i := range lf.Labels {
		if lf.Labels[i].Text == "Paged memory & permissions" {
			got = &lf.Labels[i]
		}
	}
	if got == nil || got.LabeledBy != "human" || got.At != "2026-10-12T09:00:00Z" {
		t.Fatalf("label row = %+v", got)
	}
	if diff := cmp.Diff([]string{"invariant-a", "invariant-b", "invariant-c"}, sorted(got.Anchors)); diff != "" {
		t.Errorf("anchors (-want +got):\n%s", diff)
	}

	// It now shows as labeled.
	io2, out2, _ := newIO()
	if err := topicsRun(context.Background(), &Options{IO: io2, MemoriesPath: p.mem, EmbeddingsPath: p.emb, LabelsPath: p.labels, K: 3}); err != nil {
		t.Fatal(err)
	}
	if strings.Count(out2.String(), "(unlabeled)") != 1 {
		t.Errorf("after labeling:\n%s", out2.String())
	}

	// Out-of-range index is a usage error.
	opts.Index = 99
	var fe *cmdutil.FlagError
	if err := labelRun(context.Background(), opts); !errors.As(err, &fe) {
		t.Errorf("index 99: got %v, want FlagError", err)
	}
}

func TestLabelOptions_Validate(t *testing.T) {
	ok := LabelOptions{MemoriesPath: "m", EmbeddingsPath: "e", LabelsPath: "l", K: 3, Index: 1, Text: "x", By: "human"}
	if err := ok.Validate(); err != nil {
		t.Fatal(err)
	}
	for name, mut := range map[string]func(*LabelOptions){
		"empty text": func(o *LabelOptions) { o.Text = "  " },
		"long text":  func(o *LabelOptions) { o.Text = strings.Repeat("x", 81) },
		"index 0":    func(o *LabelOptions) { o.Index = 0 },
		"no by":      func(o *LabelOptions) { o.By = "" },
	} {
		o := ok
		mut(&o)
		var fe *cmdutil.FlagError
		if err := o.Validate(); !errors.As(err, &fe) {
			t.Errorf("%s: got %v, want FlagError", name, err)
		}
	}
}

// scriptedSuggester replays answers in order and records the briefs.
type scriptedSuggester struct {
	answers []string
	briefs  []ollamaclient.ClusterBrief
}

func (s *scriptedSuggester) Suggest(ctx context.Context, b ollamaclient.ClusterBrief) (string, error) {
	s.briefs = append(s.briefs, b)
	if len(s.answers) == 0 {
		return "", errors.New("script exhausted")
	}
	a := s.answers[0]
	s.answers = s.answers[1:]
	return a, nil
}

func suggestOpts(p paths, s Suggester) (*SuggestOptions, *strings.Builder, *strings.Builder) {
	io, out, errOut := newIO()
	return &SuggestOptions{IO: io, Now: func() time.Time { return time.Date(2026, 10, 12, 10, 0, 0, 0, time.UTC) },
		Connect:      func(context.Context, *SuggestOptions) (Suggester, error) { return s, nil },
		MemoriesPath: p.mem, EmbeddingsPath: p.emb, LabelsPath: p.labels, K: 3,
		Endpoint: "http://127.0.0.1:11434", ChatModel: "fakechat"}, out, errOut
}

func TestSuggestRun_WritesDraftsAndRetriesDuplicates(t *testing.T) {
	p := fixture(t)
	// First unlabeled cluster: model repeats the existing label, then fixes it.
	s := &scriptedSuggester{answers: []string{`"Z3 solver discipline."`, "Paged memory & permissions", "Bench baselines & noise"}}
	opts, out, errOut := suggestOpts(p, s)
	if err := suggestRun(context.Background(), opts); err != nil {
		t.Fatal(err)
	}
	if len(s.briefs) != 3 {
		t.Fatalf("expected 3 model calls (one retry), got %d", len(s.briefs))
	}
	if len(s.briefs[1].Avoid) != 1 || s.briefs[1].Avoid[0] != "Z3 solver discipline" {
		t.Errorf("retry brief should name the clash, got %+v", s.briefs[1].Avoid)
	}
	b := s.briefs[0]
	if b.Size != 3 || len(b.Keys) != 3 || len(b.Excerpts) != 3 || len(b.Examples) == 0 || !strings.Contains(b.Excerpts[0], ": ") {
		t.Errorf("brief = %+v", b)
	}
	lf, _ := topics.LoadLabels(p.labels)
	var texts []string
	for _, l := range lf.Labels {
		texts = append(texts, l.Text+"|"+l.LabeledBy)
	}
	want := []string{"Bench baselines & noise|llm:fakechat", "Gone cluster|llm:old", "Paged memory & permissions|llm:fakechat", "Z3 solver discipline|human"}
	if diff := cmp.Diff(want, sorted(texts)); diff != "" {
		t.Errorf("labels file (-want +got):\n%s", diff)
	}
	if !strings.Contains(out.String(), "Paged memory & permissions") || !strings.Contains(errOut.String(), "wrote 2 label(s)") {
		t.Errorf("out=%q err=%q", out.String(), errOut.String())
	}
}

func TestSuggestRun_DryRunWritesNothing(t *testing.T) {
	p := fixture(t)
	before, _ := topics.LoadLabels(p.labels)
	s := &scriptedSuggester{answers: []string{"A", "B"}}
	opts, out, errOut := suggestOpts(p, s)
	opts.DryRun = true
	if err := suggestRun(context.Background(), opts); err != nil {
		t.Fatal(err)
	}
	after, _ := topics.LoadLabels(p.labels)
	if diff := cmp.Diff(before, after); diff != "" {
		t.Errorf("dry run changed the file:\n%s", diff)
	}
	if !strings.Contains(out.String(), "A") || !strings.Contains(errOut.String(), "dry run") {
		t.Errorf("out=%q err=%q", out.String(), errOut.String())
	}
}

func TestSuggestRun_OnlyAndRelabel(t *testing.T) {
	p := fixture(t)
	s := &scriptedSuggester{answers: []string{"Only one"}}
	opts, _, _ := suggestOpts(p, s)
	opts.Only = unlabeledIndex(t, p, "avoid")
	if err := suggestRun(context.Background(), opts); err != nil {
		t.Fatal(err)
	}
	if len(s.briefs) != 1 || s.briefs[0].Keys[0] != "avoid-g" {
		t.Errorf("--only should target the avoid cluster once, got %+v", s.briefs)
	}

	// Without --relabel the llm-labeled cluster is left alone; with it, redone.
	s2 := &scriptedSuggester{answers: []string{"Fresh", "Fresher"}}
	opts2, _, _ := suggestOpts(p, s2)
	if err := suggestRun(context.Background(), opts2); err != nil {
		t.Fatal(err)
	}
	if len(s2.briefs) != 1 {
		t.Errorf("expected only the remaining unlabeled cluster, got %d calls", len(s2.briefs))
	}
	s3 := &scriptedSuggester{answers: []string{"Again 1", "Again 2"}}
	opts3, _, _ := suggestOpts(p, s3)
	opts3.Relabel = true
	if err := suggestRun(context.Background(), opts3); err != nil {
		t.Fatal(err)
	}
	if len(s3.briefs) != 2 {
		t.Errorf("--relabel should redo both llm-labeled clusters, got %d calls", len(s3.briefs))
	}
}

func TestSuggestOptions_Validate(t *testing.T) {
	ok := SuggestOptions{MemoriesPath: "m", EmbeddingsPath: "e", LabelsPath: "l", K: 3, Endpoint: "http://x:1", ChatModel: "c"}
	if err := ok.Validate(); err != nil {
		t.Fatal(err)
	}
	for name, mut := range map[string]func(*SuggestOptions){
		"bad endpoint": func(o *SuggestOptions) { o.Endpoint = "x" },
		"no model":     func(o *SuggestOptions) { o.ChatModel = " " },
		"neg only":     func(o *SuggestOptions) { o.Only = -1 },
		"k 0":          func(o *SuggestOptions) { o.K = 0 },
	} {
		o := ok
		mut(&o)
		var fe *cmdutil.FlagError
		if err := o.Validate(); !errors.As(err, &fe) {
			t.Errorf("%s: got %v, want FlagError", name, err)
		}
	}
}

func TestCleanLabel(t *testing.T) {
	for in, want := range map[string]string{
		`"Z3 solver."`:        "Z3 solver",
		"  Paged   memory  ":  "Paged memory",
		"‘Bench noise’":       "Bench noise",
		"Register proxy sync": "Register proxy sync",
	} {
		if got := cleanLabel(in); got != want {
			t.Errorf("cleanLabel(%q) = %q, want %q", in, got, want)
		}
	}
}

func sorted(s []string) []string {
	out := append([]string(nil), s...)
	for i := 1; i < len(out); i++ {
		for j := i; j > 0 && out[j] < out[j-1]; j-- {
			out[j], out[j-1] = out[j-1], out[j]
		}
	}
	return out
}
