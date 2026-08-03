package prune

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestManifestSaveLoadRoundtrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "manifest.jsonl")

	rows := []Row{
		{Key: "a", Verdict: VerdictForget, Reason: "stale"},
		{Key: "b", Verdict: VerdictMerge, Canonical: true, MergedBody: "merged"},
		{Key: "c", Verdict: VerdictMerge, MergedInto: "b", Reason: "dup of b"},
		{Key: "d", Verdict: VerdictPromote, PromotedTo: "docs/x.rst", Body: "rst"},
		{Key: "e", Verdict: VerdictKeep},
	}
	if err := SaveManifest(path, rows); err != nil {
		t.Fatalf("SaveManifest: %v", err)
	}

	got, err := LoadManifest(path)
	if err != nil {
		t.Fatalf("LoadManifest: %v", err)
	}
	if !reflect.DeepEqual(got, rows) {
		t.Errorf("roundtrip mismatch:\ngot  %+v\nwant %+v", got, rows)
	}
}

func TestLoadManifestSkipsBlankAndCommentLines(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "manifest.jsonl")
	content := "# a comment\n\n" + `{"key":"a","verdict":"keep"}` + "\n   \n"
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	rows, err := LoadManifest(path)
	if err != nil {
		t.Fatalf("LoadManifest: %v", err)
	}
	if len(rows) != 1 || rows[0].Key != "a" {
		t.Fatalf("got %+v, want exactly one row for key a", rows)
	}
}

func TestVerdictValid(t *testing.T) {
	for _, v := range []Verdict{VerdictForget, VerdictMerge, VerdictPromote, VerdictKeep} {
		if !v.Valid() {
			t.Errorf("Verdict(%q).Valid() = false, want true", v)
		}
	}
	if Verdict("delete").Valid() {
		t.Error(`Verdict("delete").Valid() = true, want false`)
	}
}
