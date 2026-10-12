// Package embeddings is the committed embedding cache for the memory
// corpus: embeddings.jsonl at the repo root, one vector per current key.
//
// It is state rather than a cache in the byob-runtime-directories.4 sense
// -- regenerating it needs a model server, so the file carries a
// schema_version the reader checks -- and it is written atomically. The
// site build only ever reads it; `angr-memories embed` is the one writer.
//
// Format: line 1 is a Header object, every following line is a Row. Rows
// are sorted by key. Vectors are base64 of little-endian float32 (about
// 4 KB per 768-dim memory, far smaller than JSON numbers).
package embeddings

import (
	"bufio"
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"sort"
	"strings"

	"github.com/jcrussell/angr-memories/internal/atomicfile"
	"github.com/jcrussell/angr-memories/internal/memory"
)

// SchemaVersion is the current on-disk format. Bump only with a loader
// change; readers refuse newer files instead of misreading them.
const SchemaVersion = 1

// Header is the first line of embeddings.jsonl.
type Header struct {
	SchemaVersion int    `json:"schema_version"`
	Model         string `json:"model"`
	Dims          int    `json:"dims"`
}

// Row is one embedded memory. BodyHash ties the vector to the exact body
// text it was computed from, so Check can tell a stale row from a current
// one without re-embedding.
type Row struct {
	Key      string    `json:"key"`
	BodyHash string    `json:"body_sha256"`
	Vec      []float32 `json:"-"`
}

type rowJSON struct {
	Key      string `json:"key"`
	BodyHash string `json:"body_sha256"`
	Vec      string `json:"vec"`
}

// File is a parsed embeddings.jsonl.
type File struct {
	Header Header
	Rows   map[string]Row
}

// Empty returns a File with the given header and no rows.
func Empty(model string, dims int) *File {
	return &File{Header: Header{SchemaVersion: SchemaVersion, Model: model, Dims: dims}, Rows: map[string]Row{}}
}

// ErrSchemaTooNew is returned (wrapped) when the file was written by a newer
// tool version.
var ErrSchemaTooNew = errors.New("embeddings file schema is newer than this tool supports")

// Read parses an embeddings file. Empty input yields an empty v1 file with
// no model (callers decide whether that is acceptable).
func Read(r io.Reader) (*File, error) {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64*1024), 64*1024*1024)
	f := &File{Rows: map[string]Row{}}
	line := 0
	sawHeader := false
	for sc.Scan() {
		line++
		raw := bytes.TrimSpace(sc.Bytes())
		if len(raw) == 0 {
			continue
		}
		if !sawHeader {
			sawHeader = true
			if err := json.Unmarshal(raw, &f.Header); err != nil {
				return nil, fmt.Errorf("embeddings line %d: parsing header: %w", line, err)
			}
			if f.Header.SchemaVersion == 0 {
				return nil, fmt.Errorf("embeddings line %d: missing schema_version header", line)
			}
			if f.Header.SchemaVersion > SchemaVersion {
				return nil, fmt.Errorf("%w (file v%d, tool v%d); upgrade angr-memories", ErrSchemaTooNew, f.Header.SchemaVersion, SchemaVersion)
			}
			continue
		}
		var rj rowJSON
		if err := json.Unmarshal(raw, &rj); err != nil {
			return nil, fmt.Errorf("embeddings line %d: %w", line, err)
		}
		vec, err := DecodeVec(rj.Vec)
		if err != nil {
			return nil, fmt.Errorf("embeddings line %d (%s): %w", line, rj.Key, err)
		}
		if f.Header.Dims > 0 && len(vec) != f.Header.Dims {
			return nil, fmt.Errorf("embeddings line %d (%s): vector has %d dims, header says %d", line, rj.Key, len(vec), f.Header.Dims)
		}
		f.Rows[rj.Key] = Row{Key: rj.Key, BodyHash: rj.BodyHash, Vec: vec}
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}
	if !sawHeader {
		f.Header.SchemaVersion = SchemaVersion
	}
	return f, nil
}

// Load reads path. A missing file is reported as os.ErrNotExist (wrapped)
// so callers can distinguish "never embedded" from "stale".
func Load(path string) (*File, error) {
	fh, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer fh.Close()
	f, err := Read(fh)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	return f, nil
}

// Write writes f to path atomically, rows sorted by key.
func Write(path string, f *File) error {
	keys := make([]string, 0, len(f.Rows))
	for k := range f.Rows {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return atomicfile.WriteFunc(path, 0o644, func(w io.Writer) error {
		bw := bufio.NewWriter(w)
		enc := json.NewEncoder(bw)
		enc.SetEscapeHTML(false)
		if err := enc.Encode(f.Header); err != nil {
			return err
		}
		for _, k := range keys {
			r := f.Rows[k]
			if err := enc.Encode(rowJSON{Key: r.Key, BodyHash: r.BodyHash, Vec: EncodeVec(r.Vec)}); err != nil {
				return err
			}
		}
		return bw.Flush()
	})
}

// BodyHash is the short content hash recorded per row: the first 16 hex
// characters of sha256(body). Collisions within one corpus are not a
// realistic concern; the hash only answers "did this body change?".
func BodyHash(body string) string {
	sum := sha256.Sum256([]byte(body))
	return hex.EncodeToString(sum[:8])
}

// Text is the exact string that gets embedded for a memory: the key with
// dashes as spaces (so the topic words in keys count), then the body.
func Text(m *memory.Memory) string {
	return strings.ReplaceAll(m.Key, "-", " ") + "\n" + m.Body
}

// EncodeVec packs v as base64 little-endian float32.
func EncodeVec(v []float32) string {
	buf := make([]byte, 4*len(v))
	for i, x := range v {
		binary.LittleEndian.PutUint32(buf[4*i:], math.Float32bits(x))
	}
	return base64.StdEncoding.EncodeToString(buf)
}

// DecodeVec reverses EncodeVec.
func DecodeVec(s string) ([]float32, error) {
	buf, err := base64.StdEncoding.DecodeString(s)
	if err != nil {
		return nil, fmt.Errorf("decoding vector: %w", err)
	}
	if len(buf)%4 != 0 {
		return nil, fmt.Errorf("decoding vector: %d bytes is not a whole number of float32s", len(buf))
	}
	v := make([]float32, len(buf)/4)
	for i := range v {
		v[i] = math.Float32frombits(binary.LittleEndian.Uint32(buf[4*i:]))
	}
	return v, nil
}

// Normalize scales v to unit length in place (no-op for a zero vector).
func Normalize(v []float32) {
	var ss float64
	for _, x := range v {
		ss += float64(x) * float64(x)
	}
	if ss == 0 {
		return
	}
	inv := float32(1 / math.Sqrt(ss))
	for i := range v {
		v[i] *= inv
	}
}

// Status is the result of comparing the corpus against the file.
type Status struct {
	Missing []string // current keys with no row
	Stale   []string // current keys whose row was embedded from an older body
	Orphan  []string // rows for keys no longer in the corpus
	Current int      // keys whose row matches
}

// Clean reports whether every current key has an up-to-date row.
func (s Status) Clean() bool { return len(s.Missing) == 0 && len(s.Stale) == 0 }

// Check compares every key's latest incarnation against f. Slices are
// sorted so output is stable.
func Check(records []*memory.Memory, f *File) Status {
	latest := Latest(records)
	var st Status
	for key, m := range latest {
		row, ok := f.Rows[key]
		switch {
		case !ok:
			st.Missing = append(st.Missing, key)
		case row.BodyHash != BodyHash(m.Body):
			st.Stale = append(st.Stale, key)
		default:
			st.Current++
		}
	}
	for key := range f.Rows {
		if _, ok := latest[key]; !ok {
			st.Orphan = append(st.Orphan, key)
		}
	}
	sort.Strings(st.Missing)
	sort.Strings(st.Stale)
	sort.Strings(st.Orphan)
	return st
}

// Latest is memory.Latest, kept here as the name callers in this package's
// orbit use.
func Latest(records []*memory.Memory) map[string]*memory.Memory { return memory.Latest(records) }

// Vectors returns key -> unit vector for every current row of f, ready for
// clustering. Stale rows are excluded only when skipStale is set, which is
// what `site --allow-stale-embeddings` does.
func Vectors(records []*memory.Memory, f *File, skipStale bool) map[string][]float32 {
	latest := Latest(records)
	out := make(map[string][]float32, len(latest))
	for key, m := range latest {
		row, ok := f.Rows[key]
		if !ok || (skipStale && row.BodyHash != BodyHash(m.Body)) {
			continue
		}
		v := append([]float32(nil), row.Vec...)
		Normalize(v)
		out[key] = v
	}
	return out
}
