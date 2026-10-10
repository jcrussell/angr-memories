# Round R7 — 2026-10-10

## Scope

Delta round: every key new or edited since R6's apply (2026-08-05), plus R6's
5 deferrals and 2 promote candidates — 378 keys in 12 batches
(`rounds/R7-scope-keys.txt`). Each batch had a draft pass and an independent
peer-review pass (direct `Agent` calls).

## Pre-round housekeeping

- R5 had been applied archive-only: 17 of its forgets were still live. 14
  were pushed (`rounds/R5/bd-live-backup.jsonl`); 3 went into R7 instead (one
  rewritten after R5, two with new live citers).
- Archive synced from live: 164 new, 2 closed, 209 edited.

## Tool bug found and fixed (a493c1a)

`apply --source` only ran `bd forget`. Merge canonicals' `merged_body` never
reached live bd, so every R5/R6 merge lost its subordinates' content live (25
canonicals), and `sync` then folded the stale live body back over the archive.
`apply` now writes canonical bodies first and skips all forgets if any write
fails.

Backfill: `backfill-input.json` → `backfill-r5r6.jsonl` (16 bodies restored,
citations repaired against the current tree; 1 R7 canonical extended; 6 R7
forgets re-checked against the merged-in content and upheld). Pre-push live
bodies are in `backfill-live-backup.jsonl`, outcomes in `backfill-push.jsonl`.

## Result

- Peer review overturned 10 forgets (uncited-but-unwritten detail, in-repo
  citers) and restored dropped facts in 7 merged bodies.
- Applied: forget=75, merge=72 (30 canonicals + 42 subordinates), promote=1,
  keep=230, deferred=0. Live push: rewrote=30, forgot=117, failed=0.
- Live corpus: 1094 at round start → 1080 after R5 strays → **963**.
- `z3-universality-parity-test-template` was added to
  `docs/extending-angr/rust_vex_ops.rst` (angr fd24c9a92); the key stays live
  because native source and `iop-avg-vs-hadd-distinction` cite it — forget it in
  a later round once those citations point at the doc.

## Follow-ups (manual, not blocking)

Several (f)-forgets name a doc comment that "should" carry the detail but
doesn't yet; peer review overturned the ones where that would lose content.
Two kept keys need repair: `invariant-vex-fallback-counter-wiring` (cites
`run_loop.rs`; now `run_loop_single::callback_event`) and
`invariant-symcontext-push-not-cache-aware` (key name contradicts body).
