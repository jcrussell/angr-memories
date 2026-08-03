# angr-memories

A continuously-synced, browsable archive of the `angr` project's
`bd`-backed memory corpus: which memories are currently remembered, which
were forgotten (when, and why where known), and how long each one
survived.

Originally built from four curated pruning rounds (2026-06-04 through
2026-07-20, 1,296 peak memories down to 1,000 survivors — see git history
for that provenance if you need it). Since then it's kept current via
`angr-memories sync`, which pulls the live corpus and folds in whatever
changed.

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
angr-memories chunk    --round R5 [--delta-since <RFC3339>]         # bucket remembered keys into review batches
angr-memories validate --round R5                                   # check every batch's manifest
angr-memories apply    --round R5 [--apply] [--force]                # apply a round's manifests to memories.jsonl
```

`site` groups memories by key-prefix family (`invariant`, `avoid`, `z3`,
`rust`, `benchmark`, `ops`, `v5a5`, `inspect`, `syscall`, and a `catch`-all)
and renders one page per family — split into several pages for the largest
families so no single page balloons past a reasonable size. It also renders
a `/dashboard/` page: corpus size over time, prefix family sizes, a
survival-time histogram, and the most-cited remembered keys (a proxy for
"load-bearing, risky to forget"), each as inline SVG generated server-side
— the site stays zero-JavaScript, so charts are baked into the static HTML
rather than drawn client-side. Internal links are root-relative (`/dashboard/`,
`/prefix/...`), so the rendered `_site/` must be served from a domain root
(a plain static file server, or a user/org-level GitHub Pages site) — it
won't work opened directly via `file://` or hosted under a URL path prefix.

`search` ranks matches with a [bleve](https://github.com/blevesearch/bleve)
index, cached on disk under the OS cache directory
(`$XDG_CACHE_HOME/angr-memories` on Linux) and keyed by a content hash of
`memories.jsonl`: an unchanged archive is a cache hit (~50ms on the real
corpus); any change from `sync`/`apply` produces a new hash and rebuilds
automatically on the next search (~0.6s), garbage-collecting the old
entry. `--no-cache` skips the cache entirely (always rebuilds in-memory) —
useful for a one-off run against a scratch `memories.jsonl` you don't want
polluting the cache. `<query>` is bleve's query-string syntax by default
(phrase/boolean operators); `--fuzzy` switches to edit-distance matching
for typo-tolerant or near-duplicate lookups — useful during a pruning
review to check "have I already written something like this" before
deciding to keep/forget/merge a memory. Remembered records only by
default; `--all` includes forgotten ones.

`sync` runs `bd memories --json` against a live `bd`-backed project
(auto-discovering its `.beads/` DB) and diffs the result against the
currently-remembered records: new keys get appended, keys that vanished get
closed, and keys whose body changed get updated in place — all without
needing to know anything about *why* the corpus changed. Safe to run
repeatedly; a no-op against an unchanged corpus. Refuses to run if the live
snapshot is empty, or would close more than 20% of the currently-remembered
records in one go (an empty or truncated `bd` response would otherwise read
as "everything was forgotten") — pass `--force` to override. Writes are
atomic (temp file + rename), so a crash mid-write can't corrupt
`memories.jsonl`. Wire it to a cron job or similar to keep the archive
current.

### Pruning a round

`triage`/`chunk`/`validate`/`apply` port the round-by-round pruning
workflow that shrank the corpus 1,296 → 1,000 over four rounds in June/July
2026 (see git history — the original tooling was Python and `bd`-coupled,
deleted when this repo was rebuilt as a JSONL-native archive) onto the new
data model:

1. `triage` (optional) tags every remembered key with heuristics —
   status/closure shape, doc-length-and-language, stale/future dates,
   citing another memory, a verbatim fragment match against `--docs-dir`,
   and (with `--source`) citing a repo path `git ls-files` doesn't know
   about or resolving only to closed beads. Use the tags to prioritize
   which batches need careful review.
2. `chunk --round <name>` buckets remembered keys by prefix family into
   `rounds/<name>/batches/<batch-id>/{bodies.json,cites.json}` bundles,
   alpha-splitting any family over `--chunk-cap` (default 70). Pass
   `--delta-since <RFC3339>` on a round after the first to restrict to keys
   first seen or updated since that time — reviewing an already-scrubbed
   survivor set from scratch measures ~0.4% forget yield vs 9-26% on
   genuinely new/changed keys, so scope later rounds to the delta.
3. **Review** — spawn one agent per batch (or review by hand) using
   `rounds/review-instructions.md`'s decision tree (forget/merge/promote/
   keep, bias toward keep), writing `rounds/<name>/batches/<batch-id>/manifest.jsonl`.
   `rounds/mandatory-keep.txt` lists keys no verdict may forget or merge away.
4. `validate --round <name>` checks every batch's manifest: full coverage
   of its bodies bundle, valid verdicts, the mandatory-keep list, and
   merge-row consistency.
5. `apply --round <name>` combines every batch's manifest, re-validates,
   and applies forget/merge decisions to `memories.jsonl` — dry-run by
   default, `--apply` to write. A citation orphan guard defers (doesn't
   silently drop) any forget still cited by a surviving memory outside the
   manifest; `--force` skips it. Promote rows are reported, never
   auto-applied — the target is another repo's docs, so a human adds the
   content and forgets the key in a follow-up round. `--apply` also
   appends round/batch provenance to `rounds/<name>/decisions.jsonl`.

## Current snapshot

As of the last `sync`:

| | remembered | forgotten |
|---|---:|---:|
| total | 1,289 | 1,149 |

3 keys have been forgotten and later recreated at least once.

## Layout

```
memories.jsonl                the canonical data
cmd/angr-memories/main.go     entrypoint
internal/memory/              Memory struct, JSONL load/save, prefix + chunking + aggregate logic
internal/prune/                pruning-round mechanics: triage tags, citation graph, manifest validate/apply
internal/chart/                 hand-rolled inline-SVG chart primitives (bar, line) -- no charting dependency
internal/site/                static site generator (goldmark + Chroma, styled after byob-go-cli); dashboard.go builds the /dashboard/ charts
pkg/cmd/{site,stats,sync,search}/    CLI subcommands
pkg/cmd/{triage,chunk,validate,apply}/  pruning-round CLI subcommands
rounds/                        pruning-round artifacts: mandatory-keep.txt, review-instructions.md, rounds/<name>/
```
