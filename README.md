# angr-memories

A browsable, continuously-synced archive of the `angr` project's
`bd`-backed memory corpus, plus the toolkit used to prune it.

- Render the corpus as a static site — prefix-grouped pages, a dashboard
  (corpus size over time, survival histogram, most-cited keys), a topic
  dendrogram of the whole corpus, zero JavaScript
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

angr-memories embed    [--model nomic-embed-text] [--dry-run]      # refresh embeddings.jsonl via local Ollama
angr-memories topics   [--unlabeled] [--json index,label,members] [--min-topic-size 15 | --topics N]  # list the topic clusters and their labels
angr-memories topics label <index> "<text>"                        # name one cluster (writes topic-labels.jsonl)
angr-memories topics suggest [--only N] [--relabel] [--dry-run]    # let a local chat model draft labels

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
   <angr-checkout>` to also push the round to the live `bd` corpus
   (merged bodies first, then forgets).

`--help` on each command has the full mechanics and edge cases.

## Site

The corpus renders as a browsable static site at
**https://jcrussell.github.io/angr-memories/**. It's built by
`.github/workflows/pages.yml` on every push to `main`; locally, `make
site` produces a copy in `_site/`.

The first time the site is enabled in a fork, the repo owner needs to
flip Settings → Pages → "Build and deployment → Source" to `GitHub
Actions`. After that, pushes to `main` deploy automatically. Forks that
rename the repo should update the `--base-url` and `--repo-url` flags
in `.github/workflows/pages.yml`.

### Topics

`/topics/` on the site is a dendrogram of every memory ever recorded,
clustered by meaning. Three committed files feed it:

- `memories.jsonl` — the text.
- `embeddings.jsonl` — one vector per key, produced by `angr-memories
  embed` with an embedding model served by a local [Ollama](https://ollama.com)
  (`nomic-embed-text` by default). Rows carry a hash of the body they were
  computed from; the site build refuses to run while any key is missing or
  stale, so after a `sync` run `embed` and commit the result. Only new or
  edited keys are embedded.
- `topic-labels.jsonl` — the cluster names, written by a person
  (`angr-memories topics label`) or drafted by a local chat model
  (`angr-memories topics suggest`, then review). A label written by any
  other model goes through `topics label --by llm:<model>`, so the
  `labeled_by` field always says who named the cluster. Each label is anchored to
  the memories it described, so it follows them when the tree is
  recomputed after a sync, and records the embedding model it was written
  against, so it is set aside rather than misapplied if the vectors are
  ever regenerated with a different model. Clusters nobody has named yet
  show their nearest-centre keys and are listed by `angr-memories topics
  --unlabeled`.

How many topics there are is not a fixed number: the tree is split for as
long as both halves hold at least 15 memories (`--min-topic-size`), so the
count follows the corpus. `--topics N` forces an exact count instead. The
`site` and `topics` commands take the same two flags and must agree.

The build itself is offline and pure Go: CI never talks to a model.

## Current snapshot

As of the last `sync`:

| | remembered | forgotten |
|---|---:|---:|
| total | 963 | 1,639 |

6 keys have been forgotten and later recreated at least once.
