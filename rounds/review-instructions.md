# bd memory review — per-batch agent instructions

You are reviewing one batch of remembered memories for a pruning round of
the `angr-memories` archive. This decision tree is unchanged since round 2
(aggressive mode) — it has been copied verbatim into every round since,
including this port from the deleted Python tooling to `angr-memories
chunk`/`validate`/`apply`.

## Inputs (under `rounds/<round>/batches/<BATCH>/`)

- `bodies.json` — `{key: body}` for every memory you must judge.
- `cites.json` — `{"out": {key: [cited...]}, "in": {key: [citers...]}}`
  citation edges touching your batch. A key with `"in"` edges from keys
  OUTSIDE your batch is load-bearing — bias keep, or expect the `apply`
  step's citation orphan guard to defer the forget anyway.

## Output

Write `rounds/<round>/batches/<BATCH>/manifest.jsonl`: one JSON object per
line, EVERY key from `bodies.json` appearing EXACTLY once.

Base schema: `{"key": "...", "verdict": "forget|merge|promote|keep", "reason": "<short free text>"}`

Verdict-specific extra fields:
- merge subordinate: add `"merged_into": "<canonical-key>"`
- merge canonical:   add `"canonical": true, "merged_body": "<full merged text>"`
- promote:           add `"promoted_to": "docs/<allow-listed>.rst", "body": "<RST-formatted body>"`

## Decision tree (aggressive mode, from round 2)

**forget** when any of:
(a) the memory says "fixed in commit X" / "landed" / "merged" with a hash and
    the body adds nothing a commit message wouldn't;
(b) the body's first ~60 chars appear verbatim in CLAUDE.md or any docs/*.rst
    (grep to check — or trust the `dup_of_docs` triage tag if `triage` was
    run with `--docs-dir` for this round);
(c) it is an iteration receipt / bead-closure scrap ("closed angr-XXXX",
    "iteration N done", status snapshots, one-off run results);
(d) a more descriptive memory in the SAME cluster supersedes it;
(e) it describes a file, flag, script, or workflow that no longer exists
    (verify with ls/grep before concluding, or trust `stale_file_ref`);
(f) its primary value is a specific file:line, function/struct name,
    register/opcode encoding, or other implementation-level detail that will
    drift as the code changes, AND that detail would serve its purpose
    better as a comment at that location than as a memory re-injected into
    every future session regardless of topic. `bd prime` has no relevance
    filtering — every kept memory's full body loads into every session — so
    a memory only earns that permanent cost if it states something a reader
    would still want in a session unrelated to the code it's about (a
    general invariant, workflow gotcha, or design rule), not a debugging
    narrative pinned to one line of one file. When forgetting for this
    reason, name the file/function where the detail belongs in `reason`
    (e.g. "relocate to native/angr/src/vex/ccall.rs near
    arm_sym_flag_n") — `apply` won't write the comment; a human does that
    by hand, same as `promote`.

**merge** when ≥2 keys in YOUR batch describe the same invariant/rule.
Pick the most descriptive as canonical; `merged_body` = canonical body plus
the distinct content from subordinates (do not lose unique detail).
HARD RULE: `merged_into` must reference a key inside YOUR OWN batch
(cross-batch merges cause `validate` to reject the subordinate row — its
target won't be a canonical row in that batch's own manifest).

**promote** when ALL of: ≥800 chars AND durable architecture/design
knowledge AND fits exactly one allow-listed `.rst` AND is not already
covered there. If uncertain about the target, downgrade to keep — DO NOT
guess paths. Every promote candidate across four historical rounds was
declined as already-covered; promotes should be rare. Note: `apply` never
auto-applies promote rows — a human still has to add the content to docs
by hand, then forget the key in a follow-up round.

Allow-listed promote targets: every existing file matched by
`docs/advanced-topics/*.rst` and `docs/extending-angr/*.rst` in the angr
repo.

**keep** is the residual. Bias toward keep when the memory states something
durable and generalizable — an invariant, a cross-cutting gotcha, a
workflow/process rule — that a reader would still want in a session
unrelated to the code it's about. `bd prime` injects every kept memory's
full body into every session unconditionally (no relevance filtering, no
recency weighting), so "adds operational detail beyond CLAUDE.md's brief
mention" is not by itself enough to keep: if that detail is really
criterion (f) territory (pinned to one file:line/commit, better placed as a
code comment), forget it even though it's detailed. Forget only if the
memory adds nothing beyond the docs/CLAUDE.md coverage, or belongs in code
per (f).

## Mandatory keep-list (verdict MUST be keep — never forget/merge these)

See `rounds/mandatory-keep.txt` (enforced by `validate`/`apply` — a
manifest that forgets or merges away one of these fails validation). They
may be merge CANONICALS — absorbing a subordinate is fine; being merged
away or forgotten is not.

## Method

1. Read `bodies.json` and `cites.json` completely.
2. For staleness checks you may READ anything in the angr repo (CLAUDE.md,
   docs/, git log, ls) and run read-only `bd show <bead-id>` / `bd list` to
   check bead status for keys that cite beads, if you have that repo
   checked out. Do NOT run any mutating command (no `bd remember`/`forget`/
   `update`, no writes outside your one `manifest.jsonl` file — and never
   edit `memories.jsonl` directly; that's `apply`'s job, after `validate`
   passes).
3. Judge every key. Verify claims of "duplicate of docs" by actually
   grepping the docs. When genuinely torn between forget and keep: keep.
4. Write the manifest. Spot-check it's valid JSONL with one row per
   `bodies.json` key before finishing — `angr-memories validate --round
   <round>` gives the authoritative check once every batch in the round is
   done.
5. Final message: report counts (forget/merge/promote/keep) and a one-line
   rationale for each PROMOTE (there should rarely be any).
