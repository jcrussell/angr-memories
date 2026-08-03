package prune

import (
	"strings"
	"testing"
	"time"
)

func TestTag(t *testing.T) {
	now := time.Date(2026, 8, 2, 12, 0, 0, 0, time.UTC)
	emptyCtx := Context{Citations: &Graph{}, Now: now}

	t.Run("status shape from closure language", func(t *testing.T) {
		tags := Tag("k", "Fixed in commit abc123, landed on main.", emptyCtx)
		if !tags.StatusShape {
			t.Error("expected StatusShape=true")
		}
	})

	t.Run("status shape from an iteration-receipt prefix", func(t *testing.T) {
		tags := Tag("k", "Iter 4: reran the benchmark suite, all green.", emptyCtx)
		if !tags.StatusShape {
			t.Error("expected StatusShape=true for an 'Iter N' prefix")
		}
	})

	t.Run("plain prose is not status-shaped", func(t *testing.T) {
		tags := Tag("k", "The solver evaluates constraints lazily to avoid blowup.", emptyCtx)
		if tags.StatusShape {
			t.Error("expected StatusShape=false for ordinary prose")
		}
	})

	t.Run("docs shape needs both length and architecture language", func(t *testing.T) {
		long := strings.Repeat("word ", 200) // >800 chars, no arch language
		tags := Tag("k", long, emptyCtx)
		if tags.DocsShape {
			t.Error("expected DocsShape=false: long but no architecture/design language")
		}

		short := "## Architecture\nShort but has heading language."
		tags = Tag("k", short, emptyCtx)
		if tags.DocsShape {
			t.Error("expected DocsShape=false: architecture language but under 800 chars")
		}

		longAndArchy := "## Architecture\n" + strings.Repeat("This explains the design invariant. ", 30)
		tags = Tag("k", longAndArchy, emptyCtx)
		if !tags.DocsShape {
			t.Error("expected DocsShape=true: long AND architecture language")
		}
	})

	t.Run("future date is flagged relative to Context.Now", func(t *testing.T) {
		tags := Tag("k", "Scheduled for 2026-09-01.", emptyCtx)
		if !tags.FutureDate {
			t.Error("expected FutureDate=true for a date after Now")
		}
		tags = Tag("k", "Landed 2026-01-01.", emptyCtx)
		if tags.FutureDate {
			t.Error("expected FutureDate=false for a date before Now")
		}
	})

	t.Run("stale date requires both an old date and status shape", func(t *testing.T) {
		oldStatus := "Landed on 2026-01-01, closed."
		tags := Tag("k", oldStatus, emptyCtx)
		if !tags.StaleDate {
			t.Error("expected StaleDate=true: old date + status shape")
		}

		oldButNotStatus := "Started work around 2026-01-01 sometime, still ongoing exploration."
		tags = Tag("k", oldButNotStatus, emptyCtx)
		if tags.StaleDate {
			t.Error("expected StaleDate=false: old date but not status-shaped")
		}

		recentStatus := "Landed on 2026-08-01, closed."
		tags = Tag("k", recentStatus, emptyCtx)
		if tags.StaleDate {
			t.Error("expected StaleDate=false: status-shaped but recent")
		}
	})

	t.Run("cites other comes from the precomputed citation graph", func(t *testing.T) {
		ctx := Context{Citations: &Graph{Cites: map[string][]string{"k": {"other-key"}}}, Now: now}
		if !Tag("k", "body", ctx).CitesOther {
			t.Error("expected CitesOther=true when the graph has an entry for this key")
		}
		if Tag("other", "body", ctx).CitesOther {
			t.Error("expected CitesOther=false for a key absent from the graph")
		}
	})

	t.Run("dup of docs matches a verbatim fragment", func(t *testing.T) {
		body := strings.Repeat("x", 50) + " unique tail content that is long enough to sample"
		ctx := Context{Citations: &Graph{}, Now: now, DocsText: "some preamble " + body[:40] + " some suffix"}
		if !Tag("k", body, ctx).DupOfDocs {
			t.Error("expected DupOfDocs=true when a body fragment appears in DocsText")
		}
	})

	t.Run("no dup of docs when DocsText is empty (no --docs-dir given)", func(t *testing.T) {
		tags := Tag("k", "anything at all", emptyCtx)
		if tags.DupOfDocs {
			t.Error("expected DupOfDocs=false when DocsText is empty")
		}
	})

	t.Run("stale file ref flags a path git ls-files doesn't know about", func(t *testing.T) {
		ctx := Context{Citations: &Graph{}, Now: now, RepoFiles: map[string]bool{"angr/src/known.rs": true}}
		if !Tag("k", "see angr/src/missing.rs for details", ctx).StaleFileRef {
			t.Error("expected StaleFileRef=true for a path absent from RepoFiles")
		}
		if Tag("k", "see angr/src/known.rs for details", ctx).StaleFileRef {
			t.Error("expected StaleFileRef=false for a path present in RepoFiles")
		}
	})

	t.Run("stale file ref falls back to a basename match for moved files", func(t *testing.T) {
		ctx := Context{Citations: &Graph{}, Now: now, RepoFiles: map[string]bool{"native/angr/src/moved/known.rs": true}}
		if Tag("k", "see angr/src/known.rs for details", ctx).StaleFileRef {
			t.Error("expected StaleFileRef=false: basename known.rs matches a moved file")
		}
	})

	t.Run("no stale file ref check when RepoFiles is nil (no --source given)", func(t *testing.T) {
		tags := Tag("k", "see angr/src/anything.rs", emptyCtx)
		if tags.StaleFileRef {
			t.Error("expected StaleFileRef=false when RepoFiles is nil")
		}
	})

	t.Run("closed only requires every bead ref to resolve closed", func(t *testing.T) {
		ctx := Context{Citations: &Graph{}, Now: now, BeadStatus: map[string]string{"angr-1234": "closed"}}
		if !Tag("k", "fixed in angr-1234", ctx).ClosedOnly {
			t.Error("expected ClosedOnly=true: sole ref is closed")
		}

		ctx.BeadStatus["angr-5678"] = "open"
		if Tag("k", "relates to angr-1234 and angr-5678", ctx).ClosedOnly {
			t.Error("expected ClosedOnly=false: one ref is still open")
		}

		if Tag("k", "no bead refs at all here", ctx).ClosedOnly {
			t.Error("expected ClosedOnly=false when there are no bead refs to judge")
		}

		if !Tag("k", "fixed in angr-9999", ctx).ClosedOnly {
			t.Error("expected ClosedOnly=true: ref unknown to bd is treated as compatible")
		}
	})
}
