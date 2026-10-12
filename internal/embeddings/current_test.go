package embeddings

import (
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/jcrussell/angr-memories/internal/memory"
)

// TestEmbeddingsCurrent is the pre-push gate: the committed embeddings.jsonl
// must cover every current key with a vector computed from its current
// body. CI's site build enforces the same thing; this catches it at `make
// test`. Skips when the file does not exist yet (fresh clone, or before the
// first `angr-memories embed`).
func TestEmbeddingsCurrent(t *testing.T) {
	f, err := Load("../../embeddings.jsonl")
	if errors.Is(err, os.ErrNotExist) {
		t.Skip("no embeddings.jsonl yet; run `angr-memories embed`")
	}
	if err != nil {
		t.Fatal(err)
	}
	records, err := memory.Load("../../memories.jsonl")
	if err != nil {
		t.Fatal(err)
	}
	st := Check(records, f)
	if !st.Clean() {
		t.Fatalf("embeddings.jsonl is out of date: %d missing, %d stale keys; run `angr-memories embed` and commit.\nmissing: %s\nstale: %s",
			len(st.Missing), len(st.Stale), preview(st.Missing), preview(st.Stale))
	}
}

func preview(keys []string) string {
	if len(keys) > 10 {
		return strings.Join(keys[:10], ", ") + ", …"
	}
	return strings.Join(keys, ", ")
}
