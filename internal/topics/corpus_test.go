package topics

import (
	"bufio"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"math"
	"math/rand"
	"os"
	"testing"
	"time"
)

// TestRun_FullCorpus clusters the live embeddings file when
// ANGR_TOPICS_FULL is set and ../../embeddings.jsonl exists.
func TestRun_FullCorpus(t *testing.T) {
	if os.Getenv("ANGR_TOPICS_FULL") == "" {
		t.Skip("set ANGR_TOPICS_FULL=1 to run on the full corpus")
	}
	path := "../../embeddings.jsonl"
	fh, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			t.Skipf("%s not present", path)
		}
		t.Fatal(err)
	}
	defer fh.Close()

	sc := bufio.NewScanner(fh)
	sc.Buffer(make([]byte, 0, 1<<20), 64<<20)
	if !sc.Scan() {
		t.Fatal("empty embeddings file")
	}
	var hdr struct {
		SchemaVersion int    `json:"schema_version"`
		Model         string `json:"model"`
		Dims          int    `json:"dims"`
	}
	if err := json.Unmarshal(sc.Bytes(), &hdr); err != nil {
		t.Fatalf("header: %v", err)
	}
	var docs []Doc
	for sc.Scan() {
		if len(sc.Bytes()) == 0 {
			continue
		}
		var row struct {
			Key string `json:"key"`
			Vec string `json:"vec"`
		}
		if err := json.Unmarshal(sc.Bytes(), &row); err != nil {
			t.Fatal(err)
		}
		raw, err := base64.StdEncoding.DecodeString(row.Vec)
		if err != nil {
			t.Fatalf("%s: %v", row.Key, err)
		}
		if len(raw)%4 != 0 || (hdr.Dims > 0 && len(raw)/4 != hdr.Dims) {
			t.Fatalf("%s: %d bytes for dims=%d", row.Key, len(raw), hdr.Dims)
		}
		vec := make([]float32, len(raw)/4)
		for i := range vec {
			vec[i] = math.Float32frombits(binary.LittleEndian.Uint32(raw[4*i:]))
		}
		docs = append(docs, Doc{Key: row.Key, Remembered: true, Vec: vec})
	}
	if err := sc.Err(); err != nil {
		t.Fatal(err)
	}
	t.Logf("loaded %d docs, model=%q dims=%d", len(docs), hdr.Model, hdr.Dims)

	start := time.Now()
	res, err := Run(docs, Options{K: 48})
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("Run took %s", time.Since(start))
	if len(res.Clusters) != 48 {
		t.Fatalf("got %d clusters, want 48", len(res.Clusters))
	}
	total := 0
	for _, c := range res.Clusters {
		total += len(c.Members)
	}
	if total != len(docs) {
		t.Errorf("clusters cover %d of %d docs", total, len(docs))
	}
}

func randomUnitDocs(n, dims int, seed int64) []Doc {
	rng := rand.New(rand.NewSource(seed))
	docs := make([]Doc, n)
	for i := range docs {
		v := make([]float32, dims)
		var norm float64
		for j := range v {
			x := rng.NormFloat64()
			v[j] = float32(x)
			norm += x * x
		}
		norm = math.Sqrt(norm)
		for j := range v {
			v[j] = float32(float64(v[j]) / norm)
		}
		docs[i] = Doc{Key: "bench-" + string(rune('a'+i%26)), Remembered: i%3 != 0, Vec: v}
	}
	return docs
}

func BenchmarkRun(b *testing.B) {
	docs := randomUnitDocs(2600, 768, 1)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := Run(docs, Options{K: 48}); err != nil {
			b.Fatal(err)
		}
	}
}
