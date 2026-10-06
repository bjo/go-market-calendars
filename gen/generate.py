"""Generate go-market-calendars' embedded exchange data from pandas_market_calendars.

Usage (from the repository root):

    uv run --locked --project gen gen/generate.py          # rewrite data/ and exchanges_gen.go
    uv run --locked --project gen gen/generate.py --check  # fail if the committed output drifted

For every configured exchange the script asks pandas_market_calendars for the
full schedule over the exchange's coverage window, applies overrides.toml, and
writes data/<code>.json in a compact form:

  * regular  - the regular trading hours, as periods that start on a date;
  * holidays - weekdays (per the weekmask) with no session, by year;
  * special  - sessions whose hours differ from the regular hours, or sessions
               held on a non-weekmask day ("extra");
  * sha256   - a digest of the full schedule, which the Go tests rebuild from
               the compact form and compare.

Output is deterministic: the same pinned dependencies give byte-identical files.
"""

from __future__ import annotations

import argparse
import datetime as dt
import hashlib
import importlib.metadata
import json
import sys
import tomllib
import warnings
from dataclasses import dataclass
from pathlib import Path

import exchange_calendars as xcals
import pandas as pd
import pandas_market_calendars as pmc

ROOT = Path(__file__).resolve().parent.parent
DATA_DIR = ROOT / "data"
GO_FILE = ROOT / "exchanges_gen.go"
OVERRIDES = ROOT / "overrides.toml"

FIRST_YEAR = 1970
LAST_YEAR = 2035

# A year with fewer weekday closures than this is treated as outside the
# source's recorded holiday data (pandas_market_calendars silently returns
# every weekday as a session for years it has no holiday list for).
MIN_CLOSURES_PER_YEAR = 3

TIME_FIELDS = ("open", "close", "break_start", "break_end")


@dataclass(frozen=True)
class Exchange:
    code: str  # registry key and Go function name (upper-cased)
    pmc: str  # pandas_market_calendars calendar name
    name: str  # human-readable exchange name
    region: str
    xcals: str | None = None  # exchange_calendars calendar whose bounds apply
    first: str | None = None  # first trading date, if after FIRST_YEAR
    first_source: str | None = None


EXCHANGES: tuple[Exchange, ...] = (
    # America
    Exchange("xnys", "NYSE", "New York Stock Exchange", "America"),
    Exchange(
        "xnas", "NASDAQ", "NASDAQ", "America",
        first="1971-02-08",
        first_source="https://ir.nasdaq.com/news-releases/news-release-details/nasdaq-celebrates-50-years-innovation",
    ),
    Exchange(
        "xcbo", "CBOE_Equity_Options", "Chicago Board Options Exchange", "America",
        first="1973-04-26",
        first_source="https://ir.cboe.com/news/news-details/2013/CBOE-Releases-40th-Anniversary-Video-04-19-2013/default.aspx",
    ),
    Exchange(
        "xcbf", "CFE", "Cboe Futures Exchange", "America",
        first="2004-03-26",
        first_source="https://ir.cboe.com/news/news-details/2024/Cboe-Global-Markets-Commemorates-20-Years-of-Cboe-Futures-Exchange-and-VIX-Futures-Trading/default.aspx",
    ),
    Exchange("xtse", "XTSE", "Toronto Stock Exchange", "America", xcals="XTSE"),
    Exchange("xmex", "XMEX", "Mexican Stock Exchange", "America", xcals="XMEX"),
    Exchange("bvmf", "BVMF", "B3 - Brasil Bolsa Balcão", "America", xcals="BVMF"),
    # Europe
    Exchange("xlon", "XLON", "London Stock Exchange", "Europe", xcals="XLON"),
    Exchange("xams", "XAMS", "Euronext Amsterdam", "Europe", xcals="XAMS"),
    Exchange("xbru", "XBRU", "Euronext Brussels", "Europe", xcals="XBRU"),
    Exchange("xlis", "XLIS", "Euronext Lisbon", "Europe", xcals="XLIS"),
    Exchange("xpar", "XPAR", "Euronext Paris", "Europe", xcals="XPAR"),
    Exchange("xmil", "XMIL", "Borsa Italiana", "Europe", xcals="XMIL"),
    Exchange("xmad", "XMAD", "Bolsa de Madrid", "Europe", xcals="XMAD"),
    Exchange("xfra", "XFRA", "Frankfurt Stock Exchange", "Europe", xcals="XFRA"),
    Exchange(
        "xetr", "XETR", "Deutsche Börse Xetra", "Europe", xcals="XETR",
        first="1997-11-28",
        first_source="https://www.deutsche-boerse.com/dbg-en/media/news-stories/news-stories-insights/insights-200-years-stock/Xetra-the-electronic-trading-platform-of-Deutsche-B-rse--2273700",
    ),
    Exchange("xswx", "XSWX", "SIX Swiss Exchange", "Europe", xcals="XSWX"),
    Exchange("xdub", "XDUB", "Euronext Dublin", "Europe", xcals="XDUB"),
    Exchange("xwbo", "XWBO", "Wiener Börse", "Europe", xcals="XWBO"),
    Exchange("xsto", "XSTO", "Nasdaq Stockholm", "Europe", xcals="XSTO"),
    Exchange("xcse", "XCSE", "Nasdaq Copenhagen", "Europe", xcals="XCSE"),
    Exchange("xhel", "XHEL", "Nasdaq Helsinki", "Europe", xcals="XHEL"),
    Exchange("xosl", "XOSL", "Oslo Børs", "Europe", xcals="XOSL"),
    # Africa
    Exchange("xjse", "XJSE", "Johannesburg Stock Exchange", "Africa", xcals="XJSE"),
    # Asia/Pacific
    Exchange("xbom", "XBOM", "BSE (Bombay Stock Exchange)", "Asia/Pacific", xcals="XBOM"),
    Exchange("xnse", "XNSE", "National Stock Exchange of India", "Asia/Pacific"),
    Exchange(
        "xbkk", "XBKK", "Stock Exchange of Thailand", "Asia/Pacific", xcals="XBKK",
        first="1975-04-30",
        first_source="https://www.set.or.th/en/about/overview/journey",
    ),
    Exchange("xses", "XSES", "Singapore Exchange", "Asia/Pacific", xcals="XSES"),
    Exchange("xhkg", "XHKG", "Hong Kong Stock Exchange", "Asia/Pacific", xcals="XHKG"),
    Exchange("xshg", "XSHG", "Shanghai Stock Exchange", "Asia/Pacific", xcals="XSHG"),
    # Shenzhen follows the mainland China exchange holiday schedule.
    Exchange("xshe", "XSHG", "Shenzhen Stock Exchange", "Asia/Pacific", xcals="XSHG"),
    Exchange("xkrx", "XKRX", "Korea Exchange", "Asia/Pacific", xcals="XKRX"),
    Exchange("xtai", "XTAI", "Taiwan Stock Exchange", "Asia/Pacific", xcals="XTAI"),
    Exchange("xjpx", "JPX", "Japan Exchange Group", "Asia/Pacific", xcals="XTKS"),
    Exchange("xtks", "XTKS", "Tokyo Stock Exchange", "Asia/Pacific", xcals="XTKS"),
    Exchange("xasx", "XASX", "Australian Securities Exchange", "Asia/Pacific", xcals="XASX"),
    Exchange("xnze", "XNZE", "New Zealand Exchange", "Asia/Pacific", xcals="XNZE"),
)


# --------------------------------------------------------------------------
# Formatting helpers


def fmt_dur(seconds: int) -> str:
    """Seconds since local midnight of the session date, as a Go duration."""
    if seconds == 0:
        return "0s"
    sign = "-" if seconds < 0 else ""
    s = abs(seconds)
    h, rem = divmod(s, 3600)
    m, sec = divmod(rem, 60)
    out = ""
    if h:
        out += f"{h}h"
    if m:
        out += f"{m}m"
    if sec:
        out += f"{sec}s"
    return sign + out


def local_offset(ts: pd.Timestamp | None, day: dt.date, tz: str) -> int | None:
    """Wall-clock seconds of ts relative to midnight of day, in tz."""
    if ts is None or pd.isna(ts):
        return None
    loc = ts.tz_convert(tz)
    days = (loc.date() - day).days
    return days * 86400 + loc.hour * 3600 + loc.minute * 60 + loc.second


def time_seconds(t: dt.time, days: int = 0) -> int:
    return days * 86400 + t.hour * 3600 + t.minute * 60 + t.second


# --------------------------------------------------------------------------
# Coverage window


def closures_by_year(sched: pd.DataFrame, weekmask: list[bool], start: dt.date, end: dt.date) -> dict[int, int]:
    sessions = {d.date() for d in sched.index}
    counts: dict[int, int] = {}
    d = start
    while d <= end:
        if weekmask[d.weekday()] and d not in sessions:
            counts[d.year] = counts.get(d.year, 0) + 1
        d += dt.timedelta(days=1)
    return counts


def coverage(ex: Exchange, cal, weekmask: list[bool]) -> tuple[dt.date, dt.date]:
    first = dt.date(FIRST_YEAR, 1, 1)
    last = dt.date(LAST_YEAR, 12, 31)
    if ex.first:
        first = max(first, dt.date.fromisoformat(ex.first))
    if ex.xcals:
        factory = xcals.calendar_utils._default_calendar_factories[ex.xcals]
        if factory.bound_min() is not None:
            first = max(first, factory.bound_min().date())
        if factory.bound_max() is not None:
            last = min(last, factory.bound_max().date())
    # Clip to the run of years that carry recorded holidays.
    sched = cal.schedule(first.isoformat(), last.isoformat())
    counts = closures_by_year(sched, weekmask, first, last)
    years = [y for y in range(first.year, last.year + 1) if counts.get(y, 0) >= MIN_CLOSURES_PER_YEAR]
    anchor = 2025
    if anchor not in years:
        raise SystemExit(f"{ex.code}: no recorded holidays in {anchor}")
    lo = hi = anchor
    while lo - 1 in years:
        lo -= 1
    while hi + 1 in years:
        hi += 1
    if lo > first.year:
        first = dt.date(lo, 1, 1)
    if hi < last.year:
        last = dt.date(hi, 12, 31)
    return first, last


# --------------------------------------------------------------------------
# Regular hours


# A run of this many consecutive sessions with identical hours starts a new
# regular-hours period; shorter runs are recorded as special sessions.
PERIOD_RUN = 10


def regular_periods(rows: list[tuple[dt.date, dict[str, int | None]]]) -> list[tuple[dt.date, dict[str, int | None]]]:
    """Regular hours as (start date, times) periods, derived from the schedule.

    The hours a source declares as regular do not always match the sessions it
    produces, so the periods are read off the schedule itself: the hours in
    force are replaced when PERIOD_RUN consecutive sessions agree on new hours.
    """
    key = lambda t: tuple(t[f] for f in TIME_FIELDS)  # noqa: E731
    head = [key(t) for _, t in rows[:2 * PERIOD_RUN]]
    current = max(sorted(set(head), key=str), key=head.count)
    periods = [(rows[0][0], dict(zip(TIME_FIELDS, current)))]
    for i, (day, times) in enumerate(rows):
        k = key(times)
        if k == current:
            continue
        run = rows[i:i + PERIOD_RUN]
        if len(run) == PERIOD_RUN and all(key(t) == k for _, t in run):
            current = k
            periods.append((day, dict(times)))
    return periods


def regular_at(periods, day: dt.date) -> dict[str, int | None]:
    times = periods[0][1]
    for date, t in periods:
        if date <= day:
            times = t
        else:
            break
    return times


# --------------------------------------------------------------------------
# Holiday names


def holiday_names(cal, first: dt.date, last: dt.date) -> dict[dt.date, str]:
    names: dict[dt.date, set[str]] = {}
    rules = getattr(cal.regular_holidays, "rules", []) if cal.regular_holidays is not None else []
    with warnings.catch_warnings():
        warnings.simplefilter("ignore")
        for rule in rules:
            try:
                dates = rule.dates(pd.Timestamp(first), pd.Timestamp(last))
            except Exception:  # noqa: BLE001 - a rule that cannot evaluate in range has no dates
                continue
            for d in dates:
                names.setdefault(pd.Timestamp(d).date(), set()).add(str(rule.name))
    return {d: " / ".join(sorted(n)) for d, n in names.items()}


# --------------------------------------------------------------------------
# Overrides


def load_overrides() -> dict[str, list[dict]]:
    if not OVERRIDES.exists():
        return {}
    doc = tomllib.loads(OVERRIDES.read_text())
    out: dict[str, list[dict]] = {}
    for i, o in enumerate(doc.get("override", [])):
        where = f"overrides.toml entry {i + 1}"
        for key in ("calendar", "date", "action", "reason", "source"):
            if not str(o.get(key, "")).strip():
                raise SystemExit(f"{where}: missing required field {key!r}")
        if o["action"] not in ("close", "open"):
            raise SystemExit(f"{where}: action must be 'close' or 'open'")
        if isinstance(o["date"], dt.date):  # an unquoted TOML date
            o["date"] = o["date"].isoformat()
        try:
            dt.date.fromisoformat(o["date"])
        except (TypeError, ValueError):
            raise SystemExit(f"{where}: date must be YYYY-MM-DD") from None
        for key in ("open", "close"):
            if key in o:
                try:
                    o[key] = parse_hhmm(o[key])
                except (AttributeError, ValueError):
                    raise SystemExit(f"{where}: {key} must be \"HH:MM\"") from None
        out.setdefault(o["calendar"], []).append(o)
    return out


def parse_hhmm(s: str) -> int:
    h, m = s.split(":")
    if len(h) != 2 or len(m) != 2 or not 0 <= int(h) < 24 or not 0 <= int(m) < 60:
        raise ValueError(s)
    return int(h) * 3600 + int(m) * 60


# --------------------------------------------------------------------------
# Build one exchange


def canonical_digest(rows: list[tuple[dt.date, dict[str, int | None]]]) -> str:
    """sha256 over one 'date open close break_start break_end' line per session.

    Times are wall-clock seconds from the session date's local midnight ("-"
    for none), so the digest does not depend on a time zone database. The Go
    tests rebuild it from the compact data; keep in step with scheduleDigest
    in data_test.go.
    """
    h = hashlib.sha256()
    for day, times in rows:
        parts = [day.isoformat()] + ["-" if times[f] is None else str(times[f]) for f in TIME_FIELDS]
        h.update((" ".join(parts) + "\n").encode())
    return h.hexdigest()


def build(ex: Exchange, overrides: list[dict]) -> dict:
    with warnings.catch_warnings():
        warnings.simplefilter("ignore")
        cal = pmc.get_calendar(ex.pmc)
    tz = str(cal.tz)
    weekmask = [day in cal.weekmask.split() for day in ("Mon", "Tue", "Wed", "Thu", "Fri", "Sat", "Sun")]
    first, last = coverage(ex, cal, weekmask)

    with warnings.catch_warnings():
        warnings.simplefilter("ignore")
        sched = cal.schedule(first.isoformat(), last.isoformat())

    sessions: dict[dt.date, dict[str, int | None]] = {}
    cols = {"market_open": "open", "market_close": "close", "break_start": "break_start", "break_end": "break_end"}
    for idx, row in sched.iterrows():
        day = idx.date()
        times = {f: None for f in TIME_FIELDS}
        for col, field in cols.items():
            if col in sched.columns:
                times[field] = local_offset(row[col], day, tz)
        sessions[day] = times

    periods = regular_periods(sorted(sessions.items()))
    periods[0] = (first, periods[0][1])

    # Apply overrides on top of the source schedule.
    for o in sorted(overrides, key=lambda o: o["date"]):
        day = dt.date.fromisoformat(o["date"])
        if not first <= day <= last:
            raise SystemExit(f"override {ex.code} {day}: outside coverage {first}..{last}")
        if o["action"] == "close":
            if day not in sessions:
                raise SystemExit(f"override {ex.code} {day}: source already closed; remove the override")
            del sessions[day]
        else:
            if day in sessions:
                raise SystemExit(f"override {ex.code} {day}: source already open; remove the override")
            times = dict(regular_at(periods, day))
            times["open"] = o.get("open", times["open"])
            times["close"] = o.get("close", times["close"])
            if times["open"] >= times["close"]:
                raise SystemExit(f"override {ex.code} {day}: open must be before close")
            # Hours that cut into the regular break drop it.
            if times["break_start"] is not None and (
                times["close"] <= times["break_end"] or times["open"] >= times["break_start"]
            ):
                times["break_start"] = times["break_end"] = None
            sessions[day] = times

    names = holiday_names(cal, first, last)
    override_reason = {dt.date.fromisoformat(o["date"]): o["reason"] for o in overrides if o["action"] == "close"}

    holidays: dict[int, list[str]] = {}
    special: dict[int, list[str]] = {}
    day = first
    while day <= last:
        if weekmask[day.weekday()] and day not in sessions:
            name = override_reason.get(day) or names.get(day, "")
            holidays.setdefault(day.year, []).append(f"{day:%m-%d} {name}".rstrip())
        if day in sessions:
            reg = regular_at(periods, day)
            diff = [f"{f}={fmt_dur(sessions[day][f]) if sessions[day][f] is not None else '-'}"
                    for f in TIME_FIELDS if sessions[day][f] != reg[f]]
            if not weekmask[day.weekday()]:
                diff.insert(0, "extra")
            if diff:
                special.setdefault(day.year, []).append(f"{day:%m-%d} " + " ".join(diff))
        day += dt.timedelta(days=1)

    # Typical early close per period: the most common shortened close.
    regular = []
    for i, (start, times) in enumerate(periods):
        end = periods[i + 1][0] if i + 1 < len(periods) else last + dt.timedelta(days=1)
        early: dict[int, int] = {}
        for d, t in sessions.items():
            if start <= d < end and times["close"] is not None and t["close"] is not None and t["close"] < times["close"]:
                early[t["close"]] = early.get(t["close"], 0) + 1
        entry = {"from": start.isoformat()}
        for f in TIME_FIELDS:
            if times[f] is not None:
                entry[f] = fmt_dur(times[f])
        if early:
            entry["early_close"] = fmt_dur(sorted(early.items(), key=lambda kv: (-kv[1], kv[0]))[0][0])
        regular.append(entry)

    rows = sorted(sessions.items())
    extended = {}
    for pmc_key, field in (("pre", "pre"), ("post", "post")):
        entries = cal.regular_market_times.get(pmc_key, ())
        if entries:
            _, t, *rest = entries[-1]
            extended[field] = fmt_dur(time_seconds(t, rest[0] if rest else 0))
    out = {
        "code": ex.code,
        "name": ex.name,
        "source": f"pandas_market_calendars {importlib.metadata.version('pandas_market_calendars')} calendar {ex.pmc}",
        "tz": tz,
        "weekmask": "".join("1" if w else "0" for w in weekmask),
        "first": first.isoformat(),
        "last": last.isoformat(),
        "regular": regular,
        "extended": extended,
        "holidays": {str(y): v for y, v in sorted(holidays.items())},
        "special": {str(y): v for y, v in sorted(special.items())},
        "sessions": len(rows),
        "sha256": canonical_digest(rows),
    }
    check_round_trip(out, rows)
    return out


def parse_dur(s: str) -> int:
    """Inverse of fmt_dur: a Go duration string to seconds."""
    sign = -1 if s.startswith("-") else 1
    s = s.lstrip("-")
    total, num = 0, ""
    for ch in s:
        if ch.isdigit():
            num += ch
        else:
            total += int(num) * {"h": 3600, "m": 60, "s": 1}[ch]
            num = ""
    return sign * total


def rebuild_schedule(data: dict) -> list[tuple[dt.date, dict[str, int | None]]]:
    """The full session schedule encoded by one data/<code>.json document."""
    periods = []
    for r in data["regular"]:
        periods.append((dt.date.fromisoformat(r["from"]), {f: parse_dur(r[f]) if f in r else None for f in TIME_FIELDS}))
    weekmask = [c == "1" for c in data["weekmask"]]
    first = dt.date.fromisoformat(data["first"])
    last = dt.date.fromisoformat(data["last"])
    closed = {dt.date(int(y), int(e[:2]), int(e[3:5])) for y, es in data["holidays"].items() for e in es}
    specials: dict[dt.date, list[str]] = {}
    for y, es in data["special"].items():
        for e in es:
            specials[dt.date(int(y), int(e[:2]), int(e[3:5]))] = e[6:].split()
    rebuilt = []
    day = first
    while day <= last:
        fields = specials.get(day, [])
        if (weekmask[day.weekday()] and day not in closed) or "extra" in fields:
            times = dict(regular_at(periods, day))
            for kv in fields:
                if kv == "extra":
                    continue
                k, v = kv.split("=")
                times[k] = None if v == "-" else parse_dur(v)
            rebuilt.append((day, times))
        day += dt.timedelta(days=1)
    return rebuilt


def check_round_trip(data: dict, rows: list[tuple[dt.date, dict[str, int | None]]]) -> None:
    """Rebuild the schedule from the compact form; it must match exactly."""
    if rebuild_schedule(data) != rows:
        raise SystemExit(f"{data['code']}: compact form does not rebuild the source schedule")


# --------------------------------------------------------------------------
# Output


def render_json(data: dict) -> str:
    """Stable JSON: one line per regular period and per year of dates."""
    j = json.dumps
    lines = ["{"]
    for key in ("code", "name", "source", "tz", "weekmask", "first", "last"):
        lines.append(f"  {j(key)}: {j(data[key], ensure_ascii=False)},")
    lines.append('  "regular": [')
    lines.append(",\n".join(f"    {j(r, ensure_ascii=False)}" for r in data["regular"]))
    lines.append("  ],")
    lines.append(f'  "extended": {j(data["extended"])},')
    for key in ("holidays", "special"):
        lines.append(f"  {j(key)}: {{")
        items = [f"    {j(y)}: {j(v, ensure_ascii=False)}" for y, v in data[key].items()]
        lines.append(",\n".join(items)) if items else None
        lines.append("  },")
    lines.append(f'  "sessions": {data["sessions"]},')
    lines.append(f'  "sha256": {j(data["sha256"])}')
    lines.append("}")
    return "\n".join(lines) + "\n"


def render_go(exchanges: tuple[Exchange, ...]) -> str:
    out = [
        "// Code generated by gen/generate.py from pandas_market_calendars. DO NOT EDIT.",
        "",
        "package calendar",
        "",
    ]
    region = None
    for ex in exchanges:
        if ex.region != region:
            region = ex.region
            out.append(f"// {region}")
            out.append("")
        fn = ex.code.upper()
        out.append(f"// {fn} returns the {ex.name} calendar.")
        out.append("// Arguments are accepted for compatibility with scmhub/calendar and ignored:")
        out.append("// coverage is fixed by the generated data (see Calendar.Range).")
        out.append(f"func {fn}(years ...int) *Calendar {{ return load({ex.code!r}) }}".replace("'", '"'))
        out.append("")
    out.append("// registryNames lists every generated calendar, in registry order.")
    out.append("var registryNames = []string{")
    for ex in exchanges:
        out.append(f'\t"{ex.code}",')
    out.append("}")
    out.append("")
    return "\n".join(out)


def main() -> int:
    ap = argparse.ArgumentParser(description=__doc__.splitlines()[0])
    ap.add_argument("--check", action="store_true", help="fail if committed output differs from a fresh generation")
    args = ap.parse_args()

    overrides = load_overrides()
    unknown = set(overrides) - {ex.code for ex in EXCHANGES}
    if unknown:
        raise SystemExit(f"overrides.toml: unknown calendars {sorted(unknown)}")

    expected: dict[Path, str] = {}
    for ex in EXCHANGES:
        expected[DATA_DIR / f"{ex.code}.json"] = render_json(build(ex, overrides.get(ex.code, [])))
    expected[GO_FILE] = render_go(EXCHANGES)

    if args.check:
        drift = [p for p, text in expected.items() if not p.exists() or p.read_text() != text]
        stale = [p for p in DATA_DIR.glob("*.json") if p not in expected]
        for p in drift + stale:
            print(f"drift: {p.relative_to(ROOT)}", file=sys.stderr)
        if drift or stale:
            print("generated data is out of date; run: uv run --locked --project gen gen/generate.py", file=sys.stderr)
            return 1
        print(f"ok: {len(expected)} files match pandas_market_calendars "
              f"{importlib.metadata.version('pandas_market_calendars')}")
        return 0

    DATA_DIR.mkdir(exist_ok=True)
    for p in DATA_DIR.glob("*.json"):
        if p not in expected:
            p.unlink()
    for p, text in expected.items():
        p.write_text(text)
    print(f"wrote {len(expected)} files")
    return 0


if __name__ == "__main__":
    sys.exit(main())
