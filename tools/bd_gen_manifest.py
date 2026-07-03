#!/usr/bin/env python3
"""Generate a prune manifest for the closed-only + status-shape sweep.

Filters the live bd memories (not the pre-prune snapshot) against the
triage tags, emits a JSONL manifest of forget rows. Skips:

  - keys in the mandatory keep-list (CLI: --keep-list a,b,c)
  - keys cited by any memory we're NOT planning to forget in this manifest
    (the cited-by guard is double-checked by bd_prune.py)
  - keys already forgotten (not present in `bd memories --json`)

Usage:
    python3 tools/bd_gen_manifest.py B1a --status-shape --closed-only \
        --prefix v5a5 --out .bd-prune/manifest-B1a.jsonl
"""

from __future__ import annotations

import argparse
import csv
import json
import re
import subprocess
import sys
from pathlib import Path

DEFAULT_KEEP_LIST = {
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


def _live_memories() -> set[str]:
    cp = subprocess.run(["bd", "memories", "--json"],
                        check=True, capture_output=True, text=True)
    return set(json.loads(cp.stdout).keys())


def main() -> int:
    ap = argparse.ArgumentParser(description=__doc__.splitlines()[0])
    ap.add_argument("batch", help="batch label, e.g. B1a")
    ap.add_argument("--prune-dir", type=Path, default=Path(".bd-prune"),
                    help="directory containing triage.csv and cites-graph.json "
                         "(default: .bd-prune)")
    ap.add_argument("--closed-only", action="store_true")
    ap.add_argument("--no-bead", action="store_true")
    ap.add_argument("--status-shape", action="store_true")
    ap.add_argument("--docs-shape", action="store_true")
    ap.add_argument("--reference-type", action="store_true")
    ap.add_argument("--stale-file-ref", action="store_true")
    ap.add_argument("--future-date", action="store_true")
    ap.add_argument("--prefix", default=None,
                    help="restrict to keys with this hyphen-prefix (e.g. v5a5)")
    ap.add_argument("--prefix-in", default=None,
                    help="restrict to keys whose first '-' token is in this comma list")
    ap.add_argument("--min-cluster", type=int, default=None,
                    help="restrict to keys with cluster_size >= N")
    ap.add_argument("--exclude-cluster", default=None,
                    help="comma list of prefixes to exclude")
    ap.add_argument("--max-cluster", type=int, default=None,
                    help="restrict to keys with cluster_size <= N")
    ap.add_argument("--keep-list", default=",".join(sorted(DEFAULT_KEEP_LIST)))
    ap.add_argument("--out", type=Path, required=True)
    args = ap.parse_args()

    triage_csv = args.prune_dir / "triage.csv"
    cites_json = args.prune_dir / "cites-graph.json"

    keep_set = set(args.keep_list.split(","))
    rows = list(csv.DictReader(triage_csv.open()))
    live = _live_memories()
    cites_graph = json.loads(cites_json.read_text()) if cites_json.exists() else {}

    selected: list[dict[str, str]] = []
    for r in rows:
        key = r["key"]
        if key not in live:
            continue
        if key in keep_set:
            continue
        if args.closed_only and r["closed_only"] != "1":
            continue
        if args.no_bead and r["no_bead"] != "1":
            continue
        if args.status_shape and r["status_shape"] != "1":
            continue
        if args.docs_shape and r["docs_shape"] != "1":
            continue
        if args.reference_type and r["reference_type"] != "1":
            continue
        if args.stale_file_ref and r["stale_file_ref"] != "1":
            continue
        if args.future_date and r["future_date"] != "1":
            continue
        if args.prefix and r["prefix"] != args.prefix:
            continue
        if args.prefix_in:
            if r["prefix"] not in args.prefix_in.split(","):
                continue
        if args.exclude_cluster:
            if r["prefix"] in args.exclude_cluster.split(","):
                continue
        if args.min_cluster is not None and int(r["cluster_size"]) < args.min_cluster:
            continue
        if args.max_cluster is not None and int(r["cluster_size"]) > args.max_cluster:
            continue
        selected.append(r)

    # Cited-by guard: forgetting key X is unsafe if any LIVE memory we're
    # NOT forgetting cites X. Iterate to fixed point — when a deferral
    # turns a "soon-to-be-forgotten" citer into a "surviving" citer, its
    # cite targets need a fresh check.
    to_forget = {r["key"] for r in selected}
    all_blocked: set[str] = set()
    while True:
        surviving_citers = {
            citer: cited for citer, cited in cites_graph.items()
            if citer in live and citer not in to_forget
        }
        orphan_targets: set[str] = set()
        for cited in surviving_citers.values():
            orphan_targets.update(cited)
        new_blocked = to_forget & orphan_targets
        if not new_blocked:
            break
        all_blocked |= new_blocked
        to_forget -= new_blocked

    if all_blocked:
        print(f"Note: {len(all_blocked)} keys deferred because surviving "
              f"memories cite them. They'll surface in later batches.",
              file=sys.stderr)
        # Rebuild surviving_citers one last time for the diagnostic
        surviving_citers = {
            citer: cited for citer, cited in cites_graph.items()
            if citer in live and citer not in to_forget
        }
        for k in sorted(all_blocked)[:10]:
            citers = [c for c, cited in surviving_citers.items() if k in cited]
            print(f"  - {k}  (cited by: {', '.join(citers[:3])})",
                  file=sys.stderr)
        selected = [r for r in selected if r["key"] not in all_blocked]
    blocked = all_blocked  # for downstream reporting parity

    # Build a reason string from whichever tag filters were active so the
    # manifest is self-describing for any sweep (not just status-shape).
    active_filters = [name for name, flag in (
        ("closed-only", args.closed_only),
        ("no-bead", args.no_bead),
        ("status-shape", args.status_shape),
        ("docs-shape", args.docs_shape),
        ("reference-type", args.reference_type),
        ("stale-file-ref", args.stale_file_ref),
        ("future-date", args.future_date),
    ) if flag]
    reason_str = (" AND ".join(active_filters)
                  if active_filters else "automated sweep")

    args.out.parent.mkdir(parents=True, exist_ok=True)
    with args.out.open("w") as f:
        for r in selected:
            row = {
                "key": r["key"],
                "verdict": "forget",
                "reason": reason_str,
                "batch": args.batch,
            }
            f.write(json.dumps(row, ensure_ascii=False) + "\n")
    print(f"Wrote {args.out} ({len(selected)} forget rows; "
          f"deferred {len(blocked)} cited-by-living)")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
