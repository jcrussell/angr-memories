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

Everything lives in one file, `memories.jsonl` — one JSON object per line:

```json
{"key": "invariant-solver-eval-timing", "body": "...", "first_seen": "2026-06-04T00:00:00Z", "updated_at": "", "decided_at": "", "reason": "", "merged_into": ""}
```

- `key` — the bd memory key
- `body` — last known body text
- `first_seen` — when this record is first known to have existed (a floor,
  not necessarily the true creation date)
- `updated_at` — when `sync` last saw the body change; empty if it's never
  been edited since `first_seen`. Exists so pruning rounds can scope
  themselves to genuinely new-or-changed keys (`chunk --delta-since`)
  instead of re-reviewing an untouched survivor set every round.
- `decided_at` — when it was forgotten; empty while still remembered
- `reason` — why it was forgotten, when a curated decision recorded one;
  for closures `sync` detects from a live-corpus diff, this is just
  `"sync: absent from live snapshot"` — a breadcrumb for *how* the closure
  was found, not a guess at *why* the key vanished, which is unknowable
  from a live diff
- `merged_into` — canonical key this one was merged into, if applicable

There's no `verdict` field (`decided_at == ""` means remembered — storing
both would just be two fields that have to agree), no `prefix` field (it's
`key.split("-")[0]` mapped through a small family table, computed on read),
and no incarnation counter. A key that was forgotten and later recreated
just gets a second line with a later `first_seen` — group by `key`, sort by
`first_seen`, and the incarnation order falls out for free.

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

`make build`/`make test`/`make vet`/`make site`/`make clean` wrap the
equivalent `go` invocations — see `Makefile`. Each command's `--help`
has the full flag reference and rationale; what follows fills in only
what `--help` doesn't already cover.

`site` also renders a `/dashboard/` page — corpus size over time, prefix
family sizes, a survival-time histogram, and the most-cited remembered
keys (a proxy for "load-bearing, risky to forget") — each as inline SVG
generated server-side, so the site stays zero-JavaScript. Internal links
are page-relative by default, so `_site/index.html` works straight off
disk via `file://`; pass `--base-url` for absolute, prefixed links when
deploying under a URL path prefix (e.g. a project-level GitHub Pages
site).

`search`'s on-disk cache makes an unchanged archive a ~50ms hit and a
changed one a ~0.6s rebuild. `--fuzzy` is worth remembering during a
pruning review specifically — edit-distance matching catches "have I
already written something like this" before you decide keep/forget/merge.

`sync` refuses to run if it would close more than 20% of the
currently-remembered records in one go (not a number `--help` states);
`--force` overrides it.

### Pruning a round

`triage`/`chunk`/`validate`/`apply` implement a round-by-round review
workflow for shrinking the remembered set without losing anything
load-bearing — each command's `--help` has the full mechanics:

1. `triage` (optional) tags every remembered key with heuristics, to
   prioritize which batches need careful review.
2. `chunk --round <name>` buckets remembered keys into
   `rounds/<name>/batches/<batch-id>/` bundles for review. Scope later
   rounds to `--delta-since <RFC3339>` (new-or-changed keys only, instead
   of re-reviewing an already-scrubbed survivor set) or `--keys-file
   <path>` (an exact, cross-cutting key list for an ad-hoc audit).
3. **Review** — spawn one agent per batch (or review by hand) using
   `rounds/review-instructions.md`'s decision tree (forget/merge/promote/
   keep, bias toward keep), writing `rounds/<name>/batches/<batch-id>/manifest.jsonl`.
   `rounds/mandatory-keep.txt` lists keys no verdict may forget or merge away.
4. `validate --round <name>` checks every batch's manifest for full
   coverage, valid verdicts, and merge-row consistency.
5. `apply --round <name>` combines every batch's manifest and applies
   forget/merge decisions to `memories.jsonl` — dry-run by default,
   `-f`/`--force` to write. Pass `--source <angr-checkout>` alongside
   `--force` to also push forgets to the live `bd` corpus.

The corpus has gone through several rounds of this since 2026-06-04 —
four rounds shrank it 1,296 → 1,000 by 2026-07-20, and more have followed
as `sync` pulled in new keys since (see git history for round-by-round
provenance; the tooling behind the earliest rounds was Python and
`bd`-coupled, deleted when this repo was rebuilt as a JSONL-native
archive).

## Current snapshot

As of the last `sync`:

| | remembered | forgotten |
|---|---:|---:|
| total | 918 | 1,520 |

3 keys have been forgotten and later recreated at least once.
