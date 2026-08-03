package prune

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"strings"
)

// Verdict is an agent's decision for one memory during a review batch.
type Verdict string

const (
	VerdictForget  Verdict = "forget"
	VerdictMerge   Verdict = "merge"
	VerdictPromote Verdict = "promote"
	VerdictKeep    Verdict = "keep"
)

func (v Verdict) Valid() bool {
	switch v {
	case VerdictForget, VerdictMerge, VerdictPromote, VerdictKeep:
		return true
	}
	return false
}

// Row is one manifest line: an agent's verdict for one key, ported from
// the deleted bd_prune.py's manifest row schema.
type Row struct {
	Key     string  `json:"key"`
	Verdict Verdict `json:"verdict"`
	Reason  string  `json:"reason"`

	// merge subordinate only:
	MergedInto string `json:"merged_into,omitempty"`
	// merge canonical only:
	Canonical  bool   `json:"canonical,omitempty"`
	MergedBody string `json:"merged_body,omitempty"`

	// promote only -- not auto-applied (see apply.go), but a manifest can
	// still record the reviewer's intended target and content for the
	// human doing the docs edit.
	PromotedTo string `json:"promoted_to,omitempty"`
	Body       string `json:"body,omitempty"`
}

// LoadManifest reads a manifest JSONL file, one Row per line. Blank lines
// and lines starting with "#" are skipped, matching bd_prune.py's
// _load_manifest.
func LoadManifest(path string) ([]Row, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	var rows []Row
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 0, 64*1024), 16*1024*1024)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		var r Row
		if err := json.Unmarshal([]byte(line), &r); err != nil {
			return nil, fmt.Errorf("%s: %w", path, err)
		}
		rows = append(rows, r)
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	return rows, nil
}

// SaveManifest writes rows to path, one JSON object per line.
func SaveManifest(path string, rows []Row) error {
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()

	w := bufio.NewWriter(f)
	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(false)
	for _, r := range rows {
		if err := enc.Encode(r); err != nil {
			return err
		}
	}
	return w.Flush()
}
