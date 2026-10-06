# go-market-calendars

[![Go Reference](https://pkg.go.dev/badge/github.com/bjo/go-market-calendars.svg)](https://pkg.go.dev/github.com/bjo/go-market-calendars)
[![License: MIT](https://img.shields.io/badge/License-MIT-blue.svg)](LICENCE)

Exchange trading calendars for Go: sessions, holidays, early closes and
trading hours for 37 exchanges, identified by their
[ISO 10383](https://www.iso20022.org/market-identifier-codes) Market Identifier
Code (MIC).

The data is **generated from
[pandas_market_calendars](https://github.com/rsheftel/pandas_market_calendars)**
(which wraps [exchange_calendars](https://github.com/gerrymanoim/exchange_calendars)),
so Go and Python code can share one source of truth. A drift test proves the
embedded tables equal a fresh generation from the pinned version.

## Why this fork

This is a fork of [scmhub/calendar](https://github.com/scmhub/calendar), and it
keeps that package's Go API shape (`calendar.XNYS()`, `IsBusinessDay`,
`IsOpen`, `NextClose`, `Session()`, ...). It changes two things:

- **No panics on historical or future dates.** scmhub/calendar builds a
  rolling window of ±5 years around the current year and panics outside it, so
  code that queries older dates breaks as the clock moves on. Here, each
  calendar covers a fixed range (from 1970, or the exchange's first recorded
  year, up to 2035). Out-of-range queries never panic: boolean queries return
  `false`, `Check` returns `ErrOutOfRange`, and the Next/Previous queries
  search from the nearest covered date.
- **Data, not hand-written rules.** Some historical NYSE rules in
  scmhub/calendar are wrong once you look past the last few years: Martin
  Luther King Jr. Day closed the market only from 1998; Washington's Birthday
  and Memorial Day moved to Mondays only in 1971; and there are special
  closures such as presidential election days through 1980 and national days
  of mourning. Instead of maintaining rules by hand, this fork generates its
  data from pandas_market_calendars.

## Installation

```bash
go get github.com/bjo/go-market-calendars
```

The package name is `calendar`:

```go
import calendar "github.com/bjo/go-market-calendars"
```

## Usage

```go
nyse := calendar.XNYS()

day := time.Date(2025, time.January, 9, 0, 0, 0, 0, calendar.NewYork)
nyse.IsBusinessDay(day)   // false: national day of mourning for President Carter
nyse.NextBusinessDay(day) // 2025-01-10

// Historical dates work, and hours follow the data.
nyse.IsBusinessDay(time.Date(1997, time.January, 20, 0, 0, 0, 0, calendar.NewYork)) // true: MLK Day before 1998
nyse.NextClose(time.Date(1972, time.June, 1, 12, 0, 0, 0, calendar.NewYork))      // 15:30, the close until 1974

// Instants: is the market trading right now?
nyse.IsOpen(time.Now())

// Out of range never panics.
old := time.Date(1950, time.January, 3, 0, 0, 0, 0, calendar.NewYork)
nyse.IsBusinessDay(old)                                // false
errors.Is(nyse.Check(old), calendar.ErrOutOfRange)    // true

// Look up by MIC.
lse := calendar.GetCalendar("xlon")
for _, h := range lse.Holidays(time.Date(2025, 1, 1, 0, 0, 0, 0, calendar.London), time.Date(2025, 12, 31, 0, 0, 0, 0, calendar.London)) {
	fmt.Println(h.Date.Format("2006-01-02"), h.Name)
}
```

### Dates and instants

Day-level queries (`IsBusinessDay`, `IsHoliday`, `IsEarlyClose`,
`IsLateOpen`, `NextBusinessDay`, `PreviousBusinessDay`, `NextHoliday`,
`Holidays`, `NextClose`, `SessionHours`) use the calendar date of `t` **as
written, in t's own location**. So `2025-01-09T00:00Z` asks about 9 January.
`IsOpen` takes an instant and converts it to the exchange's time zone.

### API

| Method | Notes |
| --- | --- |
| `IsBusinessDay(t)` | the date is a trading session |
| `IsHoliday(t)` | the date is a weekday with no session (weekends are not holidays) |
| `IsEarlyClose(t)`, `IsLateOpen(t)` | the session closes early or opens late versus that period's regular hours |
| `IsOpen(t)` | the exchange is trading at instant `t`; bounds are inclusive, and lunch breaks are excluded |
| `SessionHours(t)` | the session's actual open, close and break times |
| `NextBusinessDay(t)`, `PreviousBusinessDay(t)` | keep `t`'s clock time and location |
| `NextHoliday(t)`, `Holidays(start, end)` | holiday dates with the source's names, where it has them |
| `NextClose(t)` | close of the session on `t`'s date, or of the next session |
| `Session()` | the regular hours in force today, as offsets from midnight |
| `Range()`, `Years()`, `InRange(t)`, `Check(t)` | the coverage range |
| `GetCalendar(mic)`, `Names()` | registry lookup |

### Differences from scmhub/calendar

- Year arguments to `XNYS(...)`, `GetCalendar(name, ...)` and the other
  constructors are accepted and ignored. Coverage is fixed by the data.
- The holiday rule engine (`Holiday` rules, `AddHolidays`,
  `AddEarlyClosingDays`, `NewCalendar`, `SetYears`, the observance helpers and
  the lunar/solar-term helpers) is removed. To add or correct a date, use
  `overrides.toml` (below). `Holiday` is now a plain `{Name, Date}` value.
- `Session()` hours come from the data, including extended hours where the
  source defines them (for US equities, `EarlyOpen` is 04:00 and `LateClose` is
  20:00).
- Out-of-range dates never panic.

## Exchanges

| MIC | Exchange | Time zone | From | To | pandas_market_calendars name |
| --- | --- | --- | --- | --- | --- |
| XNYS | New York Stock Exchange | America/New_York | 1970-01-01 | 2035-12-31 | `NYSE` |
| XNAS | NASDAQ | America/New_York | 1971-02-08 | 2035-12-31 | `NASDAQ` |
| XCBO | Chicago Board Options Exchange | America/Chicago | 1973-04-26 | 2035-12-31 | `CBOE_Equity_Options` |
| XCBF | Cboe Futures Exchange | America/Chicago | 2004-03-26 | 2035-12-31 | `CFE` |
| XTSE | Toronto Stock Exchange | America/Toronto | 1970-01-01 | 2035-12-31 | `XTSE` |
| XMEX | Mexican Stock Exchange | America/Mexico_City | 1970-01-01 | 2035-12-31 | `XMEX` |
| BVMF | B3 - Brasil Bolsa Balcão | America/Sao_Paulo | 1970-01-01 | 2035-12-31 | `BVMF` |
| XLON | London Stock Exchange | Europe/London | 1970-01-01 | 2035-12-31 | `XLON` |
| XAMS | Euronext Amsterdam | Europe/Amsterdam | 1970-01-01 | 2035-12-31 | `XAMS` |
| XBRU | Euronext Brussels | Europe/Brussels | 1970-01-01 | 2035-12-31 | `XBRU` |
| XLIS | Euronext Lisbon | Europe/Lisbon | 1970-01-01 | 2035-12-31 | `XLIS` |
| XPAR | Euronext Paris | Europe/Paris | 1970-01-01 | 2035-12-31 | `XPAR` |
| XMIL | Borsa Italiana | Europe/Rome | 1970-01-01 | 2035-12-31 | `XMIL` |
| XMAD | Bolsa de Madrid | Europe/Madrid | 1970-01-01 | 2035-12-31 | `XMAD` |
| XFRA | Frankfurt Stock Exchange | Europe/Berlin | 1970-01-01 | 2035-12-31 | `XFRA` |
| XETR | Deutsche Börse Xetra | Europe/Berlin | 1997-11-28 | 2035-12-31 | `XETR` |
| XSWX | SIX Swiss Exchange | Europe/Zurich | 1970-01-01 | 2035-12-31 | `XSWX` |
| XDUB | Euronext Dublin | Europe/Dublin | 1970-01-01 | 2035-12-31 | `XDUB` |
| XWBO | Wiener Börse | Europe/Vienna | 1970-01-01 | 2035-12-31 | `XWBO` |
| XSTO | Nasdaq Stockholm | Europe/Stockholm | 1970-01-01 | 2035-12-31 | `XSTO` |
| XCSE | Nasdaq Copenhagen | Europe/Copenhagen | 1970-01-01 | 2035-12-31 | `XCSE` |
| XHEL | Nasdaq Helsinki | Europe/Helsinki | 1970-01-01 | 2035-12-31 | `XHEL` |
| XOSL | Oslo Børs | Europe/Oslo | 1970-01-01 | 2035-12-31 | `XOSL` |
| XJSE | Johannesburg Stock Exchange | Africa/Johannesburg | 1970-01-01 | 2035-12-31 | `XJSE` |
| XBOM | BSE (Bombay Stock Exchange) | Asia/Calcutta | 1997-01-01 | 2026-12-31 | `XBOM` |
| XNSE | National Stock Exchange of India | Asia/Calcutta | 1996-01-01 | 2026-12-31 | `XNSE` |
| XBKK | Stock Exchange of Thailand | Asia/Bangkok | 1975-04-30 | 2035-12-31 | `XBKK` |
| XSES | Singapore Exchange | Asia/Singapore | 1986-01-01 | 2026-12-31 | `XSES` |
| XHKG | Hong Kong Stock Exchange | Asia/Hong_Kong | 1970-01-01 | 2035-12-31 | `XHKG` |
| XSHG | Shanghai Stock Exchange | Asia/Shanghai | 1991-01-01 | 2026-12-31 | `XSHG` |
| XSHE | Shenzhen Stock Exchange | Asia/Shanghai | 1991-01-01 | 2026-12-31 | `XSHG` |
| XKRX | Korea Exchange | Asia/Seoul | 1970-01-01 | 2035-12-31 | `XKRX` |
| XTAI | Taiwan Stock Exchange | Asia/Taipei | 1970-01-01 | 2035-12-31 | `XTAI` |
| XJPX | Japan Exchange Group | Asia/Tokyo | 1997-01-01 | 2035-12-31 | `JPX` |
| XTKS | Tokyo Stock Exchange | Asia/Tokyo | 1997-01-01 | 2035-12-31 | `XTKS` |
| XASX | Australian Securities Exchange | Australia/Sydney | 1970-01-01 | 2035-12-31 | `XASX` |
| XNZE | New Zealand Exchange | Pacific/Auckland | 1970-01-01 | 2035-12-31 | `XNZE` |

Coverage is clipped to the range where the source has recorded data. That
range is bounded by 1970 and 2035, by the exchange's first trading day, by
exchange_calendars' own bounds for that market, and by the years the source
has a holiday list for. Several Asian calendars end in 2026 because their
holiday lists are published year by year. Regenerating after an upstream
release extends them.

Accuracy follows pandas_market_calendars and exchange_calendars. For the
historically tricky NYSE dates, the spot-check table in `xnys_test.go` cites a
public primary source for each one.

## Generated data

`data/*.json` and `exchanges_gen.go` are generated by `gen/generate.py` and
committed. Each JSON file holds the regular-hours periods, holidays by year,
special sessions (early closes, late opens, unusual hours) and a SHA-256 digest
of the full schedule.

```bash
# Regenerate (uses the pinned pandas_market_calendars from gen/uv.lock)
uv run --locked --project gen gen/generate.py

# Fail if the committed data differs from a fresh generation
uv run --locked --project gen gen/generate.py --check
```

`go test ./...` checks three things:

- every calendar rebuilds the generator's schedule digest exactly (every
  session, open, close and break);
- regenerating reproduces the committed files byte for byte (this needs `uv`
  and is skipped in `-short` mode or without it);
- no query panics for any date.

### Staying current

A weekly workflow (`.github/workflows/pmc-sync.yml`, Mondays) upgrades
pandas_market_calendars and exchange_calendars to their latest **PyPI
releases** and regenerates. If anything changed, it opens a pull request
whose description lists, per exchange, the days that became closures or
sessions and the sessions whose hours changed, flagging those within a
month before or a year after the run date (they change live behaviour).
`gen/diff.py` produces that report and can be run locally:

```bash
uv run --locked --project gen gen/diff.py --base main
```

Merging the pull request and tagging a release is a manual step.
Unreleased upstream fixes are not picked up; use `overrides.toml` when a
correction cannot wait for a release.

### Overrides

`overrides.toml` holds corrections and additions on top of
pandas_market_calendars. Every entry needs a `reason` and a public primary
`source`. The generator rejects entries the source already agrees with, so
stale corrections do not linger. There are none today.

## Credits and licences

- [scmhub/calendar](https://github.com/scmhub/calendar) by Philippe Chavanne,
  MIT. This repository is a fork of it and keeps its [LICENCE](LICENCE).
- [pandas_market_calendars](https://github.com/rsheftel/pandas_market_calendars),
  MIT. The source of the generated data.
- [exchange_calendars](https://github.com/gerrymanoim/exchange_calendars),
  Apache-2.0, wrapped by pandas_market_calendars for most non-US exchanges.

See [NOTICE](NOTICE) for details.
