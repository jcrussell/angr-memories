# Plan: Exhaustive review of all 832 bd memories (round 2)

## Context

Round 1 (2026-06-04, see `.bd-prune/README.md`) used heuristic triage +
agent chunk-review to trim 1,296 → 832 bd memories. That pass deliberately
left the durable clusters mostly untouched: **B6/B7** produced 1 forget +
3 rewrites against 279 `invariant-*` + 82 `avoid-*` = 361 inputs. Bodies
were never read end-to-end for those clusters, and ~54 status-shape
memories escaped the automated sweeps.

Round 2 reviews **every remaining memory** body-by-body. Each gets
classified **forget** (outdated / narrow-fix already in code), **merge**
(duplicate of another bd memory), **promote** (durable architecture/design
that belongs in `docs/*.rst`), or **keep**. Expected reduction: aggressive
mode on the durable cluster typically yields another 30–40%, so target
final count ≈ **500–580** memories.

User picked:
- Artifact directory: **`.bd-prune-part2/`** (sibling, not subdir)
- Review depth: **aggressive** — forget/merge anything not unique;
  duplicates of CLAUDE.md / docs / each other are forget/merge candidates
- Autonomy: **one-shot for forgets/merges; pause only before docs promotions**

## Step 0 — Plumb the existing tools to accept `--prune-dir`

`bd_triage.py`, `bd_gen_manifest.py`, and `bd_prune.py` all hard-code
`Path(".bd-prune")`. Add a single `--prune-dir` flag to each, defaulting
to `.bd-prune` so round-1 reruns are unaffected.

Files:
- `tools/bd_triage.py` — replace module-level `PRUNE_DIR = ROOT / ".bd-prune"`
  with argparse-driven dir; resolve `SNAPSHOT`/`TRIAGE_CSV`/`CITES_JSON` from it.
- `tools/bd_gen_manifest.py` — same pattern; also add `--future-date` and
  `--stale-file-ref` filter flags (mirror existing `--status-shape`).
  Also: **sync `DEFAULT_KEEP_LIST` with CLAUDE.md's quick-keys** —
  `avoid-bd-remember-without-key-flag` is in CLAUDE.md:54 but missing from
  the keep-list (12 vs 13 anchors). Add it.
- `tools/bd_prune.py` — same pattern; threads through to `_load_cites_graph`,
  `_load_decisions`, `_append_decision`. **Round-2 `decisions.jsonl` lives in
  `.bd-prune-part2/`** (separate per-round audit log; round-1's stays put).

Smoke-test the patches before using them for mutations:
```bash
python3 tools/bd_triage.py --prune-dir .bd-prune          # rebuilds round-1 triage; should match git-diff = empty
python3 tools/bd_prune.py --prune-dir .bd-prune .bd-prune/manifest-B0.jsonl  # dry-run; should print all skipped (already-applied)
```

This is the only code change in round 2. Land it as its own commit so it
can be reverted independently if needed.

## Step 1 — Refresh inputs

```bash
mkdir -p .bd-prune-part2
bd memories --json > .bd-prune-part2/snapshot-pre-prune.json
python3 tools/bd_triage.py --prune-dir .bd-prune-part2
```

Outputs:
- `.bd-prune-part2/snapshot-pre-prune.json`
- `.bd-prune-part2/triage.csv`
- `.bd-prune-part2/cites-graph.json`

## Step 2 — Cheap automated sweeps (no agent)

Three forget-only manifests. Mandatory keep-list applies automatically.

```bash
# R2.1 escaped status-shape (~54 keys)
python3 tools/bd_gen_manifest.py R2-status --status-shape \
    --prune-dir .bd-prune-part2 \
    --out .bd-prune-part2/manifest-R2-status.jsonl
python3 tools/bd_prune.py --prune-dir .bd-prune-part2 \
    .bd-prune-part2/manifest-R2-status.jsonl            # dry-run
python3 tools/bd_prune.py --prune-dir .bd-prune-part2 \
    .bd-prune-part2/manifest-R2-status.jsonl --apply

# R2.2 future-date (~4 keys) — uses new --future-date flag from Step 0
python3 tools/bd_gen_manifest.py R2-future --future-date \
    --prune-dir .bd-prune-part2 \
    --out .bd-prune-part2/manifest-R2-future.jsonl
# dry-run + apply

# R2.3 stale-file-ref (~5 keys)
python3 tools/bd_gen_manifest.py R2-stale --stale-file-ref \
    --prune-dir .bd-prune-part2 \
    --out .bd-prune-part2/manifest-R2-stale.jsonl
# dry-run + apply
```

After all three sweeps land, re-snapshot + re-triage so the cited-by guard
reflects the shrunken corpus:

```bash
bd memories --json > .bd-prune-part2/snapshot-pre-prune.json
python3 tools/bd_triage.py --prune-dir .bd-prune-part2
```

## Step 3 — Agent-review batches (aggressive)

After cheap sweeps, ~720 memories remain. Chunk by **key prefix family**
(triage.csv `prefix` column):

| Batch ID    | Selector                                          | Approx size |
|-------------|---------------------------------------------------|-------------|
| R2-INV-1..4 | `prefix == "invariant"`, alpha-split into 4       | 279 → ~70 ea |
| R2-AV-1..2  | `prefix == "avoid"`, alpha-split into 2           | 82 → ~41 ea  |
| R2-V5A5     | `prefix == "v5a5"` (post-B2 residue)              | ~50         |
| R2-OPS      | prefix in {bd,ralph,dolt,git,gh,ci,github,claude-code} | ~80    |
| R2-BEAD-1..4| bead-id-prefixed keys, alpha-split                | ~250 → ~63 ea |
| R2-CATCH    | everything else                                   | ~50         |

Total: ~13 chunks. Cap any chunk at **70 keys**.

For each batch:

1. **Build bodies bundle** at `.bd-prune-part2/bodies-R2-<batch>.json` —
   `{key: body}` JSON of the chunk's keys from the current snapshot.
2. **Render cites subset** — `cites-graph.json` restricted to keys
   touching the chunk (both incoming and outgoing edges), so the agent
   sees which anchors it must not orphan even if they live in other batches.
3. **Spawn one Plan agent per batch in parallel** (up to 3 concurrently)
   with this prompt scaffold:
   - keep-list (DEFAULT_KEEP_LIST literal, post-Step-0 sync = 13 anchors)
   - subset cites-graph for the chunk
   - verbatim "Where context lives" table from `CLAUDE.md:28-38`
   - explicit allow-list of valid `promoted_to` paths: every basename
     under `docs/advanced-topics/*.rst` + `docs/extending-angr/*.rst`
   - bodies bundle
   - aggressive-mode decision tree:
     * **forget** when: (a) memory cites "fixed in commit X" / "landed" /
       "merged" + a hash and the body adds nothing the commit message
       wouldn't, (b) the body's first 60 chars appear verbatim in
       CLAUDE.md or any `docs/*.rst`, (c) it's an iteration receipt, or
       (d) a more descriptive memory in the same cluster supersedes it
     * **merge** when ≥2 keys describe the same invariant/rule. Pick
       most descriptive as canonical; concatenate distinct content into
       `merged_body`
     * **promote** when ≥800 chars AND durable architecture/design AND
       fits exactly one allow-list .rst AND not already there. If
       uncertain about target, downgrade to keep — DO NOT guess paths.
     * **keep** is the residual
   - required output: JSONL on the manifest schema from `bd_prune.py`'s
     docstring (key, verdict, reason, batch, plus verdict-specific fields)
4. Manifest → `.bd-prune-part2/manifest-R2-<batch>.jsonl`.

## Step 4 — Split manifest by verdict

Write a tiny helper `tools/bd_split_manifest.py` that takes a manifest
path and emits `<stem>-auto.jsonl` (forget/merge/keep) and
`<stem>-promote.jsonl` (promote only). Per user choice, auto applies
without pause; promotes pause for review.

Also write a pre-apply **promote target validator** in the same script
or as `tools/bd_validate_promotes.py`: assert every `promoted_to` is in
the allow-list AND the file exists. Reject the promote manifest
otherwise — agent re-runs the row or downgrades to keep.

## Step 5 — Application order

1. **Cheap sweeps** (Step 2) — apply, re-snapshot, re-triage.
2. **Agent batches**, in this order (most domain-coherent → catch-all):
   R2-INV-1..4 → R2-AV-1..2 → R2-OPS → R2-V5A5 → R2-BEAD-1..4 → R2-CATCH.
3. Per batch:
   - split into `-auto.jsonl` + `-promote.jsonl`
   - validate promote targets
   - `bd_prune.py --prune-dir .bd-prune-part2 <batch>-auto.jsonl` (dry-run, then `--apply`)
   - re-snapshot + re-triage so the next batch's cited-by guard is fresh
   - if `<batch>-promote.jsonl` non-empty: surface to user with one-line
     summary per row (`key → target.rst — reason`); wait for greenlight,
     then dry-run + apply
4. **Cross-batch merge collisions**: `bd_prune.py::validate()` only
   catches in-manifest collisions. Re-snapshot between batches so the
   next agent doesn't see an already-forgotten key in its bodies bundle.
   Idempotency in `decisions.jsonl` catches re-forgets but not merges
   into keys that no longer exist — re-triage is the safety net.
5. **Orphans-unblocked closer**: at the end, regenerate the status-shape
   manifest one more time (`bd_gen_manifest.py R2-orph --status-shape …`).
   Apply any newly unblocked forgets.

## Step 6 — Phase 4 cleanup (mirror round 1)

1. `bd memories | wc -l` → update count line at **`CLAUDE.md:7`**
   (currently "pruned to ~870 on 2026-06-04 from a peak of 1,296").
2. Walk new snapshot for quick-keys at **`CLAUDE.md:45-57`**: confirm
   every anchor still exists; add any new anchors that emerged from
   round-2 merges.
3. Re-check "Where context lives" table at **`CLAUDE.md:28-38`**: any
   topic whose entry-point bd-key was merged → swap to new canonical.
4. `bd memories --json > .bd-prune-part2/snapshot-post-prune.json`.
5. Append a "Round 2 batches" section to `.bd-prune/README.md` with the
   same table shape as round 1, plus a pointer to `.bd-prune-part2/`.
6. `~/.claude/projects/-home-ubuntu-repos-angr/memory/MEMORY.md` stays a
   stub; touch only if it has drifted.

## Critical files

- `tools/bd_triage.py` (patch: `--prune-dir` flag)
- `tools/bd_gen_manifest.py` (patch: `--prune-dir`, `--future-date`,
  `--stale-file-ref` flags; sync DEFAULT_KEEP_LIST with CLAUDE.md)
- `tools/bd_prune.py` (patch: `--prune-dir` flag)
- `tools/bd_split_manifest.py` (new; trivial)
- `tools/bd_validate_promotes.py` (new; trivial — or inline into splitter)
- `.bd-prune-part2/` (new directory; all round-2 artifacts incl. own
  `decisions.jsonl`)
- `CLAUDE.md` (Step 6 edits to lines 7, 28-38, 45-57)
- `.bd-prune/README.md` (Step 6 round-2 batch table append)

## Verification

- `bd memories | head -1` matches the new `CLAUDE.md:7` count line.
- All 13 keep-list anchors survive:
  ```bash
  python3 -c "
  import json, subprocess
  KEEPS = ['core-goal-design-philosophy','rust-proxy-architecture-decision',
           'rust-engine-claude-md-pointer','rust-python-boundary-audit',
           'constraint-export-no-pre-pin','characterization-vs-fix-pattern',
           'benchmark-bimodal-variance-rules','env-venv-corruption',
           'bd-update-notes-overwrites','avoid-bd-remember-without-key-flag',
           '9maq-bisect-method','venv-rebuild-cargo-direct-copy',
           'avoid-pip-install-deps']
  snap = json.loads(subprocess.check_output(['bd','memories','--json']))
  missing = [k for k in KEEPS if k not in snap]
  assert not missing, missing
  print('keep-list OK')
  "
  ```
- Each promoted memory landed in exactly one .rst:
  ```bash
  for key in $(jq -r 'select(.verdict=="promote") | .key' \
                .bd-prune-part2/decisions.jsonl); do
      hits=$(grep -rl "Originally captured.*as bd memory \`\`${key}\`\`" docs | wc -l)
      [ "$hits" = "1" ] || echo "BAD: $key landed in $hits files"
  done
  ```
- `pre-commit run --files docs/**/*.rst CLAUDE.md` is clean.
- Final `python3 tools/bd_triage.py --prune-dir .bd-prune-part2` on the
  post-snapshot shows zero `closed_only && status_shape` rows.
- `wc -l .bd-prune-part2/decisions.jsonl` ≈ sum of round-2 manifest
  line-counts (modulo idempotency skips).

## Risks + mitigations

- **invariant-* (279) too big for one agent.** Alpha-split into 4 chunks
  of ~70. Each chunk also gets the cites-graph subset so cross-chunk
  anchors don't get orphaned.
- **Agent picks wrong/nonexistent docs target.** Three nets: (1)
  pre-apply validator rejects unknown paths, (2) `bd_prune.py::do_promote`
  has distinctive-phrase dup-check + 2000-line bloat guard, (3) user
  greenlights every promote manifest before apply.
- **Agent over-forgets a durable invariant that overlaps with CLAUDE.md.**
  CLAUDE.md is short; memories are detailed. Bias prompt: forget only if
  the memory adds nothing beyond CLAUDE.md's mention. When in doubt: keep.
- **Cross-batch merge collision.** `validate()` is in-manifest only.
  Re-snapshot between batches → next agent's bodies bundle is fresh.
  `decisions.jsonl` idempotency catches double-forgets but merges into a
  vanished canonical would silently fail; re-triage is the backstop.
- **Cited-by deferrals accumulate.** Round 1 needed `orphans-unblocked`.
  Step 5 step 5 mirrors that.
- **`avoid-bd-remember-without-key-flag` not in DEFAULT_KEEP_LIST.**
  Sync the keep-list to CLAUDE.md's 13-anchor quick-keys in Step 0,
  BEFORE the cheap sweeps run.
- **Round-2 `decisions.jsonl` separate from round 1.** Audit recovery
  needs to concatenate both. Document this in the round-2 README append.
