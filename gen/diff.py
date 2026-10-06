"""Summarise how the generated calendars changed against a git revision.

Usage (from the repository root, after regenerating):

    uv run --locked --project gen gen/diff.py            # compare with HEAD
    uv run --locked --project gen gen/diff.py --base main

Prints Markdown for a pull request: the upstream versions, then for each
exchange the dates that stopped or started being sessions and the sessions
whose hours changed. Dates near today are flagged, because they change live
behaviour rather than history.
"""

from __future__ import annotations

import argparse
import datetime as dt
import json
import subprocess
import sys
import tomllib
from pathlib import Path

sys.path.insert(0, str(Path(__file__).resolve().parent))
from generate import DATA_DIR, ROOT, TIME_FIELDS, fmt_dur, rebuild_schedule  # noqa: E402

LOCK = ROOT / "gen" / "uv.lock"
PACKAGES = ("pandas-market-calendars", "exchange-calendars")
LIVE_BEFORE = dt.timedelta(days=30)
LIVE_AFTER = dt.timedelta(days=366)
MAX_LISTED = 12


def git_show(base: str, path: Path) -> str | None:
    rel = path.relative_to(ROOT).as_posix()
    res = subprocess.run(["git", "show", f"{base}:{rel}"], cwd=ROOT, capture_output=True, text=True)
    return res.stdout if res.returncode == 0 else None


def locked_versions(text: str | None) -> dict[str, str]:
    if not text:
        return {}
    lock = tomllib.loads(text)
    return {p["name"]: p["version"] for p in lock.get("package", []) if p["name"] in PACKAGES}


def schedule(text: str | None) -> tuple[dict, dict[dt.date, dict]]:
    if not text:
        return {}, {}
    data = json.loads(text)
    return data, dict(rebuild_schedule(data))


def fmt_times(t: dict) -> str:
    return " ".join(f"{f}={fmt_dur(t[f]) if t[f] is not None else '-'}" for f in TIME_FIELDS if t[f] is not None)


def listed(days: list[dt.date], today: dt.date) -> str:
    def one(d: dt.date) -> str:
        live = today - LIVE_BEFORE <= d <= today + LIVE_AFTER
        return f"**{d}** ⚠️" if live else str(d)

    shown = ", ".join(one(d) for d in days[:MAX_LISTED])
    more = f" … and {len(days) - MAX_LISTED} more" if len(days) > MAX_LISTED else ""
    return shown + more


def main() -> int:
    ap = argparse.ArgumentParser(description=__doc__.splitlines()[0])
    ap.add_argument("--base", default="HEAD", help="git revision to compare with (default HEAD)")
    ap.add_argument("--today", type=dt.date.fromisoformat, default=dt.date.today())
    args = ap.parse_args()

    out: list[str] = []
    old_v, new_v = locked_versions(git_show(args.base, LOCK)), locked_versions(LOCK.read_text())
    out.append("### Upstream versions\n")
    out.append("| Package | Before | After |\n|---|---|---|")
    for name in PACKAGES:
        mark = "" if old_v.get(name) == new_v.get(name) else " ⬆️"
        out.append(f"| `{name}` | {old_v.get(name, '-')} | {new_v.get(name, '-')}{mark} |")

    codes = sorted({p.stem for p in DATA_DIR.glob("*.json")})
    changed: list[str] = []
    label_only: list[str] = []
    live_total = 0
    for code in codes:
        path = DATA_DIR / f"{code}.json"
        old_doc, old = schedule(git_show(args.base, path))
        new_doc, new = schedule(path.read_text())
        if old_doc == new_doc:
            continue
        lines = [f"#### {code.upper()}"]
        if not old_doc:
            lines.append(f"- new calendar, {new_doc['first']} to {new_doc['last']}")
        else:
            if (old_doc["first"], old_doc["last"]) != (new_doc["first"], new_doc["last"]):
                lines.append(f"- coverage {old_doc['first']}..{old_doc['last']} → {new_doc['first']}..{new_doc['last']}")
            overlap = lambda d: old_doc["first"] <= d.isoformat() <= old_doc["last"]  # noqa: E731
            closed = sorted(d for d in old if d not in new and overlap(d) and d.isoformat() <= new_doc["last"])
            opened = sorted(d for d in new if d not in old and overlap(d))
            hours = sorted(d for d in new if d in old and new[d] != old[d])
            if closed:
                lines.append(f"- **now closed** ({len(closed)}): {listed(closed, args.today)}")
            if opened:
                lines.append(f"- **now open** ({len(opened)}): {listed(opened, args.today)}")
            if hours:
                detail = "; ".join(f"{d}: {fmt_times(old[d])} → {fmt_times(new[d])}" for d in hours[:MAX_LISTED])
                more = f" … and {len(hours) - MAX_LISTED} more" if len(hours) > MAX_LISTED else ""
                lines.append(f"- **hours changed** ({len(hours)}): {detail}{more}")
            for d in closed + opened + hours:
                if args.today - LIVE_BEFORE <= d <= args.today + LIVE_AFTER:
                    live_total += 1
            if len(lines) == 1:
                label_only.append(code.upper())
                continue
        changed.append("\n".join(lines))

    out.append("")
    if not changed:
        out.append("No sessions changed.")
    else:
        out.append(f"### Calendar changes ({len(changed)} exchanges)\n")
        if live_total:
            out.append(f"⚠️ **{live_total} change(s) fall within 30 days before / a year after {args.today}** "
                       "and change live behaviour. Check them against the exchanges' notices.\n")
        out.extend(changed)
    if label_only:
        out.append(f"\nSource label or holiday names only, no session changes: {', '.join(label_only)}")
    print("\n".join(out))
    return 0


if __name__ == "__main__":
    sys.exit(main())
