# angr-memories

A browsable, continuously-synced archive of the `angr` project's
`bd`-backed memory corpus, plus the toolkit used to prune it.

- Render the corpus as a static site — prefix-grouped pages, a dashboard
  (corpus size over time, survival histogram, most-cited keys), zero
  JavaScript
- Search remembered (or all) memories with ranked or fuzzy matching
- Keep the archive in sync with the live `bd` corpus (`angr-memories
  sync` diffs against the live snapshot and folds in new/changed/closed
  keys — safe to run repeatedly, e.g. from a cron job)
- Run structured pruning rounds — triage, batch, review, validate,
  apply — against the corpus, with guardrails against runaway forgetting

## Data model

Everything lives in one file, `memories.jsonl` — one JSON object per
line (pretty-printed below for readability):

```json
{
  "key": "invariant-solver-eval-timing",
  "body": "...",
  "first_seen": "2026-06-04T00:00:00Z",
  "updated_at": "",
  "decided_at": "",
  "reason": "",
  "merged_into": ""
}
```

- `key` — the bd memory key
- `body` — last known body text
- `first_seen` — when this record is first known to have existed (a
  floor, not necessarily the true creation date)
- `updated_at` — when `sync` last saw the body change; empty if unedited
  since `first_seen`. Used by `chunk --delta-since` to scope a review to
  changed keys.
- `decided_at` — when it was forgotten; empty while still remembered
- `reason` — why it was forgotten, when recorded; sync-detected closures
  record `"sync: absent from live snapshot"`
- `merged_into` — canonical key this one was merged into, if applicable

A key that's forgotten and later recreated gets a second line with a
later `first_seen`; group by `key` and sort by `first_seen` for
incarnation order.

## Commands

```
angr-memories site     --memories memories.jsonl --out _site   # render the browsable static site
angr-memories stats    --memories memories.jsonl               # remembered/forgotten counts by prefix
angr-memories sync     --source ~/repos/angr --memories memories.jsonl  # pull the live corpus, fold in changes
angr-memories search   <query> [--prefix] [--all] [--fuzzy] [--no-cache] [--limit 20]  # ranked search over memories.jsonl

angr-memories triage   [--source] [--docs-dir] --out triage.jsonl   # heuristic tags per remembered key
angr-memories chunk    --round R5 [--delta-since <RFC3339> | --keys-file <path>]  # bucket remembered keys into review batches
angr-memories validate --round R5                                   # check every batch's manifest
angr-memories apply    --round R5 [-f] [--skip-guard] [--source <angr-checkout>]  # apply a round's manifests to memories.jsonl
```

Run `angr-memories <cmd> --help` for the full flag reference on any of
these. `make build`/`make test`/`make vet`/`make site`/`make clean` wrap
the equivalent `go` invocations.

### Pruning a round

`triage`, `chunk`, `validate`, and `apply` walk the remembered set
through one review round, in order:

```
angr-memories triage   --source ~/repos/angr --out triage.jsonl
angr-memories chunk    --round R7 --delta-since 2026-07-20T00:00:00Z
#  ...review each rounds/R7/batches/<id>/, writing its manifest.jsonl...
angr-memories validate --round R7
angr-memories apply    --round R7 --force
```

1. `triage` (optional) tags every remembered key with heuristics, to
   prioritize which batches need careful review.
2. `chunk` buckets keys into batches under `rounds/<name>/batches/`.
   `--delta-since` scopes to new-or-changed keys instead of re-reviewing
   an already-scrubbed survivor set; `--keys-file` scopes to an exact
   list for an ad-hoc audit.
3. **Review** each batch — by hand, or one agent per batch — using
   `rounds/review-instructions.md`'s decision tree (forget/merge/promote/
   keep, bias toward keep). `rounds/mandatory-keep.txt` lists keys no
   verdict may remove. Write the batch's `manifest.jsonl`.
4. `validate` checks every manifest for full coverage, valid verdicts,
   and merge-row consistency.
5. `apply` combines every manifest and writes the decisions to
   `memories.jsonl` — dry-run unless `-f`/`--force`; add `--source
   <angr-checkout>` to also push forgets to the live `bd` corpus.

`--help` on each command has the full mechanics and edge cases.

## Current snapshot

As of the last `sync`:

| | remembered | forgotten |
|---|---:|---:|
| total | 918 | 1,520 |

3 keys have been forgotten and later recreated at least once.
