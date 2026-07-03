#!/usr/bin/env python3
"""Apply an -auto manifest, auto-deferring rows that would orphan citations.

Wraps `bd_prune.py --apply`. If the validate step reports orphan errors,
parse out the orphan keys, move them to `<stem>-deferred.jsonl`, and
retry. Loops until validation passes or no more rows.

Usage:
    python3 tools/bd_apply_with_defer.py <manifest> --prune-dir <dir>
"""

from __future__ import annotations

import argparse
import json
import re
import subprocess
import sys
from pathlib import Path

ORPHAN_RE = re.compile(r"forget would orphan cited keys:\s*\[(.+?)\]")


def _run_apply(manifest: Path, prune_dir: Path, apply: bool) -> tuple[int, str]:
    cmd = ["python3", "tools/bd_prune.py", "--prune-dir", str(prune_dir),
           str(manifest)]
    if apply:
        cmd.append("--apply")
    cp = subprocess.run(cmd, capture_output=True, text=True)
    return cp.returncode, cp.stdout + cp.stderr


def _extract_orphans(output: str) -> set[str]:
    keys: set[str] = set()
    for m in ORPHAN_RE.finditer(output):
        for tok in m.group(1).split(","):
            tok = tok.strip().strip("'").strip('"')
            if tok:
                keys.add(tok)
    return keys


def main() -> int:
    ap = argparse.ArgumentParser(description=__doc__.splitlines()[0])
    ap.add_argument("manifest", type=Path)
    ap.add_argument("--prune-dir", type=Path, required=True)
    ap.add_argument("--max-iters", type=int, default=10)
    args = ap.parse_args()

    manifest: Path = args.manifest
    if not manifest.exists():
        print(f"manifest not found: {manifest}", file=sys.stderr)
        return 1

    stem = manifest.with_suffix("")
    deferred_path = stem.with_name(stem.name + "-deferred.jsonl")
    all_deferred: list[dict] = []

    for it in range(args.max_iters):
        rc, out = _run_apply(manifest, args.prune_dir, apply=False)
        if rc == 0:
            # validation passed, do the real apply
            rc2, out2 = _run_apply(manifest, args.prune_dir, apply=True)
            print(out2)
            break
        # validation failed — try to extract orphans
        orphans = _extract_orphans(out)
        if not orphans:
            # unrecoverable
            print(out, file=sys.stderr)
            return 2
        # split manifest
        keep_rows: list[dict] = []
        defer_rows: list[dict] = []
        for ln in manifest.read_text().splitlines():
            ln = ln.strip()
            if not ln:
                continue
            r = json.loads(ln)
            if r["key"] in orphans:
                defer_rows.append(r)
            else:
                keep_rows.append(r)
        manifest.write_text("".join(json.dumps(r, ensure_ascii=False) + "\n"
                                    for r in keep_rows))
        all_deferred.extend(defer_rows)
        print(f"[iter {it}] deferred {len(defer_rows)} rows: "
              f"{sorted(orphans)}", file=sys.stderr)
    else:
        print(f"hit max-iters={args.max_iters}; bailing", file=sys.stderr)
        return 3

    if all_deferred:
        # append, don't overwrite — there may already be a -deferred file
        with deferred_path.open("a") as f:
            for r in all_deferred:
                f.write(json.dumps(r, ensure_ascii=False) + "\n")
        print(f"deferred -> {deferred_path} ({len(all_deferred)} rows total)")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
