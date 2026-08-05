package chunk

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/jcrussell/angr-memories/internal/memory"
	"github.com/jcrussell/angr-memories/pkg/cmdutil"
	"github.com/jcrussell/angr-memories/pkg/iostreams"
)

func TestNewCmdChunk_runFOverride(t *testing.T) {
	var captured *Options
	cmd := NewCmdChunk(&cmdutil.Factory{IOStreams: iostreams.System()}, func(o *Options) error {
		captured = o
		return nil
	})
	cmd.SetArgs([]string{"--memories", "m.jsonl", "--round", "R5", "--chunk-cap", "5"})
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	if captured.MemoriesPath != "m.jsonl" || captured.Round != "R5" || captured.ChunkCap != 5 {
		t.Errorf("flags not wired: %+v", captured)
	}
}

func TestOptionsValidate(t *testing.T) {
	base := func() *Options { return &Options{Round: "R5", ChunkCap: 70} }

	if err := base().Validate(); err != nil {
		t.Errorf("expected valid options to pass, got %v", err)
	}

	cases := []struct {
		name    string
		mutate  func(*Options)
		wantErr string
	}{
		{"round with slash rejected", func(o *Options) { o.Round = "../escape" }, "--round"},
		{"round with space rejected", func(o *Options) { o.Round = "R 5" }, "--round"},
		{"empty round rejected", func(o *Options) { o.Round = "" }, "--round"},
		{"zero chunk-cap rejected", func(o *Options) { o.ChunkCap = 0 }, "--chunk-cap"},
		{"negative chunk-cap rejected", func(o *Options) { o.ChunkCap = -1 }, "--chunk-cap"},
		{"malformed delta-since rejected", func(o *Options) { o.DeltaSince = "yesterday" }, "--delta-since"},
		{"keys-file with delta-since rejected", func(o *Options) {
			o.KeysFile = "keys.txt"
			o.DeltaSince = "2026-07-01T00:00:00Z"
		}, "--keys-file"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			opts := base()
			c.mutate(opts)
			err := opts.Validate()
			if err == nil {
				t.Fatalf("expected an error mentioning %q, got nil", c.wantErr)
			}
		})
	}

	t.Run("well-formed delta-since accepted", func(t *testing.T) {
		opts := base()
		opts.DeltaSince = "2026-07-01T00:00:00Z"
		if err := opts.Validate(); err != nil {
			t.Errorf("expected a well-formed RFC3339 timestamp to pass, got %v", err)
		}
	})
}

func writeMemories(t *testing.T, records []*memory.Memory) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "memories.jsonl")
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	enc := json.NewEncoder(f)
	for _, r := range records {
		if err := enc.Encode(r); err != nil {
			t.Fatal(err)
		}
	}
	return path
}

func TestChunkRun_bucketsByFamilyAndWritesBundles(t *testing.T) {
	memPath := writeMemories(t, []*memory.Memory{
		{Key: "invariant-alpha", Body: "sees invariant-bravo in its body, cites it", FirstSeen: "2026-06-01T00:00:00Z"},
		{Key: "invariant-bravo", Body: "plain body, long enough to be a citation target", FirstSeen: "2026-06-01T00:00:00Z"},
		{Key: "avoid-c", Body: "unrelated body", FirstSeen: "2026-06-01T00:00:00Z"},
		{Key: "avoid-d", Body: "forgotten, should be excluded", FirstSeen: "2026-06-01T00:00:00Z", DecidedAt: "2026-06-02T00:00:00Z", Reason: "r"},
	})

	roundsDir := t.TempDir()
	io, _, _, _ := iostreams.Test()
	opts := &Options{IO: io, MemoriesPath: memPath, RoundsDir: roundsDir, Round: "R5", ChunkCap: 70}
	if err := chunkRun(opts); err != nil {
		t.Fatalf("chunkRun: %v", err)
	}

	indexPath := filepath.Join(roundsDir, "R5", "batch-index.json")
	raw, err := os.ReadFile(indexPath)
	if err != nil {
		t.Fatalf("reading batch-index.json: %v", err)
	}
	var index map[string][]string
	if err := json.Unmarshal(raw, &index); err != nil {
		t.Fatalf("parsing batch-index.json: %v", err)
	}

	invariantKeys, ok := index["R5-INVARIANT"]
	if !ok {
		t.Fatalf("expected an R5-INVARIANT batch, got batches: %v", keysOf(index))
	}
	if len(invariantKeys) != 2 {
		t.Errorf("R5-INVARIANT batch has %d keys, want 2 (alpha and bravo, not the forgotten one)", len(invariantKeys))
	}

	avoidKeys, ok := index["R5-AVOID"]
	if !ok {
		t.Fatalf("expected an R5-AVOID batch, got batches: %v", keysOf(index))
	}
	if len(avoidKeys) != 1 || avoidKeys[0] != "avoid-c" {
		t.Errorf("R5-AVOID batch = %v, want just [avoid-c] (avoid-d is forgotten)", avoidKeys)
	}

	bodiesPath := filepath.Join(roundsDir, "R5", "batches", "R5-INVARIANT", "bodies.json")
	var bodies map[string]string
	mustReadJSON(t, bodiesPath, &bodies)
	if bodies["invariant-alpha"] == "" || bodies["invariant-bravo"] == "" {
		t.Errorf("bodies.json missing expected keys: %+v", bodies)
	}

	citesPath := filepath.Join(roundsDir, "R5", "batches", "R5-INVARIANT", "cites.json")
	var cites citesSubset
	mustReadJSON(t, citesPath, &cites)
	if len(cites.Out["invariant-alpha"]) != 1 || cites.Out["invariant-alpha"][0] != "invariant-bravo" {
		t.Errorf("cites.json out-edges = %+v, want invariant-alpha -> [invariant-bravo]", cites.Out)
	}
}

func TestChunkRun_deltaSinceRestrictsToNewOrUpdatedKeys(t *testing.T) {
	memPath := writeMemories(t, []*memory.Memory{
		{Key: "invariant-old", Body: "untouched survivor", FirstSeen: "2026-01-01T00:00:00Z"},
		{Key: "invariant-new", Body: "created recently", FirstSeen: "2026-07-15T00:00:00Z"},
		{Key: "invariant-edited", Body: "edited recently", FirstSeen: "2026-01-01T00:00:00Z", UpdatedAt: "2026-07-20T00:00:00Z"},
	})

	roundsDir := t.TempDir()
	io, _, _, _ := iostreams.Test()
	opts := &Options{
		IO: io, MemoriesPath: memPath, RoundsDir: roundsDir, Round: "R6", ChunkCap: 70,
		DeltaSince: "2026-07-01T00:00:00Z",
	}
	if err := chunkRun(opts); err != nil {
		t.Fatalf("chunkRun: %v", err)
	}

	var index map[string][]string
	mustReadJSON(t, filepath.Join(roundsDir, "R6", "batch-index.json"), &index)

	got := index["R6-INVARIANT"]
	if len(got) != 2 {
		t.Fatalf("delta batch = %v, want exactly the new and edited keys (not the untouched survivor)", got)
	}
	seen := map[string]bool{}
	for _, k := range got {
		seen[k] = true
	}
	if !seen["invariant-new"] || !seen["invariant-edited"] || seen["invariant-old"] {
		t.Errorf("delta batch = %v, want [invariant-new, invariant-edited] only", got)
	}
}

func TestChunkRun_keysFileRestrictsToListedKeys(t *testing.T) {
	memPath := writeMemories(t, []*memory.Memory{
		{Key: "invariant-alpha", Body: "in the keys file", FirstSeen: "2026-01-01T00:00:00Z"},
		{Key: "invariant-bravo", Body: "not in the keys file", FirstSeen: "2026-01-01T00:00:00Z"},
		{Key: "avoid-c", Body: "in the keys file but forgotten", FirstSeen: "2026-01-01T00:00:00Z",
			DecidedAt: "2026-06-02T00:00:00Z", Reason: "r"},
	})

	keysPath := filepath.Join(t.TempDir(), "keys.txt")
	if err := os.WriteFile(keysPath, []byte("invariant-alpha\navoid-c\n\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	roundsDir := t.TempDir()
	io, _, _, _ := iostreams.Test()
	opts := &Options{IO: io, MemoriesPath: memPath, RoundsDir: roundsDir, Round: "R6", ChunkCap: 70, KeysFile: keysPath}
	if err := chunkRun(opts); err != nil {
		t.Fatalf("chunkRun: %v", err)
	}

	var index map[string][]string
	mustReadJSON(t, filepath.Join(roundsDir, "R6", "batch-index.json"), &index)

	got := index["R6-INVARIANT"]
	if len(got) != 1 || got[0] != "invariant-alpha" {
		t.Errorf("keys-file batch = %v, want just [invariant-alpha] (bravo not listed, avoid-c forgotten)", got)
	}
	if _, ok := index["R6-AVOID"]; ok {
		t.Errorf("expected no R6-AVOID batch (avoid-c is forgotten, even though listed in keys-file)")
	}
}

func keysOf(m map[string][]string) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

func mustReadJSON(t *testing.T, path string, v any) {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading %s: %v", path, err)
	}
	if err := json.Unmarshal(raw, v); err != nil {
		t.Fatalf("parsing %s: %v", path, err)
	}
}
