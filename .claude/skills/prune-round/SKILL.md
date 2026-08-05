---
name: prune-round
description: Run a pruning round on angr-memories' memory archive (memories.jsonl) — triage, chunk, review, validate, apply, and report. Use when asked to "run a pruning round", "prune the memory archive", or similar for the angr-memories repo.
---

# Running a pruning round

`angr-memories` curates a memory archive (`memories.jsonl`) for the angr project through periodic pruning rounds: triage tags every remembered record, chunk buckets keys into review batches, a reviewer assigns forget/merge/promote/keep verdicts per `rounds/review-instructions.md`, validate checks the manifests, and apply mutates `memories.jsonl`.

This skill documents the runbook first exercised live in round 5 (2026-08-03, native Go pipeline's first real-corpus run — round 5's own eyeball-every-stage caution from that first run is folded into these steps below as standing practice, not repeated as one-off caution).

## Prerequisites

- Build the CLI if `./angr-memories` doesn't exist: `go build -o angr-memories ./cmd/angr-memories` (module root `github.com/jcrussell/angr-memories`, main package is at `cmd/angr-memories`, NOT the repo root).
- A live angr checkout with a `docs/` tree, e.g. `~/repos/angr` — needed for `triage --source/--docs-dir` and for reviewers to check staleness (CLAUDE.md, docs/*.rst, git log, `bd show`/`bd list`).

## Steps

1. **Baseline stats.** `./angr-memories stats` — save the remembered/forgotten/recreated counts for the before/after comparison in step 9.

2. **Triage.** `./angr-memories triage --source <angr-checkout> --docs-dir <angr-checkout>/docs --out triage.jsonl`. Always pass `--source` — omitting it makes the `closed_only` tag *more* eager (unverified `angr-XXXX` references default to closed-compatible), not more conservative. Skim the printed tag totals for sanity (compare rough proportions to the previous round if you have them).

3. **Chunk.** `./angr-memories chunk --round <R> --delta-since <prev-round-apply-timestamp>`.
   - Derive `--delta-since` from the previous round's apply time: `decided_at` on any record touched by that round (e.g. `grep`/`jq` for a forgotten key from the prior round's `decisions.jsonl` and read its `decided_at` in `memories.jsonl`), or `git log` on `memories.jsonl` around that round's commit. Round 5 was applied at `2026-08-03T05:45:12Z`.
   - `chunk` filters on `first_seen` OR `updated_at` >= the timestamp. `updated_at` is only populated if a record's body is edited in place after `first_seen` (rare/unproven so far) — so in practice this is "keys created since the last round," not "keys created-or-edited." Don't expect it to catch a body-only edit to an old key.
   - Inspect `rounds/<R>/batch-index.json` for batch count/sizes, then read `bodies.json`/`cites.json` for a batch or two directly — confirms the chunk output is sane before trusting the review step.

4. **Review batches.** Run the saved workflow: `Workflow({scriptPath: ".claude/workflows/prune-review-batches.js", args: {round: "<R>", roundsDir: "rounds"}})`. It lists the round's batches, then pipelines each through a draft-verdict agent (follows `rounds/review-instructions.md`'s decision tree, writes `manifest.jsonl`) and an independent peer-review agent (re-checks the manifest against the same instructions and `cites.json`, flags disagreements). It runs in the background — wait for the completion notification, don't poll.
   - If a batch fails outright (e.g. hitting a session/usage limit — check the failure message for a reset time), resume once the limit clears with `Workflow({scriptPath: "...", args: {...}, resumeFromRunId: "<run-id-from-first-launch>"})`. Completed batches replay from cache; only the failed ones re-run.
   - **Resolve disagreements.** Read every batch the workflow flagged and either adjust `manifest.jsonl` or record why the original verdict stands.
   - **Independent sanity spot-check**, even when peer review reports zero disagreements (it did for all 10 batches in round 5) — don't take an all-clear peer-review pass as the only check:
     - grep/script-check every manifest against `rounds/mandatory-keep.txt` (verdict must be `keep`, or a merge canonical — never forgotten/merged-away as a subordinate)
     - check every merge subordinate's `merged_into` resolves to a `canonical: true` row inside the **same batch's own manifest** (cross-batch merge targets fail `validate`)
     - check no forgotten/merged-away key has a `cites.json` `"in"` edge from a key OUTSIDE its batch (should already be biased to keep, but confirm)
     - read the reasoning behind the batch(es) with the most forget/merge activity — look for real verification (grep hits, `bd show` output, commit hashes), not hand-waving
   - Promote verdicts: `apply` never auto-applies `promote` — it's report-only. Don't write to `<angr-checkout>/docs/` yourself; collect promote candidates (key, `promoted_to` path, rationale) for the final report so a human adds them by hand, then forgets the key in a later round.

5. **Validate.** `./angr-memories validate --round <R> --keep-list rounds/mandatory-keep.txt`. Hard gate — fix any invalid batch (coverage gaps, bad verdicts, mandatory-keep violations, cross-batch merge targets) and re-validate until clean before proceeding.

6. **Dry-run apply.** `./angr-memories apply --round <R> --keep-list rounds/mandatory-keep.txt` (no `-f`/`--force` — dry-run is the default, git-clean-style: it refuses to mutate anything unless forced). Review the projected forget/merge/promote/keep counts and any `deferred` entries (the citation-orphan guard defers forgetting a key that's still cited by a surviving memory, even if the citer is in the same batch — it checks corpus-wide). That's expected/correct behavior, not a bug to work around with `--skip-guard`.

7. **Apply.** `./angr-memories apply --round <R> --keep-list rounds/mandatory-keep.txt -f [--source <angr-checkout>]`. Backs up `memories.jsonl` to `rounds/<R>/memories.pre-apply.jsonl`, mutates `memories.jsonl` in place, and appends `rounds/<R>/decisions.jsonl`. Note: this rewrites the *entire* file (every line's bytes change, not just the touched ~20 records) if the on-disk schema gained a field since the file was last fully rewritten (e.g. round 5 added an explicit `"updated_at":""` to every record) — a `git diff --stat` showing N/N changed lines isn't itself a red flag; spot-check a few unrelated keys' `body`/`reason` are byte-identical before assuming something broke. Passing `--source <angr-checkout>` also pushes `bd forget` for every newly-forgotten key against that checkout's live `bd` corpus, backing up each key's live body to `rounds/<R>/bd-live-backup.jsonl` first (bd has no atomic multi-key forget or rollback — a failed push warns and continues rather than aborting the batch, and `apply` exits non-zero if any push fails even though the local mutation already succeeded). Check the `live-push (source=...): forgot=N failed=M` summary line; if `failed>0`, see `rounds/<R>/live-push.jsonl` for which keys need a manual retry or `bd remember "<body>" --key <key>` restore from the backup file.

8. **Post-apply stats.** `./angr-memories stats` again.
   - Cross-check the remembered/forgotten delta exactly matches the manifests: `Δforgotten = applied-forgets + merge-subordinates` (a "merge" verdict includes both the canonical, which stays remembered, and the subordinate, which becomes forgotten — so `merge` count in the summary is roughly double the number of merge *pairs*). Per-prefix deltas should match 1:1 against which batches touched that prefix.
   - Sanity-check the shrink is proportional to the delta-scoped key count, not a cliff (round 4 saw ~0.4% forgets on untouched survivors vs 9-26% on new/changed keys when reviewed non-delta-scoped — a delta-scoped round should land in a similar order of magnitude on the reviewed keys, though exact rates vary).
   - Check the `recreated` count didn't jump noticeably — recreation (a forgotten key later reappearing) is rare historically (3 of ~1,587 unique keys as of round 4), so a jump is worth investigating, not routine.

9. **Report.** Summarize: batches reviewed, verdict breakdown, before/after `stats`, promote candidates (for manual doc addition), relocate-worthy forgets (reason cites criterion (f) — key + suggested code location, for optional manual comment-adding, non-blocking), any deferred (citation-orphan-guarded) keys for next round, peer-review disagreements and how they were resolved, anomalies. Leave `memories.jsonl` and `rounds/<R>/` **uncommitted** — the user reviews `git diff`/`git status` and commits themselves, unless they've said otherwise for this session.

## Files this skill relies on

- `.claude/workflows/prune-review-batches.js` — the draft + peer-review pipeline for step 4. Edit it if the decision tree in `rounds/review-instructions.md` changes shape.
- `rounds/review-instructions.md` — the actual decision tree (forget/merge/promote/keep criteria). This skill doesn't restate it; always defer to that file as the source of truth.
- `rounds/mandatory-keep.txt` — keys that must never be forgotten or merged away.
