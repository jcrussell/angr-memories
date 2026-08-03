package prune

import (
	"regexp"
	"strconv"
	"strings"
	"time"
)

// staleAfter is how long a status-shaped memory can go without a more
// recent date before it's tagged stale. bd_triage.py hardcoded a fixed
// cutoff date (2026-05-01) appropriate to its one-off analysis; this is
// the same ~90-day window expressed relative to Context.Now so triage
// stays meaningful on every future run instead of freezing to that date.
const staleAfter = 90 * 24 * time.Hour

var (
	beadRE = regexp.MustCompile(`\bangr-([a-z0-9]+)(?:\.\d+)?\b`)
	dateRE = regexp.MustCompile(`\b(20\d\d)-(\d\d)-(\d\d)\b`)

	statusRE = regexp.MustCompile(
		`(?i)\b(landed|closed|merged|reverted|superseded|fixed in commit|completed|resolved)\b`)
	iterStartRE = regexp.MustCompile(`(?i)^\s*(iter\s+\d|20\d\d-\d\d-\d\d)\b`)
	docsLangRE  = regexp.MustCompile(
		"(?i)\\b(architecture|design|how to apply|invariant|rule|why:|how:|##\\s|```)")

	// filePathRE has no lookbehind (Go's RE2 doesn't support one, unlike
	// the original's `(?<![\w-])`) -- group 1 captures start-of-string or
	// a non-word/non-hyphen boundary char instead, and callers use group 2
	// as the path.
	filePathRE = regexp.MustCompile(
		`(^|[^\w-])((?:angr|native|tests|docs|tools)/[A-Za-z0-9_./-]+\.[A-Za-z]+)`)
)

// Tags mirrors the subset of the deleted bd_triage.py's heuristic tag set
// that doesn't depend on a live beads store beyond bead *status* (which
// Context.BeadStatus supplies from `bd list` in --source).
type Tags struct {
	ClosedOnly   bool `json:"closed_only"`    // every angr-<id> ref in the body resolves to closed or is unknown to bd
	StatusShape  bool `json:"status_shape"`   // body reads like a closure/status receipt
	DocsShape    bool `json:"docs_shape"`     // >=800 chars + architecture/design language -- promote candidate
	FutureDate   bool `json:"future_date"`    // body contains a date after Context.Now
	StaleDate    bool `json:"stale_date"`     // most recent body date is >staleAfter old AND status-shaped
	CitesOther   bool `json:"cites_other"`    // body mentions another remembered key
	DupOfDocs    bool `json:"dup_of_docs"`    // a >=40-char fragment of body appears verbatim under --docs-dir
	StaleFileRef bool `json:"stale_file_ref"` // body cites a repo-relative path git ls-files doesn't know about
}

// Context holds the shared, expensive-to-build state Tag needs, computed
// once per triage run rather than once per record.
type Context struct {
	BeadStatus map[string]string // "angr-XXXX" -> bd status; nil if --source wasn't given
	RepoFiles  map[string]bool   // git-ls-files-relative paths; nil if --source wasn't given
	DocsText   string            // concatenated docs/**/*.rst; empty if --docs-dir wasn't given
	Citations  *Graph
	Now        time.Time
}

// Tag computes the heuristic tags for one memory. Pure function: all IO
// (bead status, repo file list, docs text, citation graph) is precomputed
// into ctx by the caller.
func Tag(key, body string, ctx Context) Tags {
	var t Tags

	if refs := beadRE.FindAllString(body, -1); len(refs) > 0 {
		t.ClosedOnly = true
		for _, ref := range refs {
			status, known := ctx.BeadStatus[normalizeBeadRef(ref)]
			if !known {
				continue // missing from bd is treated as compatible with closed_only
			}
			if status != "closed" {
				t.ClosedOnly = false
			}
		}
	}

	t.StatusShape = statusRE.MatchString(body) || iterStartRE.MatchString(body)
	t.DocsShape = len(body) >= 800 && docsLangRE.MatchString(body)

	var mostRecent string
	for _, m := range dateRE.FindAllStringSubmatch(body, -1) {
		if d, ok := parseDate(m); ok {
			if d.After(ctx.Now) {
				t.FutureDate = true
			}
			iso := m[1] + "-" + m[2] + "-" + m[3]
			if iso > mostRecent {
				mostRecent = iso
			}
		}
	}
	if mostRecent != "" && t.StatusShape {
		if d, err := time.Parse("2006-01-02", mostRecent); err == nil {
			t.StaleDate = ctx.Now.Sub(d) > staleAfter
		}
	}

	t.CitesOther = len(ctx.Citations.Cites[key]) > 0

	if ctx.DocsText != "" {
		t.DupOfDocs = anyFragmentIn(body, ctx.DocsText)
	}

	if ctx.RepoFiles != nil {
		t.StaleFileRef = hasStaleFileRef(body, ctx.RepoFiles)
	}

	return t
}

func normalizeBeadRef(ref string) string {
	// beadRE already anchors on "angr-<id>", optionally followed by
	// ".<n>" which FindAllString includes in the match if present via the
	// non-capturing group -- strip it back off so lookups key on the bare
	// bead id, matching bd's own id format.
	if i := strings.IndexByte(ref, '.'); i >= 0 {
		return ref[:i]
	}
	return ref
}

func parseDate(m []string) (time.Time, bool) {
	y, err1 := strconv.Atoi(m[1])
	mo, err2 := strconv.Atoi(m[2])
	d, err3 := strconv.Atoi(m[3])
	if err1 != nil || err2 != nil || err3 != nil || mo < 1 || mo > 12 || d < 1 || d > 31 {
		return time.Time{}, false
	}
	return time.Date(y, time.Month(mo), d, 0, 0, 0, 0, time.UTC), true
}

// fragmentLen and maxFragments mirror bd_triage.py's _fragments sampling:
// enough 40-char samples spread across the body to catch a verbatim-copied
// passage without diffing the whole body against a large docs corpus.
const (
	fragmentLen  = 40
	maxFragments = 12
)

func fragments(body string) []string {
	body = strings.TrimSpace(body)
	if len(body) < fragmentLen {
		if body == "" {
			return nil
		}
		return []string{body}
	}
	nSamples := len(body) / fragmentLen
	if nSamples > maxFragments {
		nSamples = maxFragments
	}
	if nSamples < 1 {
		nSamples = 1
	}
	step := len(body) / nSamples
	if step < fragmentLen {
		step = fragmentLen
	}
	var out []string
	for i := 0; i+fragmentLen <= len(body) && len(out) < maxFragments; i += step {
		frag := strings.TrimSpace(body[i : i+fragmentLen])
		if len(frag) >= fragmentLen && !strings.HasPrefix(frag, "```") {
			out = append(out, frag)
		}
	}
	return out
}

func anyFragmentIn(body, corpus string) bool {
	for _, f := range fragments(body) {
		if strings.Contains(corpus, f) {
			return true
		}
	}
	return false
}

func hasStaleFileRef(body string, repoFiles map[string]bool) bool {
	for _, m := range filePathRE.FindAllStringSubmatch(body, -1) {
		path := m[2]
		if repoFiles[path] {
			continue
		}
		basename := path
		if i := strings.LastIndexByte(path, '/'); i >= 0 {
			basename = path[i+1:]
		}
		found := false
		for rf := range repoFiles {
			if rf == basename || strings.HasSuffix(rf, "/"+basename) {
				found = true
				break
			}
		}
		if !found {
			return true
		}
	}
	return false
}
