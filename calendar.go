package calendar

import (
	"errors"
	"fmt"
	"strings"
	"time"
)

// YearsAhead and YearsPast described scmhub/calendar's default rolling window.
// Calendars here cover a fixed range set by the generated data instead; the
// constants remain for source compatibility.
const YearsAhead = 5
const YearsPast = 5

// Predefined time locations for various cities.
var (
	Mexico, _       = time.LoadLocation("America/Mexico_City")
	Chicago, _      = time.LoadLocation("America/Chicago")
	Toronto, _      = time.LoadLocation("America/Toronto")
	NewYork, _      = time.LoadLocation("America/New_York")
	SaoPaulo, _     = time.LoadLocation("America/Sao_Paulo")
	London, _       = time.LoadLocation("Europe/London")
	Lisbon, _       = time.LoadLocation("Europe/Lisbon")
	Madrid, _       = time.LoadLocation("Europe/Madrid")
	Amsterdam, _    = time.LoadLocation("Europe/Amsterdam")
	Brussels, _     = time.LoadLocation("Europe/Brussels")
	Paris, _        = time.LoadLocation("Europe/Paris")
	Zurich, _       = time.LoadLocation("Europe/Zurich")
	Milan, _        = time.LoadLocation("Europe/Rome")
	Franckfurt, _   = time.LoadLocation("Europe/Berlin")
	Moscow, _       = time.LoadLocation("Europe/Moscow")
	Johannesburg, _ = time.LoadLocation("Africa/Johannesburg")
	Dubai, _        = time.LoadLocation("Asia/Dubai")
	Mumbai, _       = time.LoadLocation("Asia/Kolkata")
	Singapore, _    = time.LoadLocation("Asia/Singapore")
	Bangkok, _      = time.LoadLocation("Asia/Bangkok")
	HongKong, _     = time.LoadLocation("Asia/Hong_Kong")
	Shenzhen, _     = time.LoadLocation("Asia/Hong_Kong")
	Shanghai, _     = time.LoadLocation("Asia/Shanghai")
	Seoul, _        = time.LoadLocation("Asia/Seoul")
	Tokyo, _        = time.LoadLocation("Asia/Tokyo")
	Sydney, _       = time.LoadLocation("Australia/Sydney")
)

// ErrOutOfRange is returned by Check for a date outside a calendar's coverage.
var ErrOutOfRange = errors.New("date outside calendar coverage")

// Session defines the regular operating hours of a calendar, as offsets from
// local midnight. EarlyOpen and LateClose are the extended-hours bounds where
// the source data defines them.
type Session struct {
	EarlyOpen  time.Duration
	Open       time.Duration
	BreakStart time.Duration
	BreakStop  time.Duration
	Close      time.Duration
	EarlyClose time.Duration
	LateClose  time.Duration
}

// HasBreak checks if the session has a break defined.
func (s *Session) HasBreak() bool {
	return s.BreakStart != 0
}

// IsZero checks if the Session is empty.
func (s Session) IsZero() bool {
	return s == Session{}
}

// SessionHours are the actual trading hours of one session.
// BreakStart and BreakEnd are zero when the session has no break.
type SessionHours struct {
	Open       time.Time
	BreakStart time.Time
	BreakEnd   time.Time
	Close      time.Time
}

// Holiday is a weekday on which the exchange is closed.
type Holiday struct {
	Name string    // empty when the source does not name the closure
	Date time.Time // midnight in the calendar's location
}

// Calendar is an exchange calendar backed by generated data.
//
// Day-level queries (IsBusinessDay, IsHoliday, IsEarlyClose, NextBusinessDay,
// NextClose, ...) use the calendar date of t as written, in t's own location:
// 2025-01-09 00:00 UTC asks about 9 January. Instant queries (IsOpen) convert t
// to the calendar's location first.
//
// Dates outside Range never panic: boolean queries return false, Check
// reports ErrOutOfRange, and the Next/Previous queries search from the
// nearest covered date, returning the zero time when the coverage range has
// no answer in that direction.
type Calendar struct {
	Name    string
	Loc     *time.Location
	d       *calData
	session *Session
}

// Code returns the calendar's registry code, e.g. "xnys".
func (c *Calendar) Code() string { return c.d.code }

// Source describes where the calendar data was generated from.
func (c *Calendar) Source() string { return c.d.source }

// Session returns the regular session in force today (or at the nearest end
// of the coverage range).
func (c *Calendar) Session() *Session {
	return c.session
}

// SetSession replaces the value returned by Session. It does not change the
// generated session hours used by IsOpen, NextClose and SessionHours.
func (c *Calendar) SetSession(s *Session) {
	c.session = s
}

// Years returns the first and last year of the calendar's coverage.
func (c *Calendar) Years() (start, end int) {
	return c.d.first.year(), c.d.last.year()
}

// Range returns the first and last covered dates, at midnight in c.Loc.
func (c *Calendar) Range() (first, last time.Time) {
	return c.d.first.midnight(c.Loc), c.d.last.midnight(c.Loc)
}

// InRange reports whether the calendar date of t is covered.
func (c *Calendar) InRange(t time.Time) bool {
	k := dayOf(t)
	return k >= c.d.first && k <= c.d.last
}

// Check returns an error wrapping ErrOutOfRange if the calendar date of t is
// not covered, and nil otherwise.
func (c *Calendar) Check(t time.Time) error {
	if c.InRange(t) {
		return nil
	}
	return fmt.Errorf("%s: %s: %w (%s to %s)", c.d.code, t.Format(time.DateOnly), ErrOutOfRange,
		c.d.first.String(), c.d.last.String())
}

// IsBusinessDay reports whether the calendar date of t is a trading session.
func (c *Calendar) IsBusinessDay(t time.Time) bool {
	_, ok := c.d.session(dayOf(t))
	return ok
}

// IsHoliday reports whether the calendar date of t is a weekday on which the
// exchange is closed. Weekends are not holidays.
func (c *Calendar) IsHoliday(t time.Time) bool {
	_, ok := c.d.holidays[dayOf(t)]
	return ok
}

// IsEarlyClose reports whether the session on the calendar date of t closes
// before the regular close.
func (c *Calendar) IsEarlyClose(t time.Time) bool {
	k := dayOf(t)
	s, ok := c.d.session(k)
	return ok && s.close < c.d.regular(k).close
}

// IsLateOpen reports whether the session on the calendar date of t opens after
// the regular open.
func (c *Calendar) IsLateOpen(t time.Time) bool {
	k := dayOf(t)
	s, ok := c.d.session(k)
	return ok && s.open > c.d.regular(k).open
}

// IsOpen reports whether the exchange is trading at instant t. Bounds are
// inclusive, and a break excludes its interior.
func (c *Calendar) IsOpen(t time.Time) bool {
	lt := t.In(c.Loc)
	today := dayOf(lt)
	// A session can open on the previous evening or close after midnight.
	for _, k := range []day{today - 1, today, today + 1} {
		s, ok := c.d.session(k)
		if !ok {
			continue
		}
		h := s.hours(k, c.Loc)
		if lt.Before(h.Open) || lt.After(h.Close) {
			continue
		}
		if s.hasBreak && lt.After(h.BreakStart) && lt.Before(h.BreakEnd) {
			continue
		}
		return true
	}
	return false
}

// SessionHours returns the trading hours of the session on the calendar date
// of t. ok is false when that date is not a session.
func (c *Calendar) SessionHours(t time.Time) (h SessionHours, ok bool) {
	k := dayOf(t)
	s, ok := c.d.session(k)
	if !ok {
		return SessionHours{}, false
	}
	return s.hours(k, c.Loc), true
}

// NextBusinessDay returns the first session after the calendar date of t, at
// t's clock time and location. A date before the coverage range gets the
// first covered session; the zero time means there is none.
func (c *Calendar) NextBusinessDay(t time.Time) time.Time {
	return c.stepBusinessDay(t, 1)
}

// PreviousBusinessDay returns the last session before the calendar date of t,
// at t's clock time and location. A date after the coverage range gets the
// last covered session; the zero time means there is none.
func (c *Calendar) PreviousBusinessDay(t time.Time) time.Time {
	return c.stepBusinessDay(t, -1)
}

func (c *Calendar) stepBusinessDay(t time.Time, step int) time.Time {
	from := dayOf(t)
	k := from
	// Start the search at the edge of the coverage range.
	if step > 0 && k < c.d.first-1 {
		k = c.d.first - 1
	}
	if step < 0 && k > c.d.last+1 {
		k = c.d.last + 1
	}
	for k += day(step); k >= c.d.first && k <= c.d.last; k += day(step) {
		if _, ok := c.d.session(k); ok {
			return t.AddDate(0, 0, int(k-from))
		}
	}
	return time.Time{}
}

// NextHoliday returns the first holiday after the calendar date of t. It
// returns the zero time and nil when there is none within the coverage range.
func (c *Calendar) NextHoliday(t time.Time) (time.Time, *Holiday) {
	k := dayOf(t)
	i := c.d.holidayIndex(k + 1)
	if i == len(c.d.holidayDays) {
		return time.Time{}, nil
	}
	h := c.holiday(c.d.holidayDays[i])
	return h.Date, &h
}

// Holidays returns the holidays between the calendar dates of start and end,
// inclusive.
func (c *Calendar) Holidays(start, end time.Time) []Holiday {
	var out []Holiday
	last := dayOf(end)
	for i := c.d.holidayIndex(dayOf(start)); i < len(c.d.holidayDays) && c.d.holidayDays[i] <= last; i++ {
		out = append(out, c.holiday(c.d.holidayDays[i]))
	}
	return out
}

func (c *Calendar) holiday(k day) Holiday {
	return Holiday{Name: c.d.holidays[k], Date: k.midnight(c.Loc)}
}

// NextClose returns the close of the session on the calendar date of t, or of
// the next session if that date is not one. It returns the zero time when
// there is no such session within the coverage range.
func (c *Calendar) NextClose(t time.Time) time.Time {
	k := dayOf(t)
	if k < c.d.first {
		k = c.d.first
	}
	for ; k <= c.d.last; k++ {
		if s, ok := c.d.session(k); ok {
			return s.hours(k, c.Loc).Close
		}
	}
	return time.Time{}
}

func (c *Calendar) String() string {
	var b strings.Builder
	fmt.Fprintf(&b, "Calendar %v:\n", c.Name)
	for k := c.d.first; k <= c.d.last; k++ {
		if name, ok := c.d.holidays[k]; ok {
			if name == "" {
				name = "Closed"
			}
			fmt.Fprintf(&b, "\t%-15v    %v\n", k.midnight(c.Loc).Format("2006-Jan-02 Mon"), name)
		} else if c.IsEarlyClose(k.midnight(c.Loc)) {
			fmt.Fprintf(&b, "\t%-15v ec Early close\n", k.midnight(c.Loc).Format("2006-Jan-02 Mon"))
		}
	}
	return b.String()
}
