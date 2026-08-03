package prune

import (
	"fmt"
	"sort"
)

// Validate checks a batch manifest against the batch's own key set (from
// its bodies bundle) and the mandatory-keep list, mirroring round4's
// validate_manifest.py. Returns every violation found; empty means valid.
func Validate(rows []Row, batchKeys map[string]bool, mandatoryKeep map[string]bool) []string {
	var errs []string

	seen := map[string]int{}
	for _, r := range rows {
		seen[r.Key]++
	}

	var dupes, missing, extra []string
	for k, c := range seen {
		if c > 1 {
			dupes = append(dupes, k)
		}
	}
	for k := range batchKeys {
		if _, ok := seen[k]; !ok {
			missing = append(missing, k)
		}
	}
	for k := range seen {
		if !batchKeys[k] {
			extra = append(extra, k)
		}
	}
	sort.Strings(dupes)
	sort.Strings(missing)
	sort.Strings(extra)
	if len(dupes) > 0 {
		errs = append(errs, fmt.Sprintf("duplicate keys: %v", dupes))
	}
	if len(missing) > 0 {
		errs = append(errs, fmt.Sprintf("missing keys: %v", missing))
	}
	if len(extra) > 0 {
		errs = append(errs, fmt.Sprintf("extra keys: %v", extra))
	}

	canonical := map[string]bool{}
	for _, r := range rows {
		if r.Verdict == VerdictMerge && r.Canonical {
			canonical[r.Key] = true
		}
	}

	for _, r := range rows {
		if !r.Verdict.Valid() {
			errs = append(errs, fmt.Sprintf("%s: bad verdict %q", r.Key, r.Verdict))
			continue
		}
		if mandatoryKeep[r.Key] && r.Verdict != VerdictKeep && !r.Canonical {
			errs = append(errs, fmt.Sprintf("%s: mandatory-keep violated (verdict=%s)", r.Key, r.Verdict))
		}
		switch r.Verdict {
		case VerdictMerge:
			if r.Canonical {
				if r.MergedBody == "" {
					errs = append(errs, fmt.Sprintf("%s: canonical without merged_body", r.Key))
				}
			} else if !canonical[r.MergedInto] {
				errs = append(errs, fmt.Sprintf(
					"%s: merged_into %q is not a canonical row in this batch", r.Key, r.MergedInto))
			}
		case VerdictPromote:
			if r.PromotedTo == "" || r.Body == "" {
				errs = append(errs, fmt.Sprintf("%s: promote missing promoted_to/body", r.Key))
			}
		}
	}

	return errs
}
