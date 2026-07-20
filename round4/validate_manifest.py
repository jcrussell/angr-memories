#!/usr/bin/env python3
"""Validate one round-4 batch manifest against its bodies file.

Usage: python3 validate_manifest.py <BATCH>   (e.g. R4-CATCH-1)
Exits non-zero with a message on any violation; prints verdict counts on success.
"""

from __future__ import annotations

import json
import sys
from collections import Counter
from pathlib import Path

D = Path(__file__).parent
batch = sys.argv[1]
bodies = json.loads((D / f"bodies-{batch}.json").read_text())
mpath = D / f"manifest-{batch}.jsonl"
rows = [json.loads(ln) for ln in mpath.read_text().splitlines() if ln.strip()]

keys = [r["key"] for r in rows]
dupes = [k for k, c in Counter(keys).items() if c > 1]
missing = set(bodies) - set(keys)
extra = set(keys) - set(bodies)
errs = []
if dupes:
    errs.append(f"duplicate keys: {sorted(dupes)}")
if missing:
    errs.append(f"missing keys: {sorted(missing)}")
if extra:
    errs.append(f"extra keys: {sorted(extra)}")

MANDATORY_KEEP = {
    "core-goal-design-philosophy",
    "rust-proxy-architecture-decision",
    "rust-engine-claude-md-pointer",
    "rust-python-boundary-audit",
    "constraint-export-no-pre-pin",
    "characterization-vs-fix-pattern",
    "benchmark-bimodal-variance-rules",
    "env-venv-corruption",
    "bd-update-notes-overwrites",
    "avoid-bd-remember-without-key-flag",
    "9maq-bisect-method",
    "venv-rebuild-cargo-direct-copy",
    "avoid-pip-install-deps",
}
for r in rows:
    v = r.get("verdict")
    if v not in ("forget", "merge", "promote", "keep"):
        errs.append(f"{r['key']}: bad verdict {v!r}")
    if r["key"] in MANDATORY_KEEP and v not in ("keep",) and not r.get("canonical"):
        errs.append(f"{r['key']}: mandatory-keep violated (verdict={v})")
    if v == "merge":
        if r.get("canonical"):
            if not r.get("merged_body"):
                errs.append(f"{r['key']}: canonical without merged_body")
        else:
            tgt = r.get("merged_into")
            if tgt not in bodies:
                errs.append(f"{r['key']}: merged_into {tgt!r} not in batch")
    if v == "promote" and not (r.get("promoted_to") and r.get("body")):
        errs.append(f"{r['key']}: promote missing promoted_to/body")

# merge targets must themselves be merge-canonical rows
canon = {r["key"] for r in rows if r.get("canonical")}
for r in rows:
    if r.get("verdict") == "merge" and not r.get("canonical"):
        if r.get("merged_into") not in canon:
            errs.append(f"{r['key']}: merged_into {r.get('merged_into')!r} is not a canonical row")

if errs:
    print(f"INVALID manifest-{batch}.jsonl:")
    for e in errs:
        print("  -", e)
    sys.exit(1)

print(f"OK manifest-{batch}.jsonl:", dict(Counter(r["verdict"] for r in rows)))
