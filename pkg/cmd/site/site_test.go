package site

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

func TestNewCmdSite_runFOverride(t *testing.T) {
	var captured *Options
	cmd := NewCmdSite(&cmdutil.Factory{IOStreams: iostreams.System()}, func(o *Options) error {
		captured = o
		return nil
	})
	cmd.SetArgs([]string{"--memories", "m.jsonl", "--out", "/tmp/s", "--base-url", "/x"})
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	if captured.MemoriesPath != "m.jsonl" || captured.OutDir != "/tmp/s" || captured.BaseURL != "/x" {
		t.Errorf("flags not wired: %+v", captured)
	}
}

func TestSiteSmoke_writesExpectedFiles(t *testing.T) {
	tmp := t.TempDir()
	memPath := filepath.Join(tmp, "memories.jsonl")
	out := filepath.Join(tmp, "_site")

	f, err := os.Create(memPath)
	if err != nil {
		t.Fatal(err)
	}
	enc := json.NewEncoder(f)
	for _, r := range []*memory.Memory{
		{Key: "invariant-a", Body: "**bold** invariant body", FirstSeen: "2026-06-04T00:00:00Z"},
		{Key: "avoid-b", Body: "avoid body", FirstSeen: "2026-06-04T00:00:00Z", DecidedAt: "2026-06-05T00:00:00Z", Reason: "obsolete"},
	} {
		if err := enc.Encode(r); err != nil {
			t.Fatal(err)
		}
	}
	f.Close()

	opts := &Options{IO: &iostreams.IOStreams{}, MemoriesPath: memPath, OutDir: out}
	if err := siteRun(opts); err != nil {
		t.Fatalf("siteRun: %v", err)
	}

	for _, p := range []string{
		filepath.Join(out, "index.html"),
		filepath.Join(out, "prefix", "invariant", "index.html"),
		filepath.Join(out, "prefix", "avoid", "index.html"),
		filepath.Join(out, "static", "site.css"),
		filepath.Join(out, "static", "chroma.css"),
	} {
		if _, err := os.Stat(p); err != nil {
			t.Errorf("expected %s: %v", p, err)
		}
	}

	body, err := os.ReadFile(filepath.Join(out, "prefix", "invariant", "index.html"))
	if err != nil {
		t.Fatal(err)
	}
	if got := string(body); !strings.Contains(got, "<strong>bold</strong>") {
		t.Errorf("expected markdown body rendered to HTML, got: %s", got)
	}
}
