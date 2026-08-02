package stats

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jcrussell/angr-memories/internal/memory"
	"github.com/jcrussell/angr-memories/pkg/cmdutil"
	"github.com/jcrussell/angr-memories/pkg/iostreams"
)

func TestNewCmdStats_runFOverride(t *testing.T) {
	var captured *Options
	cmd := NewCmdStats(&cmdutil.Factory{IOStreams: iostreams.System()}, func(o *Options) error {
		captured = o
		return nil
	})
	cmd.SetArgs([]string{"--memories", "m.jsonl"})
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	if captured.MemoriesPath != "m.jsonl" {
		t.Errorf("flag not wired: %+v", captured)
	}
}

func TestStatsSmoke_countsMatchInput(t *testing.T) {
	tmp := t.TempDir()
	memPath := filepath.Join(tmp, "memories.jsonl")

	f, err := os.Create(memPath)
	if err != nil {
		t.Fatal(err)
	}
	enc := json.NewEncoder(f)
	records := []*memory.Memory{
		{Key: "invariant-a", Body: "x", FirstSeen: "2026-06-04T00:00:00Z"},
		{Key: "invariant-b", Body: "x", FirstSeen: "2026-06-04T00:00:00Z", DecidedAt: "2026-06-05T00:00:00Z", Reason: "r"},
		{Key: "avoid-c", Body: "x1", FirstSeen: "2026-06-04T00:00:00Z", DecidedAt: "2026-06-05T00:00:00Z", Reason: "r"},
		{Key: "avoid-c", Body: "x2", FirstSeen: "2026-07-04T00:00:00Z"}, // recreated
	}
	for _, r := range records {
		if err := enc.Encode(r); err != nil {
			t.Fatal(err)
		}
	}
	f.Close()

	io, _, out, _ := iostreams.Test()
	opts := &Options{IO: io, MemoriesPath: memPath}
	if err := statsRun(opts); err != nil {
		t.Fatalf("statsRun: %v", err)
	}

	got := out.String()
	if !strings.Contains(got, "2 remembered, 2 forgotten (4 records, 1 recreated key)") {
		t.Errorf("unexpected summary line, got:\n%s", got)
	}
	if !strings.Contains(got, "invariant") || !strings.Contains(got, "avoid") {
		t.Errorf("expected both prefix families in output, got:\n%s", got)
	}
}
