package embeddings

import (
	"errors"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/jcrussell/angr-memories/internal/memory"
)

func TestVecRoundTrip(t *testing.T) {
	v := []float32{0, 1, -1.5, 3.25e-7, float32(math.Pi)}
	got, err := DecodeVec(EncodeVec(v))
	if err != nil {
		t.Fatal(err)
	}
	if diff := cmp.Diff(v, got); diff != "" {
		t.Errorf("round trip mismatch (-want +got):\n%s", diff)
	}
	if _, err := DecodeVec("AAA="); err == nil {
		t.Error("expected an error for a non-multiple-of-4 payload")
	}
	if _, err := DecodeVec("!!!"); err == nil {
		t.Error("expected an error for invalid base64")
	}
}

func TestWriteRead_RoundTripSortedWithHeader(t *testing.T) {
	path := filepath.Join(t.TempDir(), "embeddings.jsonl")
	f := Empty("nomic-embed-text", 3)
	f.Rows["b-key"] = Row{Key: "b-key", BodyHash: BodyHash("B"), Vec: []float32{0, 1, 0}}
	f.Rows["a-key"] = Row{Key: "a-key", BodyHash: BodyHash("A"), Vec: []float32{1, 0, 0}}
	if err := Write(path, f); err != nil {
		t.Fatal(err)
	}
	raw, _ := os.ReadFile(path)
	lines := strings.Split(strings.TrimSpace(string(raw)), "\n")
	if len(lines) != 3 || !strings.HasPrefix(lines[0], `{"schema_version":1,"model":"nomic-embed-text","dims":3}`) {
		t.Fatalf("unexpected file layout:\n%s", raw)
	}
	if !strings.Contains(lines[1], `"key":"a-key"`) || !strings.Contains(lines[2], `"key":"b-key"`) {
		t.Errorf("rows not sorted by key:\n%s", raw)
	}
	got, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if diff := cmp.Diff(f, got); diff != "" {
		t.Errorf("Load mismatch (-want +got):\n%s", diff)
	}
}

func TestRead_RejectsNewerSchemaAndMissingHeader(t *testing.T) {
	_, err := Read(strings.NewReader(`{"schema_version":2,"model":"m","dims":1}` + "\n"))
	if !errors.Is(err, ErrSchemaTooNew) {
		t.Errorf("newer schema: got %v, want ErrSchemaTooNew", err)
	}
	_, err = Read(strings.NewReader(`{"key":"k","body_sha256":"x","vec":"AAAAAA=="}` + "\n"))
	if err == nil || !strings.Contains(err.Error(), "schema_version") {
		t.Errorf("missing header: got %v", err)
	}
	_, err = Read(strings.NewReader(`{"schema_version":1,"model":"m","dims":2}` + "\n" + `{"key":"k","body_sha256":"x","vec":"AAAAAA=="}` + "\n"))
	if err == nil || !strings.Contains(err.Error(), "dims") {
		t.Errorf("dims mismatch: got %v", err)
	}
	f, err := Read(strings.NewReader(""))
	if err != nil || f.Header.SchemaVersion != SchemaVersion || len(f.Rows) != 0 {
		t.Errorf("empty input: f=%+v err=%v", f, err)
	}
}

func TestLoad_MissingFileIsNotExist(t *testing.T) {
	_, err := Load(filepath.Join(t.TempDir(), "nope.jsonl"))
	if !errors.Is(err, os.ErrNotExist) {
		t.Errorf("got %v, want os.ErrNotExist", err)
	}
}

func TestCheck_ClassifiesMissingStaleOrphanAndUsesLatestIncarnation(t *testing.T) {
	records := []*memory.Memory{
		{Key: "cur", Body: "same", FirstSeen: "2026-06-01T00:00:00Z"},
		{Key: "stale", Body: "new body", FirstSeen: "2026-06-01T00:00:00Z"},
		{Key: "missing", Body: "x", FirstSeen: "2026-06-01T00:00:00Z"},
		{Key: "twice", Body: "old incarnation", FirstSeen: "2026-05-01T00:00:00Z", DecidedAt: "2026-05-02T00:00:00Z"},
		{Key: "twice", Body: "latest incarnation", FirstSeen: "2026-07-01T00:00:00Z"},
	}
	f := Empty("m", 1)
	f.Rows["cur"] = Row{Key: "cur", BodyHash: BodyHash("same"), Vec: []float32{1}}
	f.Rows["stale"] = Row{Key: "stale", BodyHash: BodyHash("old body"), Vec: []float32{1}}
	f.Rows["gone"] = Row{Key: "gone", BodyHash: BodyHash("?"), Vec: []float32{1}}
	f.Rows["twice"] = Row{Key: "twice", BodyHash: BodyHash("latest incarnation"), Vec: []float32{1}}

	got := Check(records, f)
	want := Status{Missing: []string{"missing"}, Stale: []string{"stale"}, Orphan: []string{"gone"}, Current: 2}
	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("Check mismatch (-want +got):\n%s", diff)
	}
	if got.Clean() {
		t.Error("Clean() should be false with missing/stale keys")
	}

	vecs := Vectors(records, f, true)
	if _, ok := vecs["stale"]; ok {
		t.Error("skipStale should drop stale rows")
	}
	if _, ok := vecs["gone"]; ok {
		t.Error("orphans must never be returned")
	}
	if len(vecs) != 2 {
		t.Errorf("got %d vectors, want 2 (cur, twice)", len(vecs))
	}
}

func TestNormalize(t *testing.T) {
	v := []float32{3, 4}
	Normalize(v)
	if math.Abs(float64(v[0])-0.6) > 1e-6 || math.Abs(float64(v[1])-0.8) > 1e-6 {
		t.Errorf("Normalize = %v", v)
	}
	z := []float32{0, 0}
	Normalize(z)
	if z[0] != 0 || z[1] != 0 {
		t.Errorf("zero vector changed: %v", z)
	}
}

func TestText(t *testing.T) {
	got := Text(&memory.Memory{Key: "z3-solver-fallback", Body: "body"})
	if got != "z3 solver fallback\nbody" {
		t.Errorf("Text = %q", got)
	}
}
