package triage

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jcrussell/angr-memories/internal/memory"
	"github.com/jcrussell/angr-memories/internal/prune"
	"github.com/jcrussell/angr-memories/pkg/cmdutil"
	"github.com/jcrussell/angr-memories/pkg/iostreams"
)

func TestNewCmdTriage_runFOverride(t *testing.T) {
	var captured *Options
	cmd := NewCmdTriage(&cmdutil.Factory{IOStreams: iostreams.System()}, func(o *Options) error {
		captured = o
		return nil
	})
	cmd.SetArgs([]string{"--memories", "m.jsonl", "--out", "t.jsonl", "--docs-dir", "docs"})
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	if captured.MemoriesPath != "m.jsonl" || captured.Out != "t.jsonl" || captured.DocsDir != "docs" {
		t.Errorf("flags not wired: %+v", captured)
	}
}

func TestOptionsValidate(t *testing.T) {
	if err := (&Options{Out: "", Timeout: 30}).Validate(); err == nil {
		t.Error("expected empty --out to be rejected")
	}
	if err := (&Options{Out: "t.jsonl", Timeout: 0}).Validate(); err == nil {
		t.Error("expected zero --timeout to be rejected")
	}
	if err := (&Options{Out: "t.jsonl", Timeout: 30}).Validate(); err != nil {
		t.Errorf("expected valid options to pass, got %v", err)
	}
}

func TestTriageRun_writesOneRowPerRememberedKey(t *testing.T) {
	dir := t.TempDir()
	memPath := filepath.Join(dir, "memories.jsonl")
	if err := memory.Save(memPath, []*memory.Memory{
		{Key: "invariant-alpha", Body: "Fixed in commit abc, landed.", FirstSeen: "2026-06-01T00:00:00Z"},
		{Key: "invariant-bravo", Body: "plain prose, nothing notable", FirstSeen: "2026-06-01T00:00:00Z"},
		{Key: "invariant-gone", Body: "forgotten already", FirstSeen: "2026-06-01T00:00:00Z", DecidedAt: "2026-06-02T00:00:00Z", Reason: "r"},
	}); err != nil {
		t.Fatal(err)
	}

	outPath := filepath.Join(dir, "triage.jsonl")
	io, _, out, _ := iostreams.Test()
	opts := &Options{IO: io, MemoriesPath: memPath, Out: outPath, Timeout: 30}
	if err := triageRun(opts); err != nil {
		t.Fatalf("triageRun: %v", err)
	}
	if !strings.Contains(out.String(), "wrote "+outPath+" (2 rows)") {
		t.Errorf("expected a 2-row summary line, got:\n%s", out.String())
	}

	f, err := os.Open(outPath)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	rows := map[string]triageRow{}
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		var r triageRow
		if err := json.Unmarshal(scanner.Bytes(), &r); err != nil {
			t.Fatal(err)
		}
		rows[r.Key] = r
	}
	if len(rows) != 2 {
		t.Fatalf("got %d rows, want 2 (forgotten key excluded)", len(rows))
	}
	if _, ok := rows["invariant-gone"]; ok {
		t.Error("forgotten key should not appear in triage output")
	}
	if !rows["invariant-alpha"].StatusShape {
		t.Error("expected invariant-alpha to be tagged status_shape")
	}
	if rows["invariant-alpha"].Prefix != "invariant" {
		t.Errorf("Prefix = %q, want invariant", rows["invariant-alpha"].Prefix)
	}
}

func TestTriageRun_warnsAboutUnverifiedClosedOnlyWithoutSource(t *testing.T) {
	dir := t.TempDir()
	memPath := filepath.Join(dir, "memories.jsonl")
	if err := memory.Save(memPath, []*memory.Memory{
		{Key: "invariant-alpha", Body: "see angr-abc123 for context", FirstSeen: "2026-06-01T00:00:00Z"},
	}); err != nil {
		t.Fatal(err)
	}

	io, _, _, errOut := iostreams.Test()
	opts := &Options{IO: io, MemoriesPath: memPath, Out: filepath.Join(dir, "triage.jsonl"), Timeout: 30}
	if err := triageRun(opts); err != nil {
		t.Fatalf("triageRun: %v", err)
	}
	if !strings.Contains(errOut.String(), "warning: --source not set") {
		t.Errorf("expected an unverified-closed_only warning on stderr, got:\n%s", errOut.String())
	}
}

func TestTriageRun_noWarningWhenNoClosedOnlyTags(t *testing.T) {
	dir := t.TempDir()
	memPath := filepath.Join(dir, "memories.jsonl")
	if err := memory.Save(memPath, []*memory.Memory{
		{Key: "invariant-alpha", Body: "plain prose, no bead references", FirstSeen: "2026-06-01T00:00:00Z"},
	}); err != nil {
		t.Fatal(err)
	}

	io, _, _, errOut := iostreams.Test()
	opts := &Options{IO: io, MemoriesPath: memPath, Out: filepath.Join(dir, "triage.jsonl"), Timeout: 30}
	if err := triageRun(opts); err != nil {
		t.Fatalf("triageRun: %v", err)
	}
	if errOut.String() != "" {
		t.Errorf("expected no warning when nothing is tagged closed_only, got:\n%s", errOut.String())
	}
}

func TestLoadDocsText_concatenatesRSTFilesOnly(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "sub"), 0o755); err != nil {
		t.Fatal(err)
	}
	os.WriteFile(filepath.Join(dir, "a.rst"), []byte("alpha content"), 0o644)
	os.WriteFile(filepath.Join(dir, "sub", "b.rst"), []byte("beta content"), 0o644)
	os.WriteFile(filepath.Join(dir, "ignored.txt"), []byte("should not appear"), 0o644)

	got, err := loadDocsText(dir)
	if err != nil {
		t.Fatalf("loadDocsText: %v", err)
	}
	if !strings.Contains(got, "alpha content") || !strings.Contains(got, "beta content") {
		t.Errorf("expected both .rst files concatenated, got: %q", got)
	}
	if strings.Contains(got, "should not appear") {
		t.Errorf("expected non-.rst files excluded, got: %q", got)
	}
}

func TestAddTagTotals(t *testing.T) {
	totals := map[string]int{}
	addTagTotals(totals, prune.Tags{ClosedOnly: true, StatusShape: true})
	addTagTotals(totals, prune.Tags{StatusShape: true})
	if totals["closed_only"] != 1 || totals["status_shape"] != 2 {
		t.Errorf("totals = %+v, want closed_only=1 status_shape=2", totals)
	}
	if totals["docs_shape"] != 0 {
		t.Errorf("expected untouched tags to be absent/zero, got %+v", totals)
	}
}
