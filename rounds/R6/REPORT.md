# Round R6 — status (2026-08-04)

## Why this round exists

The first R6 attempt (2026-08-03) was chunked with `--keys-file` against a curated,
cross-cutting audit list — only 317 of 1289 remembered keys (~25%), not a full-corpus
pass or a plain `--delta-since` window. There was no recent full pass to delta against
(R1-R4 round artifacts were deleted in an earlier refactor), so 730 of 1272
remembered memories (57%) had never been reviewed against current
`rounds/review-instructions.md` criteria — including forget-criterion (f) and the
tightened keep-bias language.

That R6 was discarded and redone from scratch, scoped to **all currently-remembered
keys minus the 273 keys R5 already reviewed** (R5 stays trusted, not re-litigated):
999 keys across 20 batches (`rounds/R6-scope-keys.txt` has the exact list).

## What's done

- `chunk --round R6 --keys-file rounds/R6-scope-keys.txt` → `rounds/R6/batches/*`
- Draft verdicts + independent peer review, run as direct `Agent` tool calls (not the
  `Workflow` tool — it returned a stale cached result across three separate
  invocations regardless of script/args edits; see memory `workflow-list-batches-bug`).
- 13 peer-review disagreements across 8 batches, all resolved `forget`→`keep`
  directly in the affected `manifest.jsonl` files (citation-orphan violations and
  criterion-(f) forgets with no concrete relocation target). See each batch's
  `manifest.jsonl` `reason` field for "Peer review overturn" entries.
- `./angr-memories validate --round R6 --keep-list rounds/mandatory-keep.txt` — clean,
  all 20 batches OK.
- Dry-run `./angr-memories apply --round R6 --keep-list rounds/mandatory-keep.txt`
  (no `--apply`) projects:
  - 321 forget + 33 merge-subordinate → **354 newly forgotten**
  - 618 keep + 20 merge-canonical + 5 deferred + 2 promote → **645 stay remembered**
  - Net if applied: 1272 → 918 remembered
  - Deferred (citation-orphan-guarded, re-review next round): `native-getopt-family-parity`,
    `native-strndup-parity`, `proxy-writes-design-verdict`,
    `snapshot-rustsimstate-envelope-shape`, `snapshot-solver-smtlib2-design`
  - Promote candidates (manual, non-blocking — add to docs by hand, then forget in a
    later round): `memreplay-bottleneck-root-cause` → `docs/advanced-topics/rust_engine.rst`,
    `z3-universality-parity-test-template` → `docs/extending-angr/rust_vex_ops.rst`

## What's NOT done

- **Nothing has been applied.** `memories.jsonl` is untouched by this round (its `M` in
  `git status` is from R5, already applied earlier, also still uncommitted).
- **Nothing is committed.** `rounds/R5/`, `rounds/R6/`, `rounds/R6-scope-keys.txt`,
  `triage.jsonl`, and the uncommitted `pkg/cmd/chunk/chunk.go`/`chunk_test.go`
  (`--keys-file` flag) are all sitting in the working tree.
- No promote candidates have been manually added to docs yet.

## Next step

Review `git status` / `git diff` on `rounds/R6/` and the manifests, then decide whether
to run:

```
./angr-memories apply --round R6 --keep-list rounds/mandatory-keep.txt --apply
```

This mutates `memories.jsonl` in place and writes `rounds/R6/decisions.jsonl`. After
that: post-apply `./angr-memories stats`, manually add the 2 promote candidates to
docs, and commit whenever you're ready (R5 and R6 are both still uncommitted, so a
commit at that point would cover both rounds unless you split them).
