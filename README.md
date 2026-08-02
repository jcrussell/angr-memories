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
{"key": "invariant-solver-eval-timing", "body": "...", "first_seen": "2026-06-04T00:00:00Z", "decided_at": "", "reason": "", "merged_into": ""}
```

- `key` — the bd memory key
- `body` — last known body text
- `first_seen` — when this record is first known to have existed (a floor,
  not necessarily the true creation date)
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
angr-memories site  --memories memories.jsonl --out _site   # render the browsable static site
angr-memories stats --memories memories.jsonl               # remembered/forgotten counts by prefix
angr-memories sync  --source ~/repos/angr --memories memories.jsonl  # pull the live corpus, fold in changes
```

`site` groups memories by key-prefix family (`invariant`, `avoid`, `z3`,
`rust`, `benchmark`, `ops`, `v5a5`, `inspect`, `syscall`, and a `catch`-all)
and renders one page per family — split into several pages for the largest
families so no single page balloons past a reasonable size.

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
internal/memory/              Memory struct, JSONL load/save, prefix + chunking logic
internal/site/                static site generator (goldmark + Chroma, styled after byob-go-cli)
pkg/cmd/{site,stats,sync}/    CLI subcommands
```
