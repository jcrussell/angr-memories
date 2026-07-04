# Round-3 bd memory review — per-batch agent instructions

You are reviewing one batch of bd memories for the round-3 prune of the angr
project's memory corpus (working dir: /home/ubuntu/repos/angr). Round 2's
process is being mirrored exactly (aggressive mode).

## Inputs (all under /home/ubuntu/repos/angr/.bd-prune-part3/)

- `bodies-<BATCH>.json` — `{key: body}` for every memory you must judge.
- `cites-<BATCH>.json` — `{"out": {key: [cited...]}, "in": {key: [citers...]}}`
  citation edges touching your batch. A key with "in" edges from keys OUTSIDE
  your batch is load-bearing — bias keep, or expect the apply step to defer
  the forget.

## Output

Write `/home/ubuntu/repos/angr/.bd-prune-part3/manifest-R3-<BATCH>.jsonl`:
one JSON object per line, EVERY key from the bodies file appearing EXACTLY once.

Base schema: `{"key": "...", "verdict": "forget|merge|promote|keep", "reason": "<short free text>", "batch": "R3-<BATCH>"}`

Verdict-specific extra fields:
- merge subordinate: add `"merged_into": "<canonical-key>"`
- merge canonical:   add `"canonical": true, "merged_body": "<full merged text>"`
- promote:           add `"promoted_to": "docs/<allow-listed>.rst", "body": "<RST-formatted body>"`

## Decision tree (aggressive mode, from round 2)

**forget** when any of:
(a) the memory says "fixed in commit X" / "landed" / "merged" with a hash and
    the body adds nothing a commit message wouldn't;
(b) the body's first ~60 chars appear verbatim in CLAUDE.md or any docs/*.rst
    (grep to check);
(c) it is an iteration receipt / bead-closure scrap ("closed angr-XXXX",
    "iteration N done", status snapshots, one-off run results);
(d) a more descriptive memory in the SAME cluster supersedes it;
(e) it describes a file, flag, script, or workflow that no longer exists
    (verify with ls/grep before concluding).

**merge** when ≥2 keys in YOUR batch describe the same invariant/rule.
Pick the most descriptive as canonical; `merged_body` = canonical body plus
the distinct content from subordinates (do not lose unique detail).
HARD RULE: `merged_into` must reference a key inside YOUR OWN batch
(cross-batch merges cause collisions with other batches' verdicts).

**promote** when ALL of: ≥800 chars AND durable architecture/design knowledge
AND fits exactly one allow-listed .rst AND is not already covered there.
If uncertain about the target, downgrade to keep — DO NOT guess paths.
Round-2 precedent: both promote candidates were declined as already-covered;
promotes should be rare.

Allow-listed promote targets: every existing file matched by
`docs/advanced-topics/*.rst` and `docs/extending-angr/*.rst` in the repo.

**keep** is the residual. Bias toward keep when the memory adds operational
detail beyond CLAUDE.md's brief mention. CLAUDE.md is short; memories are
detailed — forget only if the memory adds nothing beyond the docs/CLAUDE.md
coverage.

## Mandatory keep-list (verdict MUST be keep — never forget/merge these)

core-goal-design-philosophy, rust-proxy-architecture-decision,
rust-engine-claude-md-pointer, rust-python-boundary-audit,
constraint-export-no-pre-pin, characterization-vs-fix-pattern,
benchmark-bimodal-variance-rules, env-venv-corruption,
bd-update-notes-overwrites, avoid-bd-remember-without-key-flag,
9maq-bisect-method, venv-rebuild-cargo-direct-copy, avoid-pip-install-deps

(They may be merge CANONICALS — absorbing a subordinate is fine; being
merged away or forgotten is not.)

## Method

1. Read the bodies file and cites file completely.
2. For staleness checks you may READ anything in the repo (CLAUDE.md,
   docs/, git log, ls) and run read-only `bd show <bead-id>` / `bd list`
   to check bead status for keys that cite beads. Do NOT run any mutating
   command (no bd remember/forget/update, no file writes outside your one
   manifest file).
3. Judge every key. Verify claims of "duplicate of docs" by actually
   grepping the docs. When genuinely torn between forget and keep: keep.
4. Write the manifest. Validate it: `python3 -c` parse every line, check
   every bodies key present exactly once, check merge targets are in-batch.
5. Final message: report counts (forget/merge/promote/keep) and a one-line
   rationale for each PROMOTE (there should rarely be any).
