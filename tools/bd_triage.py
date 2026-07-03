#!/usr/bin/env python3
"""Phase 1 triage: bucket every bd memory by heuristic tags.

Reads `.bd-prune/snapshot-pre-prune.json` (full memory dump) and emits:

  .bd-prune/triage.csv         — key, length, tags, prefix, cluster_size
  .bd-prune/cites-graph.json   — {citer: [cited_keys, ...]}

Tags (all boolean unless noted):

  closed-only       all `angr-<id>` refs resolve to closed beads
  open-ref          at least one bead ref is open/in_progress/blocked
  no-bead           no `angr-<id>` reference
  status-shape      body matches landed|closed|merged|reverted|... or starts
                    with "Iter <n>" / "<YYYY-MM-DD> ..."
  docs-shape        >=800 chars with arch/design language or RST/MD headings
  future-date       contains 2026-08-* or later date
  stale-date        most recent date in body < 2026-05-01 AND status-shape
  cites-other       body contains the literal key of another bd memory
  dup-of-claude-md  >=40-char fragment from body present in CLAUDE.md
  dup-of-docs       >=40-char fragment from body present in any docs/**/*.rst
  reference-type    key prefix matches bd|ralph|dolt|claude-code|git|gh|ci|github
  user-preference   body contains "user prefers" / "do not" / "always" w/o commit
  stale-file-ref    cites a path that git ls-files cannot resolve
"""

from __future__ import annotations

import argparse
import csv
import json
import re
import subprocess
from pathlib import Path
from typing import Any

ROOT = Path(".")

BEAD_RE = re.compile(r"\bangr-([a-z0-9]+)(?:\.\d+)?\b")
DATE_RE = re.compile(r"\b(20\d\d)-(\d\d)-(\d\d)\b")
STATUS_RE = re.compile(
    r"\b(landed|closed|merged|reverted|superseded|fixed in commit|completed|resolved)\b",
    re.I,
)
ITER_START_RE = re.compile(r"^\s*(iter\s+\d|20\d\d-\d\d-\d\d)\b", re.I)
DOCS_LANG_RE = re.compile(
    r"\b(architecture|design|how to apply|invariant|rule|why:|how:|##\s|```)",
    re.I,
)
USER_PREF_RE = re.compile(r"\b(user prefers|do not|don.t |never |always )\b", re.I)
REF_TYPE_PREFIXES = ("bd-", "ralph-", "dolt-", "claude-code-",
                     "git-", "gh-", "ci-", "github-")
FILE_PATH_RE = re.compile(
    r"(?<![\w-])(?:angr|native|tests|docs|tools)/[A-Za-z0-9_./-]+\.[A-Za-z]+",
)


def _load_bead_status() -> dict[str, str]:
    """Return {bead_id: status} for all beads."""
    out: dict[str, str] = {}
    for status in ("open", "in_progress", "blocked", "deferred", "closed"):
        try:
            cp = subprocess.run(
                ["bd", "list", f"--status={status}", "--limit=0", "--json"],
                check=True, capture_output=True, text=True,
            )
        except subprocess.CalledProcessError:
            continue
        for item in json.loads(cp.stdout or "[]"):
            out[item["id"]] = status
    return out


def _load_repo_files() -> set[str]:
    cp = subprocess.run(["git", "ls-files"], check=True,
                        capture_output=True, text=True)
    return set(cp.stdout.splitlines())


def _load_claude_md() -> str:
    return Path("CLAUDE.md").read_text()


def _load_all_docs_text() -> str:
    parts: list[str] = []
    for p in Path("docs").rglob("*.rst"):
        parts.append(p.read_text())
    return "\n".join(parts)


def _fragments(body: str, n: int = 40, max_samples: int = 12) -> list[str]:
    """Sample fragments from body for dup detection."""
    body = body.strip()
    if len(body) < n:
        return [body] if body else []
    step = max(len(body) // max_samples, n)
    frags = []
    for i in range(0, len(body) - n, step):
        frag = body[i:i + n].strip()
        if len(frag) >= n and not frag.startswith("```"):
            frags.append(frag)
        if len(frags) >= max_samples:
            break
    return frags


def main() -> int:
    ap = argparse.ArgumentParser(description=__doc__.splitlines()[0])
    ap.add_argument("--prune-dir", type=Path, default=ROOT / ".bd-prune",
                    help="directory containing snapshot/triage/cites artifacts "
                         "(default: .bd-prune)")
    args = ap.parse_args()
    prune_dir: Path = args.prune_dir
    snapshot = prune_dir / "snapshot-pre-prune.json"
    triage_csv = prune_dir / "triage.csv"
    cites_json = prune_dir / "cites-graph.json"
    if not snapshot.exists():
        raise SystemExit(f"missing {snapshot}")
    mems: dict[str, str] = json.loads(snapshot.read_text())
    bead_status = _load_bead_status()
    repo_files = _load_repo_files()
    claude_md = _load_claude_md()
    docs_text = _load_all_docs_text()

    # cluster size by bead-id prefix and free-form prefix
    prefix_counts: dict[str, int] = {}
    for k in mems:
        # extract first hyphen-separated token as the "prefix"
        pfx = k.split("-")[0]
        prefix_counts[pfx] = prefix_counts.get(pfx, 0) + 1

    keys = set(mems.keys())
    cites_graph: dict[str, list[str]] = {}

    rows: list[dict[str, Any]] = []
    for key, body in mems.items():
        bead_refs = set(BEAD_RE.findall(body))
        bead_refs_full = {f"angr-{r}" for r in bead_refs}
        ref_statuses = {bead_status.get(b, "missing") for b in bead_refs_full}

        closed_only = bool(bead_refs_full) and ref_statuses <= {"closed", "missing"}
        open_ref = bool(ref_statuses & {"open", "in_progress", "blocked"})
        no_bead = not bead_refs_full

        status_shape = bool(STATUS_RE.search(body) or ITER_START_RE.match(body))

        docs_shape = len(body) >= 800 and bool(DOCS_LANG_RE.search(body))

        dates = DATE_RE.findall(body)
        future_date = any(int(y) == 2026 and int(m) >= 8 for (y, m, _) in dates) \
                      or any(int(y) > 2026 for (y, _, _) in dates)
        most_recent = max(("-".join(d) for d in dates), default="")
        stale_date = bool(most_recent and most_recent < "2026-05-01" and status_shape)

        # cites-other-memory: look for backticked keys
        cited = []
        for m in re.findall(r"`([a-z0-9][a-z0-9_-]{6,})`", body):
            if m in keys and m != key:
                cited.append(m)
        # also raw mentions of well-known keys
        for cand in keys:
            if len(cand) >= 12 and cand != key and cand in body:
                if cand not in cited:
                    cited.append(cand)
        if cited:
            cites_graph[key] = sorted(set(cited))

        cites_other = bool(cited)

        # dup-of-claude-md / dup-of-docs
        frags = _fragments(body)
        dup_claude = any(f in claude_md for f in frags) if frags else False
        dup_docs = any(f in docs_text for f in frags) if frags else False

        reference_type = key.startswith(REF_TYPE_PREFIXES)
        user_preference = bool(USER_PREF_RE.search(body))

        stale_file_ref = False
        for path in FILE_PATH_RE.findall(body):
            if path not in repo_files:
                # try a basename match before declaring stale (file may have moved)
                basename = path.rsplit("/", 1)[-1]
                if not any(rf.endswith("/" + basename) or rf == basename
                           for rf in repo_files):
                    stale_file_ref = True
                    break

        pfx = key.split("-")[0]
        rows.append({
            "key": key,
            "length": len(body),
            "prefix": pfx,
            "cluster_size": prefix_counts[pfx],
            "closed_only": int(closed_only),
            "open_ref": int(open_ref),
            "no_bead": int(no_bead),
            "status_shape": int(status_shape),
            "docs_shape": int(docs_shape),
            "future_date": int(future_date),
            "stale_date": int(stale_date),
            "cites_other": int(cites_other),
            "dup_of_claude_md": int(dup_claude),
            "dup_of_docs": int(dup_docs),
            "reference_type": int(reference_type),
            "user_preference": int(user_preference),
            "stale_file_ref": int(stale_file_ref),
            "most_recent_date": most_recent,
        })

    prune_dir.mkdir(exist_ok=True)
    with triage_csv.open("w") as f:
        w = csv.DictWriter(f, fieldnames=list(rows[0].keys()))
        w.writeheader()
        w.writerows(rows)

    cites_json.write_text(json.dumps(cites_graph, indent=2, sort_keys=True))

    # summary
    def cnt(field):
        return sum(r[field] for r in rows)

    print(f"Wrote {triage_csv} ({len(rows)} rows)")
    print(f"Wrote {cites_json} ({len(cites_graph)} citing memories, "
          f"{sum(len(v) for v in cites_graph.values())} citations)")
    print()
    print("Tag totals:")
    for field in ("closed_only", "open_ref", "no_bead", "status_shape",
                  "docs_shape", "future_date", "stale_date", "cites_other",
                  "dup_of_claude_md", "dup_of_docs", "reference_type",
                  "user_preference", "stale_file_ref"):
        print(f"  {field:20s} {cnt(field):5d}")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
