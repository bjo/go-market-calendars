package calendar

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func ny(y int, m time.Month, d, h, min int) time.Time {
	return time.Date(y, m, d, h, min, 0, 0, NewYork)
}

// No query may panic, whatever the date: out-of-range dates give false or the
// zero time, and Check explains why.
func TestOutOfRangeNeverPanics(t *testing.T) {
	dates := []time.Time{
		{},
		time.Date(1, 1, 1, 0, 0, 0, 0, time.UTC),
		time.Date(1900, 1, 2, 12, 0, 0, 0, time.UTC),
		time.Date(1969, 12, 31, 12, 0, 0, 0, time.UTC),
		time.Date(2036, 1, 2, 12, 0, 0, 0, time.UTC),
		time.Date(9999, 12, 31, 23, 59, 59, 0, time.UTC),
	}
	for _, code := range Names() {
		c := GetCalendar(code)
		for _, d := range dates {
			require.NotPanics(t, func() {
				assert.False(t, c.InRange(d), "%s %s", code, d)
				assert.ErrorIs(t, c.Check(d), ErrOutOfRange)
				assert.False(t, c.IsBusinessDay(d))
				assert.False(t, c.IsHoliday(d))
				assert.False(t, c.IsEarlyClose(d))
				assert.False(t, c.IsLateOpen(d))
				assert.False(t, c.IsOpen(d))
				_, ok := c.SessionHours(d)
				assert.False(t, ok)
				c.NextBusinessDay(d)
				c.PreviousBusinessDay(d)
				c.NextHoliday(d)
				c.NextClose(d)
				c.Holidays(d, d.AddDate(0, 0, 10))
			}, "%s %s", code, d)
		}
	}
}

func TestOldAndFutureDatesInRange(t *testing.T) {
	c := XNYS()
	for _, d := range []time.Time{ny(1970, 1, 2, 12, 0), ny(1985, 1, 2, 12, 0), ny(2005, 1, 3, 12, 0), ny(2035, 12, 31, 12, 0)} {
		assert.NoError(t, c.Check(d), d)
		assert.True(t, c.IsBusinessDay(d), d)
	}
	start, end := c.Years()
	assert.Equal(t, 1970, start)
	assert.Equal(t, 2035, end)
}

func TestRangeEdges(t *testing.T) {
	c := XNYS()
	first, last := c.Range()
	assert.True(t, c.NextBusinessDay(last).IsZero(), "no session after coverage")
	assert.True(t, c.PreviousBusinessDay(first).IsZero(), "no session before coverage")
	assert.Equal(t, ny(1970, 1, 2, 0, 0), c.NextBusinessDay(first.AddDate(0, 0, -1)), "entering from just before the range")
	err := c.Check(first.AddDate(0, 0, -1))
	require.Error(t, err)
	assert.True(t, errors.Is(err, ErrOutOfRange))
	assert.Contains(t, err.Error(), "1969-12-31")
}

func TestBusinessDaysAndHolidays(t *testing.T) {
	c := XNYS()
	assert.False(t, c.IsBusinessDay(ny(2025, 12, 25, 0, 0)), "Christmas")
	assert.True(t, c.IsHoliday(ny(2025, 12, 25, 0, 0)))
	assert.False(t, c.IsBusinessDay(ny(2025, 12, 27, 0, 0)), "Saturday")
	assert.False(t, c.IsHoliday(ny(2025, 12, 27, 0, 0)), "weekends are not holidays")
	assert.True(t, c.IsBusinessDay(ny(2025, 12, 26, 0, 0)))

	// The calendar date is read in t's own location, so a UTC-midnight date
	// asks about that date, not the New York evening before it.
	assert.False(t, c.IsBusinessDay(time.Date(2025, 12, 25, 0, 0, 0, 0, time.UTC)))
	assert.True(t, c.IsBusinessDay(time.Date(2025, 12, 26, 0, 0, 0, 0, time.UTC)))

	ti, h := c.NextHoliday(ny(2025, 12, 1, 0, 0))
	require.NotNil(t, h)
	assert.Equal(t, ny(2025, 12, 25, 0, 0), ti)
	assert.Equal(t, ti, h.Date)

	hs := c.Holidays(ny(2025, 12, 1, 0, 0), ny(2026, 1, 1, 0, 0))
	require.Len(t, hs, 2)
	assert.Equal(t, ny(2026, 1, 1, 0, 0), hs[1].Date)
}

func TestNextBusinessDayKeepsClockAndLocation(t *testing.T) {
	c := XNYS()
	assert.Equal(t, ny(2025, 12, 26, 15, 30), c.NextBusinessDay(ny(2025, 12, 24, 15, 30)))
	assert.Equal(t, ny(2025, 12, 29, 15, 30), c.NextBusinessDay(ny(2025, 12, 26, 15, 30)))
	assert.Equal(t, ny(2025, 12, 24, 9, 0), c.PreviousBusinessDay(ny(2025, 12, 26, 9, 0)))
	utc := time.Date(2025, 12, 24, 0, 0, 0, 0, time.UTC)
	assert.Equal(t, time.Date(2025, 12, 26, 0, 0, 0, 0, time.UTC), c.NextBusinessDay(utc))
}

func TestEarlyCloseAndNextClose(t *testing.T) {
	c := XNYS()
	assert.True(t, c.IsEarlyClose(ny(2025, 11, 28, 0, 0)), "day after Thanksgiving")
	assert.False(t, c.IsEarlyClose(ny(2025, 11, 26, 0, 0)))
	assert.Equal(t, ny(2025, 11, 28, 13, 0), c.NextClose(ny(2025, 11, 28, 8, 0)))
	assert.Equal(t, ny(2025, 11, 26, 16, 0), c.NextClose(ny(2025, 11, 26, 8, 0)))
	assert.Equal(t, ny(2025, 11, 28, 13, 0), c.NextClose(ny(2025, 11, 27, 8, 0)), "holiday rolls to the next session")
	// Historical hours come from the data: the close was 15:30 before 1974.
	assert.Equal(t, ny(1972, 6, 1, 15, 30), c.NextClose(ny(1972, 6, 1, 12, 0)))
}

func TestIsOpen(t *testing.T) {
	c := XNYS()
	assert.False(t, c.IsOpen(ny(2025, 11, 26, 9, 29)))
	assert.True(t, c.IsOpen(ny(2025, 11, 26, 9, 30)))
	assert.True(t, c.IsOpen(ny(2025, 11, 26, 16, 0)))
	assert.False(t, c.IsOpen(ny(2025, 11, 26, 16, 1)))
	assert.False(t, c.IsOpen(ny(2025, 11, 28, 13, 30)), "early close")
	assert.False(t, c.IsOpen(ny(2025, 11, 27, 12, 0)), "holiday")
	assert.True(t, c.IsOpen(time.Date(2025, 11, 26, 15, 0, 0, 0, time.UTC)), "instants convert to the exchange's zone")

	hk := XHKG()
	assert.True(t, hk.IsOpen(time.Date(2025, 11, 26, 11, 0, 0, 0, HongKong)))
	assert.False(t, hk.IsOpen(time.Date(2025, 11, 26, 12, 30, 0, 0, HongKong)), "lunch break")
	assert.True(t, hk.IsOpen(time.Date(2025, 11, 26, 13, 30, 0, 0, HongKong)))
}

func TestSessionHours(t *testing.T) {
	c := XNYS()
	h, ok := c.SessionHours(ny(2025, 7, 3, 0, 0))
	require.True(t, ok)
	assert.Equal(t, ny(2025, 7, 3, 9, 30), h.Open)
	assert.Equal(t, ny(2025, 7, 3, 13, 0), h.Close)
	assert.True(t, h.BreakStart.IsZero())
	_, ok = c.SessionHours(ny(2025, 7, 4, 0, 0))
	assert.False(t, ok)
}

func TestSession(t *testing.T) {
	s := XNYS().Session()
	assert.Equal(t, 9*time.Hour+30*time.Minute, s.Open)
	assert.Equal(t, 16*time.Hour, s.Close)
	assert.Equal(t, 13*time.Hour, s.EarlyClose)
	assert.Equal(t, 4*time.Hour, s.EarlyOpen)
	assert.Equal(t, 20*time.Hour, s.LateClose)
	assert.False(t, s.HasBreak())

	hk := XHKG().Session()
	assert.True(t, hk.HasBreak())

	c := XNYS()
	c.SetSession(&Session{Open: time.Hour})
	assert.Equal(t, time.Hour, c.Session().Open)
	assert.Equal(t, 9*time.Hour+30*time.Minute, XNYS().Session().Open, "each Calendar has its own Session")
}

func TestString(t *testing.T) {
	s := XNYS().String()
	assert.True(t, strings.HasPrefix(s, "Calendar New York Stock Exchange:\n"))
	assert.Contains(t, s, "2025-Dec-25 Thu    Christmas")
	assert.Contains(t, s, "2025-Nov-28 Fri ec Early close")
}
