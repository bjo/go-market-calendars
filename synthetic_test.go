package calendar

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A hand-written calendar exercising paths today's generated data does not:
// a session opening the previous evening, an extra weekend session, and
// special sessions that add or remove a break.
const syntheticJSON = `{
  "code": "test", "name": "Test Exchange", "source": "hand-written", "tz": "America/Chicago",
  "weekmask": "1111100", "first": "2024-01-01", "last": "2024-12-31",
  "regular": [
    {"from": "2024-01-01", "open": "-7h", "close": "16h", "break_start": "15h15m", "break_end": "15h30m"}
  ],
  "extended": {},
  "holidays": {"2024": ["01-01 New Year", "07-04"]},
  "special": {"2024": [
    "03-08 break_start=- break_end=-",
    "03-09 extra open=9h close=12h break_start=- break_end=-",
    "03-11 close=12h"
  ]},
  "sessions": 0, "sha256": ""
}`

func syntheticCalendar(t *testing.T) *Calendar {
	t.Helper()
	d, err := parseData([]byte(syntheticJSON))
	require.NoError(t, err)
	return newCalendar(d)
}

func TestSyntheticSessionOpensPreviousEvening(t *testing.T) {
	c := syntheticCalendar(t)
	chi := func(m time.Month, d, h, min int) time.Time { return time.Date(2024, m, d, h, min, 0, 0, Chicago) }

	h, ok := c.SessionHours(chi(3, 5, 0, 0))
	require.True(t, ok)
	assert.Equal(t, chi(3, 4, 17, 0), h.Open, "-7h opens 17:00 the evening before")
	assert.Equal(t, chi(3, 5, 16, 0), h.Close)

	assert.True(t, c.IsOpen(chi(3, 4, 18, 0)), "Monday evening belongs to Tuesday's session")
	assert.True(t, c.IsOpen(chi(3, 5, 10, 0)))
	assert.False(t, c.IsOpen(chi(3, 5, 15, 20)), "break")
	assert.False(t, c.IsOpen(chi(3, 5, 16, 30)), "between sessions")
	assert.True(t, c.IsOpen(chi(3, 3, 18, 0)), "Sunday evening belongs to Monday's session")
	assert.False(t, c.IsOpen(chi(3, 2, 18, 0)), "Saturday evening: Sunday has no session")
}

func TestSyntheticSpecialSessions(t *testing.T) {
	c := syntheticCalendar(t)
	chi := func(m time.Month, d, h, min int) time.Time { return time.Date(2024, m, d, h, min, 0, 0, Chicago) }

	// 03-08: break removed.
	h, ok := c.SessionHours(chi(3, 8, 0, 0))
	require.True(t, ok)
	assert.True(t, h.BreakStart.IsZero())
	assert.True(t, c.IsOpen(chi(3, 8, 15, 20)), "no break that day")

	// 03-09 is a Saturday with an extra session.
	sat := chi(3, 9, 0, 0)
	assert.True(t, c.IsBusinessDay(sat))
	assert.False(t, c.IsHoliday(sat))
	assert.True(t, c.IsOpen(chi(3, 9, 10, 0)))
	assert.False(t, c.IsOpen(chi(3, 9, 13, 0)))
	assert.Equal(t, chi(3, 9, 0, 0), c.NextBusinessDay(chi(3, 8, 0, 0)))
	assert.Equal(t, chi(3, 9, 12, 0), c.NextClose(sat))
	assert.True(t, c.IsLateOpen(sat), "opens 09:00 versus a regular 17:00 previous-evening open")

	// 03-11: early close keeps the regular open and break.
	assert.True(t, c.IsEarlyClose(chi(3, 11, 0, 0)))
	h, ok = c.SessionHours(chi(3, 11, 0, 0))
	require.True(t, ok)
	assert.Equal(t, chi(3, 10, 17, 0), h.Open)
	assert.Equal(t, chi(3, 11, 12, 0), h.Close)

	// Holidays, named and unnamed.
	ti, hol := c.NextHoliday(chi(6, 1, 0, 0))
	require.NotNil(t, hol)
	assert.Equal(t, chi(7, 4, 0, 0), ti)
	assert.Equal(t, "", hol.Name)
	assert.Contains(t, c.String(), "2024-Jul-04 Thu    Closed")
}

// Out-of-range Next/Previous queries search from the nearest covered date.
func TestOutOfRangeSearchesFromNearestCoveredDate(t *testing.T) {
	c := XNYS()
	assert.Equal(t, ny(1970, 1, 2, 10, 0), c.NextBusinessDay(ny(1950, 1, 3, 10, 0)))
	assert.Equal(t, ny(1970, 1, 2, 10, 0), c.NextBusinessDay(ny(1969, 12, 30, 10, 0)))
	assert.Equal(t, ny(2035, 12, 31, 10, 0), c.PreviousBusinessDay(ny(2040, 6, 1, 10, 0)))
	assert.True(t, c.NextBusinessDay(ny(2040, 6, 1, 10, 0)).IsZero())
	assert.True(t, c.PreviousBusinessDay(ny(1950, 1, 3, 10, 0)).IsZero())
	ti, h := c.NextHoliday(ny(1950, 1, 3, 0, 0))
	require.NotNil(t, h)
	assert.Equal(t, ny(1970, 1, 1, 0, 0), ti)
	assert.Equal(t, ny(1970, 1, 2, 15, 0), c.NextClose(ny(1950, 1, 3, 0, 0)))
	assert.True(t, c.NextClose(ny(2040, 1, 3, 0, 0)).IsZero())
}

// Years far outside int32 day numbers must not wrap into the covered range.
func TestExtremeYearsDoNotWrap(t *testing.T) {
	c := XNYS()
	for _, d := range []time.Time{
		time.Date(11761246, 6, 22, 12, 0, 0, 0, time.UTC),
		time.Date(-11757306, 6, 22, 12, 0, 0, 0, time.UTC),
	} {
		assert.False(t, c.InRange(d), d)
		assert.False(t, c.IsBusinessDay(d), d)
	}
}

// Brazil's DST began at 00:00 before 2019, so local midnight did not exist on
// those days; dates must still land on the right day.
func TestHolidayDateWhereMidnightIsSkipped(t *testing.T) {
	c := BVMF()
	hs := c.Holidays(time.Date(2004, 11, 1, 0, 0, 0, 0, SaoPaulo), time.Date(2004, 11, 3, 0, 0, 0, 0, SaoPaulo))
	require.Len(t, hs, 1)
	assert.Equal(t, 2, hs[0].Date.Day())
	assert.Equal(t, time.November, hs[0].Date.Month())
	assert.True(t, c.IsHoliday(hs[0].Date))
}

func TestCorruptDataReportsTheCalendar(t *testing.T) {
	_, err := parseData([]byte(`{"tz": "Not/AZone", "weekmask": "1111100"}`))
	assert.Error(t, err)
	_, err = parseData([]byte(`{"tz": "UTC", "weekmask": "11"}`))
	assert.Error(t, err)
}
