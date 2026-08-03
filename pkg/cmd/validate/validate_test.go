package validate

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jcrussell/angr-memories/internal/prune"
	"github.com/jcrussell/angr-memories/pkg/cmdutil"
	"github.com/jcrussell/angr-memories/pkg/iostreams"
)

func TestNewCmdValidate_runFOverride(t *testing.T) {
	var captured *Options
	cmd := NewCmdValidate(&cmdutil.Factory{IOStreams: iostreams.System()}, func(o *Options) error {
		captured = o
		return nil
	})
	cmd.SetArgs([]string{"--round", "R5", "--rounds-dir", "myrounds"})
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	if captured.Round != "R5" || captured.RoundsDir != "myrounds" {
		t.Errorf("flags not wired: %+v", captured)
	}
}

func TestOptionsValidate_rejectsBadRoundName(t *testing.T) {
	if err := (&Options{Round: "../escape"}).Validate(); err == nil {
		t.Error("expected a path-traversal-shaped round name to be rejected")
	}
	if err := (&Options{Round: "R5"}).Validate(); err != nil {
		t.Errorf("expected a well-formed round name to pass, got %v", err)
	}
}

func setupRound(t *testing.T, batches map[string][]string, manifests map[string][]prune.Row) (roundsDir string) {
	t.Helper()
	roundsDir = t.TempDir()
	roundDir := filepath.Join(roundsDir, "R5")
	if err := os.MkdirAll(roundDir, 0o755); err != nil {
		t.Fatal(err)
	}
	idx, _ := json.Marshal(batches)
	if err := os.WriteFile(filepath.Join(roundDir, "batch-index.json"), idx, 0o644); err != nil {
		t.Fatal(err)
	}
	for batchID, rows := range manifests {
		batchDir := filepath.Join(roundDir, "batches", batchID)
		if err := os.MkdirAll(batchDir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := prune.SaveManifest(filepath.Join(batchDir, "manifest.jsonl"), rows); err != nil {
			t.Fatal(err)
		}
	}
	return roundsDir
}

func TestValidateRun_allBatchesValid(t *testing.T) {
	roundsDir := setupRound(t,
		map[string][]string{"R5-A": {"a", "b"}},
		map[string][]prune.Row{
			"R5-A": {
				{Key: "a", Verdict: prune.VerdictForget, Reason: "stale"},
				{Key: "b", Verdict: prune.VerdictKeep},
			},
		},
	)

	io, _, out, _ := iostreams.Test()
	opts := &Options{IO: io, RoundsDir: roundsDir, Round: "R5", KeepListPath: filepath.Join(roundsDir, "no-such-keep-list.txt")}
	if err := validateRun(opts); err != nil {
		t.Fatalf("validateRun: %v", err)
	}
	if !strings.Contains(out.String(), "R5-A") || !strings.Contains(out.String(), "OK") {
		t.Errorf("expected an OK line for R5-A, got:\n%s", out.String())
	}
	if !strings.Contains(out.String(), "all 1 batches valid") {
		t.Errorf("expected a summary line, got:\n%s", out.String())
	}
}

func TestValidateRun_missingManifestFailsLoud(t *testing.T) {
	roundsDir := setupRound(t,
		map[string][]string{"R5-A": {"a"}},
		map[string][]prune.Row{}, // no manifest written for R5-A
	)

	io, _, out, _ := iostreams.Test()
	opts := &Options{IO: io, RoundsDir: roundsDir, Round: "R5", KeepListPath: filepath.Join(roundsDir, "no-such-keep-list.txt")}
	err := validateRun(opts)
	if err == nil {
		t.Fatal("expected an error when a batch has no manifest yet")
	}
	if !strings.Contains(out.String(), "no manifest yet") {
		t.Errorf("expected a 'no manifest yet' line, got:\n%s", out.String())
	}
}

func TestValidateRun_mandatoryKeepFromFile(t *testing.T) {
	roundsDir := setupRound(t,
		map[string][]string{"R5-A": {"protected"}},
		map[string][]prune.Row{
			"R5-A": {{Key: "protected", Verdict: prune.VerdictForget, Reason: "oops"}},
		},
	)
	keepListPath := filepath.Join(roundsDir, "keep.txt")
	if err := os.WriteFile(keepListPath, []byte("# comment\nprotected\n\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	io, _, out, _ := iostreams.Test()
	opts := &Options{IO: io, RoundsDir: roundsDir, Round: "R5", KeepListPath: keepListPath}
	if err := validateRun(opts); err == nil {
		t.Fatal("expected mandatory-keep violation to fail validation")
	}
	if !strings.Contains(out.String(), "mandatory-keep violated") {
		t.Errorf("expected a mandatory-keep error, got:\n%s", out.String())
	}
}
