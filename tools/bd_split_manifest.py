#!/usr/bin/env python3
"""Split an agent-emitted manifest into apply-now vs. needs-review halves.

Reads `<manifest>.jsonl` and writes:

  <stem>-auto.jsonl     -> verdict in {forget, merge, keep}
  <stem>-promote.jsonl  -> verdict == "promote"

Per the round-2 plan, the auto half applies without pause; promotes
require human review of the target docs file before apply.

Also validates promote rows up front: every `promoted_to` must exist on
disk, OR be in an explicit `--allow-list` (comma-separated paths). The
allow-list defaults to every `docs/advanced-topics/*.rst` and
`docs/extending-angr/*.rst`. Rows that fail validation are written to
`<stem>-promote-rejected.jsonl` and printed to stderr.
"""

from __future__ import annotations

import argparse
import json
import sys
from pathlib import Path


def _default_allow_list() -> set[str]:
    paths: set[str] = set()
    for d in ("docs/advanced-topics", "docs/extending-angr"):
        for p in Path(d).glob("*.rst"):
            paths.add(str(p))
    return paths


def main() -> int:
    ap = argparse.ArgumentParser(description=__doc__.splitlines()[0])
    ap.add_argument("manifest", type=Path, help="agent manifest JSONL")
    ap.add_argument("--allow-list", default=None,
                    help="comma-separated docs/*.rst paths; default: "
                         "docs/advanced-topics/*.rst + docs/extending-angr/*.rst")
    args = ap.parse_args()

    if not args.manifest.exists():
        print(f"manifest not found: {args.manifest}", file=sys.stderr)
        return 1

    if args.allow_list:
        allow = set(args.allow_list.split(","))
    else:
        allow = _default_allow_list()

    stem = args.manifest.with_suffix("")
    auto_path = stem.with_name(stem.name + "-auto.jsonl")
    promote_path = stem.with_name(stem.name + "-promote.jsonl")
    rejected_path = stem.with_name(stem.name + "-promote-rejected.jsonl")

    auto_rows: list[dict] = []
    promote_rows: list[dict] = []
    rejected_rows: list[dict] = []

    for ln in args.manifest.read_text().splitlines():
        ln = ln.strip()
        if not ln or ln.startswith("#"):
            continue
        r = json.loads(ln)
        verdict = r.get("verdict")
        if verdict == "promote":
            target = r.get("promoted_to", "")
            if target not in allow:
                r["_rejection"] = (
                    f"promoted_to {target!r} not in allow-list "
                    f"({len(allow)} valid paths)"
                )
                rejected_rows.append(r)
                continue
            if not Path(target).exists():
                r["_rejection"] = f"promoted_to {target!r} does not exist"
                rejected_rows.append(r)
                continue
            promote_rows.append(r)
        elif verdict in {"forget", "merge", "keep"}:
            auto_rows.append(r)
        else:
            r["_rejection"] = f"unknown verdict {verdict!r}"
            rejected_rows.append(r)

    auto_path.write_text("".join(json.dumps(r, ensure_ascii=False) + "\n"
                                 for r in auto_rows))
    promote_path.write_text("".join(json.dumps(r, ensure_ascii=False) + "\n"
                                    for r in promote_rows))
    if rejected_rows:
        rejected_path.write_text("".join(json.dumps(r, ensure_ascii=False) + "\n"
                                         for r in rejected_rows))
        print(f"REJECTED {len(rejected_rows)} rows -> {rejected_path}",
              file=sys.stderr)
        for r in rejected_rows[:10]:
            print(f"  {r['key']}: {r['_rejection']}", file=sys.stderr)
    print(f"auto={len(auto_rows)} ({auto_path})")
    print(f"promote={len(promote_rows)} ({promote_path})")
    print(f"rejected={len(rejected_rows)}")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
