# angr-memories — bd memory cleanup archive

Snapshot of the two-round cleanup of the `angr` bd memory corpus
(2026-06-04 round 1 + 2026-06-05 round 2). Use this directory to:

- Study **what was forgotten** — body archives + per-decision audit logs
- Re-run a forgotten memory's recovery — pre-prune snapshots are full JSON dumps
- Reuse the **tooling** for future cleanup rounds on this or another bd-backed project
- Read back the conversation transcripts that drove the prune decisions

This is a frozen mirror of the project's `.bd-prune/` + `.bd-prune-part2/`
directories plus the live tool scripts at the time of round-2 completion.
The original directories remain in the repo (gitignored).

## Headline numbers

| | Before | After |
|---|---|---|
| Round 1 (2026-06-04) | 1,296 | 832 (−36%) |
| Round 2 (2026-06-05) | 832 | 414 (−50%) |
| Combined           | 1,296 | 414 (−68%, 948 KB → 289 KB) |

Round 1 used heuristic triage + light agent review. Round 2 did an
exhaustive body-by-body agent review with an aggressive forget/merge
posture, plus citation-graph surgery on the residue.

## Layout

```
~/angr-memories/
├── README.md            (this file)
├── round2-plan.md       (the plan that drove round 2)
├── forgotten.txt        — all 882 keys removed, with round/verdict/reason
├── survivors.txt        — all 414 surviving keys with first-line preview
├── forget_reasons.txt   — reason histogram (360 distinct reasons, sorted by count)
├── merges.txt           — 36 merges: subordinate → canonical
├── batch_stats.txt      — per-batch verdict counts
├── claude-md/
│   ├── CLAUDE.md.pre-round2  — state at last git commit (before round 1)
│   ├── CLAUDE.md.post-round2 — current state (after both rounds)
│   └── CLAUDE.md.diff        — unified diff
├── tools/               (the six scripts, post-patch state)
│   ├── bd_triage.py             — heuristic tagger; emits triage.csv + cites-graph
│   ├── bd_gen_manifest.py       — turns triage tags into a forget manifest
│   ├── bd_prune.py              — applies a manifest (forget/merge/promote/keep)
│   ├── bd_split_manifest.py     — splits a manifest into -auto + -promote halves
│   ├── bd_apply_with_defer.py   — wraps bd_prune.py with auto-defer on orphan errors
│   └── bd_chunk_for_review.py   — buckets a snapshot by prefix family for agents
├── round1/              (full .bd-prune/ snapshot from 2026-06-04)
│   ├── README.md                — round 1 workflow notes
│   ├── snapshot-pre-prune.json  — all 1,296 memories at start of round 1
│   ├── snapshot-post-prune.json — all 832 memories at end of round 1
│   ├── triage.csv               — heuristic tags per key
│   ├── cites-graph.json         — {citer: [cited]} citation graph
│   ├── manifest-B*.jsonl        — per-batch decisions
│   ├── bodies-B*.json           — bodies handed to round-1 agents
│   └── decisions.jsonl          — append-only audit log (1226 rows)
├── round2/              (full .bd-prune-part2/ snapshot from 2026-06-05)
│   ├── batch-index.json         — {batch_id: [keys]} chunking record
│   ├── snapshot-pre-prune.json  — all 832 memories at start of round 2
│   ├── snapshot-post-prune.json — all 414 memories at end of round 2
│   ├── triage.csv               — heuristic tags per key (final state)
│   ├── cites-graph.json         — final citation graph
│   ├── manifest-R2-*.jsonl      — per-batch decisions
│   ├── manifest-R2-*-auto.jsonl — split: forget/merge/keep half
│   ├── manifest-R2-*-promote.jsonl       — split: promote half (empty for most batches)
│   ├── manifest-R2-*-auto-deferred.jsonl — orphan-deferred rows from auto-apply
│   ├── bodies-R2-*.json         — bodies handed to round-2 agents (per batch)
│   ├── cites-R2-*.json          — per-batch citation subset given to agents
│   ├── decisions.jsonl          — append-only audit log (842 rows)
│   └── intermediate/            — temp manifests from the citation-surgery phase
│       (orphans retries, promotes-declined, last3 cleanup, etc.)
└── claude-logs/
    ├── round1-session.jsonl     — Claude Code transcript driving round 1
    └── round2-session.jsonl     — Claude Code transcript driving round 2
```

## Common operations

### Quick survey of what's gone

```bash
# Top 20 most-common forget reasons
sort forget_reasons.txt | tail -20

# Every key forgotten in round 2 with its reason
grep '^round2' forgotten.txt | awk -F'\t' '{print $3, $4}'

# How aggressive was each batch?
cat batch_stats.txt
```

### List every key forgotten across both rounds

```bash
jq -r 'select(.verdict=="forget") | .key' round1/decisions.jsonl round2/decisions.jsonl | sort -u
# or, with the pre-baked study aid:
awk -F'\t' '{print $3}' forgotten.txt | sort -u
```

### Read a forgotten key's body

```bash
# Was it forgotten in round 1?
python3 -c "import json; d=json.load(open('round1/snapshot-pre-prune.json')); print(d['<key>'])"

# Or round 2? (round-2 pre-prune is the round-1 post-prune state)
python3 -c "import json; d=json.load(open('round2/snapshot-pre-prune.json')); print(d['<key>'])"
```

### Find what reason was given for forgetting a specific key

```bash
jq -c 'select(.key=="<key>")' round1/decisions.jsonl round2/decisions.jsonl
```

### See per-batch agent reasoning

The conversation transcripts in `claude-logs/` capture each agent's
report (counts, representative forgets, uncertain cases). Each round-2
agent invocation produced a ~250-word summary block — search for
"R2-INVARIANT-3" etc. in `round2-session.jsonl`.

```bash
# Show only the assistant turns from the round-2 session
python3 -c "
import json
for ln in open('claude-logs/round2-session.jsonl'):
    r = json.loads(ln)
    if r.get('type') == 'assistant' and r.get('message', {}).get('content'):
        for c in r['message']['content']:
            if c.get('type') == 'text':
                print(c['text']); print('---')
" | less
```

### Reuse the tools on another bd-backed project

The six scripts in `tools/` are self-contained. To kick off a cleanup:

```bash
cd <your-project>
mkdir -p .bd-prune
bd memories --json > .bd-prune/snapshot-pre-prune.json
python3 /path/to/tools/bd_triage.py --prune-dir .bd-prune
python3 /path/to/tools/bd_chunk_for_review.py --prune-dir .bd-prune
# then per-batch: spawn an agent, write manifest, then:
python3 /path/to/tools/bd_split_manifest.py .bd-prune/manifest-X.jsonl
python3 /path/to/tools/bd_apply_with_defer.py .bd-prune/manifest-X-auto.jsonl --prune-dir .bd-prune
```

The mandatory keep-list (`DEFAULT_KEEP_LIST` in `bd_gen_manifest.py`)
is angr-specific — replace with your project's anchors before reuse.

## Round-2 highlights worth re-reading

- **Aggressive prompt design** (round1 vs round2 INV-1): the first
  INV-1 attempt with mild prompting produced 2 forgets out of 65; the
  re-run with explicit "the bar for keep is X" framing produced 18
  forgets + 4 merges. See `claude-logs/round2-session.jsonl` for the
  diff.
- **Cited-by guard fixed-point bug**: round-1's `bd_gen_manifest.py`
  did one pass and missed a chain orphan
  (`avoid-dfs-coupling-for-shared-lineage`). Round 2 fixed this — see
  the patch in `tools/bd_gen_manifest.py` `while True` block.
- **Citation surgery on mandatory keeps**: three CLAUDE.md anchors had
  "see also X" refs to stale targets. Surgery let the targets go
  without losing anchor content. Search the round-2 log for
  "benchmark-bimodal-variance-rules" or "iop-avg-vs-hadd-distinction".
- **Promote candidates declined**: rust_engine.rst already covered the
  state.inspect supported-event matrix and SimOption raise list; INV-3
  agent's promotes were rejected. Useful precedent for "when in doubt,
  forget rather than duplicate".

## Recovery

If a forgotten key turns out to be load-bearing:

```bash
python3 -c "
import json, subprocess
# Try round-2 snapshot first (round-1 post-prune == round-2 pre-prune)
for snap in ('round2/snapshot-pre-prune.json', 'round1/snapshot-pre-prune.json'):
    d = json.load(open(snap))
    if '<key>' in d:
        body = d['<key>']
        subprocess.run(['bd','remember', body, '--key', '<key>'], check=True)
        print(f'restored from {snap}')
        break
else:
    raise SystemExit('not found')
"
```

## Provenance

- Round 1 driven by Claude Code session `671c1d6c-cb62-4d8c-ad0e-da0fb1c2f733`
- Round 2 driven by Claude Code session `194c8d59-ecfc-43df-92f8-149659bfbd57`
- Tooling commits live in the angr repo on the `rust-symex` branch (uncommitted as of archive time)
- Original `.bd-prune/` and `.bd-prune-part2/` remain in `/home/ubuntu/repos/angr/`
