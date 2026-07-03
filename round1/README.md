# .bd-prune/ — bd memories prune workflow artifacts

`bd memories` grew to 1,296 entries (≈948 KB) — most of it was per-iteration
ralph-loop scratch (status snapshots, "X landed in commit Y", bead-closure
receipts). The corpus was pruned on **2026-06-04** down to **832** memories
(36% reduction). This directory holds the workflow artifacts and the
recovery path if something load-bearing was forgotten by mistake.

This directory is gitignored. Treat it as ephemeral — but keep it around
until you're sure nothing important was lost.

## Files

| File                            | What it is                                                    |
|---------------------------------|---------------------------------------------------------------|
| `snapshot-pre-prune.json`       | Full `bd memories --json` dump *before* any pruning           |
| `snapshot-post-prune.json`      | Full `bd memories --json` dump after Phase 4                  |
| `triage.csv`                    | Phase 1 heuristic tags per key (closed_only, status_shape, …) |
| `cites-graph.json`              | `{citer_key: [cited_keys]}` — used by the orphan guard        |
| `bodies-B*.json`                | Per-batch input bundles handed to review agents               |
| `manifest-B*.jsonl`             | Per-batch action manifests (one decision per line)            |
| `decisions.jsonl`               | Append-only log of every applied action (key, verdict, time)  |

## Recovery

If you discover that a forgotten memory was actually load-bearing:

```bash
# Find it in the pre-prune snapshot
python3 -c "import json; d=json.load(open('.bd-prune/snapshot-pre-prune.json')); print(d['<key>'])"

# Restore it (use --key explicitly — see avoid-bd-remember-without-key-flag)
bd remember "$(python3 -c "import json; print(json.load(open('.bd-prune/snapshot-pre-prune.json'))['<key>'])")" --key <key>
```

To audit what was forgotten:

```bash
jq -r 'select(.verdict=="forget") | .key + "\t" + .reason' .bd-prune/decisions.jsonl | sort | less
```

To find which batch deleted a specific key:

```bash
jq -r 'select(.key=="<key>")' .bd-prune/decisions.jsonl
```

## Workflow scripts

The two scripts that drove the prune live in `tools/`:

- `tools/bd_triage.py` — emits `triage.csv` + `cites-graph.json` from a
  `bd memories --json` dump
- `tools/bd_prune.py` — applies a manifest (dry-run by default; pass
  `--apply` to mutate bd)
- `tools/bd_gen_manifest.py` — generates a forget-manifest from
  triage.csv + tag filters

## Batches applied (2026-06-04 session)

| Batch  | Description                                       | Net memories removed |
|--------|---------------------------------------------------|----------------------|
| B2     | v5a5-* cluster collapse (46 → 11)                 | −35 forget, −1 merge |
| B0     | dup-of-claude-md / dup-of-docs                    | −8                   |
| B1     | closed-only + status-shape (safe sweep)           | −60                  |
| B1-rule| 23 rule-shaped reviewed manually                  | −6                   |
| B4-c1  | closed-only NOT status-shape (chunk 1, agent)     | −122                 |
| B4-c2  | closed-only NOT status-shape (chunk 2, agent)     | −98                  |
| B4-c3  | closed-only NOT status-shape (chunk 3, agent)     | −94                  |
| B67    | invariant-* + avoid-* no-bead (conservative)      | −1 forget, +3 rewrite|
| B5     | docs-shape promote candidates                     | −2                   |
| B9-c1  | catch-all chunk 1 (agent)                         | −21                  |
| B9-c2  | catch-all chunk 2 (agent)                         | −7                   |
| B8     | reference-type + user-preference (no-op review)   | 0                    |
| orphans-unblocked | deferred orphan-targets whose citers were forgotten | −9 |

## Phase 4 cleanup

CLAUDE.md updated with:

- Refreshed memory-system count line
- New "Where context lives" subsection — topic → docs → bd-key navigation
- Expanded quick-keys list (now 13 mandatory anchors)

`~/.claude/projects/-home-ubuntu-repos-angr/memory/MEMORY.md` replaced
with a stub pointing at CLAUDE.md's navigation hub.

## Round 2 (2026-06-05) — exhaustive review

Round 1 left the durable clusters (invariant-* + avoid-*) only conservatively
touched. Round 2 reviewed every remaining memory body-by-body via agent
batches with an **aggressive** decision tree (forget/merge anything not
unique). Final: **832 → 414 memories** (50.2% reduction, 585 KB → 289 KB).

Round-2 artifacts live in **`.bd-prune-part2/`** (sibling, not subdir).
The round-2 `decisions.jsonl` is separate from round 1; concatenate both
files for cross-round audit.

### Tooling changes (round 2)

Three scripts gained a `--prune-dir` flag so future rounds don't need
the symlink trick: `tools/bd_triage.py`, `tools/bd_gen_manifest.py`,
`tools/bd_prune.py`. New filter flags on `bd_gen_manifest.py`:
`--future-date`, `--stale-file-ref`. `DEFAULT_KEEP_LIST` synced to
CLAUDE.md's 13-anchor quick-keys (added `avoid-bd-remember-without-key-flag`).
`bd_gen_manifest.py` cited-by guard now iterates to fixed point.

Two new helpers in `tools/`:
- `bd_split_manifest.py` — splits an agent manifest into `-auto.jsonl`
  (forget/merge/keep) and `-promote.jsonl`; validates `promoted_to` paths
  against an allow-list.
- `bd_apply_with_defer.py` — wraps `bd_prune.py --apply` to auto-defer
  rows that would orphan citations; loops until validation passes.
- `bd_chunk_for_review.py` — buckets the snapshot by prefix family and
  emits per-batch bodies + cites subsets for agents.

### Batches applied (2026-06-05 session)

| Batch              | Description                                              | Net removed |
|--------------------|----------------------------------------------------------|-------------|
| R2-status          | escaped status-shape (cited-by guard fixed-point)        | 52          |
| R2-future          | future-dated narratives (2026-08+)                       | 2           |
| R2-stale           | stale file references                                    | 10          |
| R2-INVARIANT-1..4  | 260 invariant-* memories, alpha-split into 4 chunks      | 85          |
| R2-AVOID-1..2      | 71 avoid-* memories                                      | 26          |
| R2-Z3              | 29 z3-* memories                                         | 14          |
| R2-RUST            | 25 rust-* memories                                       | 12          |
| R2-BENCHMARK       | 30 benchmark-* memories (many dated snapshots)           | 14          |
| R2-INSPECT         | 11 inspect-* memories                                    | 6           |
| R2-SYSCALL         | 11 syscall-* memories                                    | 6           |
| R2-OPS-V5A5        | 12 dev-tooling + v5a5-* residue (full v5a5 sweep)        | 9           |
| R2-CATCH-1..5      | 319 mixed-prefix catch-all (alpha-split into 5 chunks)   | 122         |
| R2-CITERS          | re-review of citers blocking deferred forgets            | 11          |
| R2-orphans-final   | force-applied after manual citer-anchor surgery          | 5           |
| R2-promotes-decl   | 2 INV-3 promotes user-declined (already in rust_engine.rst) | 2        |

### Decisions on promotes

Two promote candidates emerged from INV-3 (both targeting
`docs/advanced-topics/rust_engine.rst`):

1. `invariant-rust-inspect-unsupported` — supported-event matrix + bitmask layout
2. `invariant-rust-raise-option-names` — SimOption raise list with rationale

Both were declined: rust_engine.rst already has "SimOption coverage matrix"
(line 1346) and "state.inspect dispatch (partial)" sections covering the
same surface area. The agent's extra detail (bitmask 0..=7 layout,
per-option Veritesting note) was deemed too implementation-detail-y for
docs; better to live in code comments.

### Citation-graph surgery

Two mandatory-keep anchors had "see also" refs to stale memories that
otherwise blocked forgets:
- `benchmark-bimodal-variance-rules` — dropped "see also the
  `invariant-bimodal-variance-benchmarks` memory" tail
- `iop-avg-vs-hadd-distinction` + `z3-universality-parity-test-template`
  — dropped refs to `z3-spec-replay-test-template` (variant 2 inlined)
- `inspection-system` — replaced ref to `invariant-rust-inspect-unsupported`
  with pointer to rust_engine.rst's inspect section

### Recovery

```bash
# Find a forgotten round-2 key
python3 -c "import json; print(json.load(open('.bd-prune-part2/snapshot-pre-prune.json'))['<key>'])"

# Audit what was forgotten in round 2
jq -r 'select(.verdict=="forget") | .key + "\t" + .reason' .bd-prune-part2/decisions.jsonl | sort | less
```
