#!/usr/bin/env python3
"""Apply a bd-memories prune manifest.

Reads a manifest JSONL (one decision per line) and executes the actions:

  - "forget"   -> bd forget <key>
  - "promote"  -> append body (best-effort RST conversion) to a docs file,
                  then bd forget <key>
  - "merge"    -> bd remember --key <canonical> <merged-body>, then
                  bd forget on each subordinate key
  - "keep"     -> no-op (logged for completeness)

Safety contract:

  * Dry-run by default. Use `--apply` to actually mutate bd.
  * Idempotent: an action whose `key` already appears in
    `.bd-prune/decisions.jsonl` with the same verdict is skipped on rerun.
  * Pre-apply validation:
      - reject if any merge `canonical` collides with a forget `key` in the
        manifest
      - reject if any forget `key` is listed as a target in
        `.bd-prune/cites-graph.json` for a memory we intend to keep/promote
  * Every applied action is appended to `.bd-prune/decisions.jsonl`.

Manifest row schema (JSON object per line):

  {
    "key":           "<memory key>",
    "verdict":       "forget" | "keep" | "promote" | "merge",
    "reason":        "<short reason>",
    "batch":         "B0|B1a|...",

    # promote-only:
    "promoted_to":   "docs/advanced-topics/<file>.rst",
    "body":          "<RST-converted body to append>",

    # merge-only:
    "merged_into":   "<canonical key>",       # on subordinates
    "canonical":     true,                     # on the canonical row
    "merged_body":   "<full merged body>",     # on canonical row
  }
"""

from __future__ import annotations

import argparse
import datetime as dt
import json
import re
import subprocess
import sys
from pathlib import Path
from typing import Any

DEFAULT_PRUNE_DIR = Path(".bd-prune")


def _run(cmd: list[str], *, check: bool = True) -> subprocess.CompletedProcess:
    return subprocess.run(cmd, check=check, capture_output=True, text=True,
                          errors="replace")


def _load_decisions(prune_dir: Path) -> set[tuple[str, str]]:
    decisions = prune_dir / "decisions.jsonl"
    if not decisions.exists():
        return set()
    seen: set[tuple[str, str]] = set()
    for line in decisions.read_text().splitlines():
        line = line.strip()
        if not line:
            continue
        rec = json.loads(line)
        seen.add((rec["key"], rec["verdict"]))
    return seen


def _append_decision(rec: dict[str, Any], prune_dir: Path) -> None:
    decisions = prune_dir / "decisions.jsonl"
    rec = {**rec, "applied_at": dt.datetime.now(dt.timezone.utc).isoformat()}
    with decisions.open("a") as f:
        f.write(json.dumps(rec, ensure_ascii=False) + "\n")


def _load_manifest(path: Path) -> list[dict[str, Any]]:
    rows: list[dict[str, Any]] = []
    for ln in path.read_text().splitlines():
        ln = ln.strip()
        if not ln or ln.startswith("#"):
            continue
        rows.append(json.loads(ln))
    return rows


def _load_cites_graph(prune_dir: Path) -> dict[str, list[str]]:
    cites_graph = prune_dir / "cites-graph.json"
    if not cites_graph.exists():
        return {}
    return json.loads(cites_graph.read_text())


def validate(manifest: list[dict[str, Any]],
             cites_graph: dict[str, list[str]],
             prune_dir: Path) -> list[str]:
    """Return a list of validation errors (empty = ok)."""
    errors: list[str] = []
    forget_keys = {r["key"] for r in manifest if r["verdict"] == "forget"}
    # subordinates of a merge are also gone after apply
    forget_keys |= {r["key"] for r in manifest
                    if r["verdict"] == "merge" and not r.get("canonical")}
    # citers whose bodies are getting rewritten (merge canonical) or removed
    rewritten_or_removed = forget_keys | {
        r["key"] for r in manifest
        if r["verdict"] == "merge" and r.get("canonical")
    }

    merge_canonical_keys = {r["key"] for r in manifest
                            if r["verdict"] == "merge" and r.get("canonical")}

    # collision: a canonical we are writing should not also be forgotten
    collisions = forget_keys & merge_canonical_keys
    if collisions:
        errors.append(
            f"merge canonical collides with forget rows: {sorted(collisions)}"
        )

    # cited-by guard: rebuild guard excluding citations from memories that
    # are themselves being forgotten/rewritten in this manifest (their
    # citations will be gone after apply) or that have already been
    # forgotten in a prior batch (no longer in `bd memories`).
    if cites_graph:
        # what memories are still live in bd?
        try:
            cp = subprocess.run(["bd", "memories", "--json"],
                                check=True, capture_output=True, text=True,
                                errors="replace")
            live = set(json.loads(cp.stdout).keys())
        except Exception:
            live = set()
        guard: set[str] = set()
        for citer, cited in cites_graph.items():
            if citer in rewritten_or_removed:
                continue
            if live and citer not in live:
                continue
            guard.update(cited)
        orphans = forget_keys & guard
        if orphans:
            errors.append(
                f"forget would orphan cited keys: {sorted(orphans)} "
                f"(see {prune_dir / 'cites-graph.json'})"
            )
    return errors


_HEADING_RE = re.compile(r"^(#{1,4})\s+(.+?)\s*$")
_FENCE_RE = re.compile(r"^```([a-zA-Z0-9_+-]*)$")


def md_to_rst(body: str) -> str:
    """Crude Markdown -> RST conversion for memory bodies.

    Memory bodies are usually informal prose with the occasional `code`
    span or fenced block. This handles:
      - ATX headings (#, ##, ###) -> RST underlines
      - Fenced code blocks ``` -> RST literal block (.. code-block::)
      - Bullet lists pass through unchanged
      - Backtick spans stay as-is (RST accepts them)
    Human review in the docs commit is required for anything fancier.
    """
    out_lines: list[str] = []
    in_fence = False
    fence_lang = ""
    for raw in body.splitlines():
        if (m := _FENCE_RE.match(raw)):
            if not in_fence:
                fence_lang = m.group(1) or "text"
                out_lines.append("")
                out_lines.append(f".. code-block:: {fence_lang}")
                out_lines.append("")
                in_fence = True
            else:
                out_lines.append("")
                in_fence = False
            continue
        if in_fence:
            out_lines.append(f"    {raw}")
            continue
        if (m := _HEADING_RE.match(raw)):
            level = len(m.group(1))
            title = m.group(2)
            char = {1: "=", 2: "-", 3: "~", 4: "."}.get(level, "-")
            out_lines.append("")
            out_lines.append(title)
            out_lines.append(char * max(len(title), 3))
            out_lines.append("")
            continue
        out_lines.append(raw)
    return "\n".join(out_lines).rstrip() + "\n"


def do_forget(row: dict[str, Any], apply: bool, prune_dir: Path) -> None:
    key = row["key"]
    if apply:
        cp = _run(["bd", "forget", key], check=False)
        if cp.returncode != 0:
            print(f"  WARN: bd forget {key} exit={cp.returncode}: "
                  f"{cp.stderr.strip()}", file=sys.stderr)
        _append_decision(row, prune_dir)
    else:
        print(f"  [dry] bd forget {key}")


def do_promote(row: dict[str, Any], apply: bool, prune_dir: Path) -> None:
    key = row["key"]
    target = Path(row["promoted_to"])
    body = row.get("body") or ""
    rst = md_to_rst(body)
    today = dt.date.today().isoformat()
    footer = (
        f"\n.. note::\n\n   Originally captured {today} as bd memory ``{key}``.\n"
    )
    chunk = "\n" + rst + footer

    # bloat guard
    if target.exists():
        line_count = sum(1 for _ in target.open())
        if line_count > 2000 and not row.get("override_bloat_guard"):
            print(f"  SKIP promote -> {target}: file has {line_count} lines "
                  f"(>2000 bloat guard). Set override_bloat_guard=true to force.",
                  file=sys.stderr)
            return

    # dup-check: distinctive phrase
    if target.exists() and body.strip():
        phrase = body.strip().split("\n")[0][:60]
        if phrase and phrase in target.read_text():
            print(f"  SKIP promote -> {target}: target already contains "
                  f"distinctive phrase {phrase!r}; verdict downgraded to forget.",
                  file=sys.stderr)
            if apply:
                do_forget({**row, "verdict": "forget",
                           "reason": "dup-of-docs (auto-detected during apply)"},
                          apply=apply, prune_dir=prune_dir)
            return

    if apply:
        with target.open("a") as f:
            f.write(chunk)
        cp = _run(["bd", "forget", key], check=False)
        if cp.returncode != 0:
            print(f"  WARN: bd forget {key} exit={cp.returncode}: "
                  f"{cp.stderr.strip()}", file=sys.stderr)
        _append_decision(row, prune_dir)
    else:
        print(f"  [dry] append {len(chunk)} chars to {target}, then bd forget {key}")


def do_merge_canonical(row: dict[str, Any], apply: bool,
                       prune_dir: Path) -> None:
    key = row["key"]
    body = row["merged_body"]
    if apply:
        cp = _run(["bd", "remember", body, "--key", key], check=False)
        if cp.returncode != 0:
            print(f"  ERROR: bd remember --key {key} exit={cp.returncode}: "
                  f"{cp.stderr.strip()}", file=sys.stderr)
            return
        _append_decision(row, prune_dir)
    else:
        print(f"  [dry] bd remember --key {key} <{len(body)}-char body>")


def do_merge_subordinate(row: dict[str, Any], apply: bool,
                         prune_dir: Path) -> None:
    # subordinates of a merge are forgotten — same semantics as forget
    do_forget({**row, "verdict": "merge"}, apply=apply, prune_dir=prune_dir)


def do_keep(row: dict[str, Any], apply: bool, prune_dir: Path) -> None:
    if apply:
        _append_decision(row, prune_dir)
    else:
        print(f"  [dry] keep {row['key']} ({row.get('reason', '')})")


def apply_manifest(path: Path, apply: bool, prune_dir: Path) -> int:
    manifest = _load_manifest(path)
    print(f"Manifest: {path} ({len(manifest)} rows)")

    cites_graph = _load_cites_graph(prune_dir)
    errors = validate(manifest, cites_graph, prune_dir)
    if errors:
        print("VALIDATION ERRORS:", file=sys.stderr)
        for e in errors:
            print(f"  - {e}", file=sys.stderr)
        return 2

    seen = _load_decisions(prune_dir)
    counts = {"forget": 0, "promote": 0, "merge": 0, "keep": 0, "skip": 0}

    for row in manifest:
        key = row["key"]
        verdict = row["verdict"]
        if (key, verdict) in seen:
            counts["skip"] += 1
            continue
        if verdict == "forget":
            do_forget(row, apply, prune_dir)
            counts["forget"] += 1
        elif verdict == "promote":
            do_promote(row, apply, prune_dir)
            counts["promote"] += 1
        elif verdict == "merge":
            if row.get("canonical"):
                do_merge_canonical(row, apply, prune_dir)
            else:
                do_merge_subordinate(row, apply, prune_dir)
            counts["merge"] += 1
        elif verdict == "keep":
            do_keep(row, apply, prune_dir)
            counts["keep"] += 1
        else:
            print(f"  WARN: unknown verdict {verdict!r} for {key}",
                  file=sys.stderr)

    mode = "applied" if apply else "dry-run"
    print(f"\n{mode}: forget={counts['forget']} promote={counts['promote']} "
          f"merge={counts['merge']} keep={counts['keep']} "
          f"skipped(already-applied)={counts['skip']}")
    return 0


def main() -> int:
    ap = argparse.ArgumentParser(description=__doc__.splitlines()[0])
    ap.add_argument("manifest", type=Path, help="path to manifest JSONL")
    ap.add_argument("--apply", action="store_true",
                    help="actually mutate bd (default: dry-run)")
    ap.add_argument("--prune-dir", type=Path, default=DEFAULT_PRUNE_DIR,
                    help="directory holding cites-graph.json + decisions.jsonl "
                         "(default: .bd-prune)")
    args = ap.parse_args()
    if not args.manifest.exists():
        print(f"manifest not found: {args.manifest}", file=sys.stderr)
        return 1
    if not args.prune_dir.exists():
        args.prune_dir.mkdir(parents=True)
    return apply_manifest(args.manifest, args.apply, args.prune_dir)


if __name__ == "__main__":
    raise SystemExit(main())
