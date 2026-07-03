#!/usr/bin/env python3
"""Chunk bd memories into agent-review batches.

Reads the snapshot at `<prune-dir>/snapshot-pre-prune.json` and writes
one `bodies-R2-<batch>.json` bundle per chunk under `<prune-dir>/`.
Each bundle is `{key: body}` JSON restricted to the chunk's keys.

Chunking strategy: bucket by prefix family, then alpha-split inside
each family if it exceeds `--chunk-cap`. Families:

  invariant     prefix == "invariant"
  avoid         prefix == "avoid"
  z3            prefix == "z3"
  rust          prefix == "rust"
  benchmark     prefix in {"benchmark", "bench"}
  ops           prefix in {"bd","ralph","dolt","git","gh","ci","github","claude"}
  v5a5          prefix == "v5a5"
  inspect       prefix == "inspect"
  syscall       prefix == "syscall"
  catch         everything else

Also writes:
  - `<prune-dir>/batch-index.json` — {batch_id: [keys]}
  - per-batch cites subset alongside the bodies bundle
    (`cites-R2-<batch>.json`)
"""

from __future__ import annotations

import argparse
import json
from pathlib import Path

FAMILIES_FIXED = {
    "invariant": {"invariant"},
    "avoid": {"avoid"},
    "z3": {"z3"},
    "rust": {"rust"},
    "benchmark": {"benchmark", "bench"},
    "ops": {"bd", "ralph", "dolt", "git", "gh", "ci", "github", "claude"},
    "v5a5": {"v5a5"},
    "inspect": {"inspect"},
    "syscall": {"syscall"},
}


def _family_of(key: str) -> str:
    pfx = key.split("-")[0]
    for fam, pxs in FAMILIES_FIXED.items():
        if pfx in pxs:
            return fam
    return "catch"


def _alpha_split(keys: list[str], cap: int) -> list[list[str]]:
    """Split keys (sorted) into alpha-ordered chunks at most `cap` long."""
    keys = sorted(keys)
    if len(keys) <= cap:
        return [keys]
    n_chunks = (len(keys) + cap - 1) // cap
    base = len(keys) // n_chunks
    rem = len(keys) % n_chunks
    chunks: list[list[str]] = []
    idx = 0
    for i in range(n_chunks):
        size = base + (1 if i < rem else 0)
        chunks.append(keys[idx:idx + size])
        idx += size
    return chunks


def main() -> int:
    ap = argparse.ArgumentParser(description=__doc__.splitlines()[0])
    ap.add_argument("--prune-dir", type=Path, required=True)
    ap.add_argument("--chunk-cap", type=int, default=70,
                    help="max keys per chunk (default: 70)")
    args = ap.parse_args()

    snap_path = args.prune_dir / "snapshot-pre-prune.json"
    cites_path = args.prune_dir / "cites-graph.json"
    if not snap_path.exists():
        raise SystemExit(f"missing {snap_path}")
    snap: dict[str, str] = json.loads(snap_path.read_text())
    cites_graph: dict[str, list[str]] = (
        json.loads(cites_path.read_text()) if cites_path.exists() else {}
    )
    # Reverse index: who cites X?
    cited_by: dict[str, list[str]] = {}
    for citer, targets in cites_graph.items():
        for t in targets:
            cited_by.setdefault(t, []).append(citer)

    # Bucket all keys by family
    families: dict[str, list[str]] = {fam: [] for fam in FAMILIES_FIXED}
    families["catch"] = []
    for k in snap:
        families[_family_of(k)].append(k)

    # Split each family into chunks
    batch_index: dict[str, list[str]] = {}
    for fam, keys in families.items():
        if not keys:
            continue
        chunks = _alpha_split(keys, args.chunk_cap)
        if len(chunks) == 1:
            batch_index[f"R2-{fam.upper()}"] = chunks[0]
        else:
            for i, chunk in enumerate(chunks, start=1):
                batch_index[f"R2-{fam.upper()}-{i}"] = chunk

    # Write per-batch bodies + cites subset
    for batch_id, keys in batch_index.items():
        bodies = {k: snap[k] for k in keys}
        (args.prune_dir / f"bodies-{batch_id}.json").write_text(
            json.dumps(bodies, ensure_ascii=False, indent=2, sort_keys=True)
        )
        # Cites subset: any citer or citee touching the chunk
        ck = set(keys)
        subset: dict[str, dict[str, list[str]]] = {"out": {}, "in": {}}
        for k in keys:
            if k in cites_graph:
                subset["out"][k] = cites_graph[k]
            if k in cited_by:
                subset["in"][k] = cited_by[k]
        (args.prune_dir / f"cites-{batch_id}.json").write_text(
            json.dumps(subset, ensure_ascii=False, indent=2, sort_keys=True)
        )

    (args.prune_dir / "batch-index.json").write_text(
        json.dumps(batch_index, ensure_ascii=False, indent=2, sort_keys=True)
    )

    print(f"Wrote {len(batch_index)} batches:")
    for batch_id, keys in batch_index.items():
        print(f"  {batch_id:20s} {len(keys):4d} keys")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
