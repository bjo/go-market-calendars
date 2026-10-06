package calendar

import (
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func resetAdjustments(t *testing.T, code string) {
	t.Helper()
	t.Cleanup(func() { require.NoError(t, SetAdjustments(code, nil)) })
}

func TestAdjustmentClosesASession(t *testing.T) {
	resetAdjustments(t, "xnys")
	c := XNYS() // created before the adjustment: it must still see it
	day := ny(2026, 12, 28, 0, 0)
	require.True(t, c.IsBusinessDay(day))

	require.NoError(t, SetAdjustments("xnys", []Adjustment{{Date: day, Closed: true, Name: "Day of mourning"}}))
	assert.False(t, c.IsBusinessDay(day))
	assert.True(t, c.IsHoliday(day))
	assert.False(t, c.IsOpen(ny(2026, 12, 28, 12, 0)))
	assert.Equal(t, ny(2026, 12, 29, 16, 0), c.NextClose(day), "rolls to the next session")
	assert.Equal(t, ny(2026, 12, 29, 10, 0), c.NextBusinessDay(ny(2026, 12, 25, 10, 0)))
	ti, h := c.NextHoliday(ny(2026, 12, 26, 0, 0))
	require.NotNil(t, h)
	assert.Equal(t, day, ti)
	assert.Equal(t, "Day of mourning", h.Name)
	assert.Contains(t, c.String(), "2026-Dec-28 Mon    Day of mourning")
	assert.True(t, XNYS().IsHoliday(day), "new Calendar values see it too")

	require.NoError(t, SetAdjustments("xnys", nil))
	assert.True(t, c.IsBusinessDay(day), "removing adjustments restores the data")
	assert.False(t, c.IsHoliday(day))
}

func TestAdjustmentChangesHours(t *testing.T) {
	resetAdjustments(t, "xnys")
	c := XNYS()
	day := ny(2026, 12, 28, 0, 0)
	require.NoError(t, SetAdjustments("xnys", []Adjustment{{Date: day, Open: ny(2026, 12, 28, 9, 30), Close: ny(2026, 12, 28, 13, 0)}}))
	assert.True(t, c.IsBusinessDay(day))
	assert.True(t, c.IsEarlyClose(day))
	assert.False(t, c.IsLateOpen(day))
	assert.True(t, c.IsOpen(ny(2026, 12, 28, 12, 59)))
	assert.False(t, c.IsOpen(ny(2026, 12, 28, 13, 1)))
	h, ok := c.SessionHours(day)
	require.True(t, ok)
	assert.Equal(t, ny(2026, 12, 28, 13, 0), h.Close)
	// Instants in another zone are converted to the exchange's wall clock.
	require.NoError(t, SetAdjustments("xnys", []Adjustment{{Date: day,
		Open: time.Date(2026, 12, 28, 15, 30, 0, 0, time.UTC), Close: time.Date(2026, 12, 28, 21, 0, 0, 0, time.UTC)}}))
	assert.True(t, c.IsLateOpen(day), "10:30 ET")
}

func TestAdjustmentOpensAHolidayAndAWeekend(t *testing.T) {
	resetAdjustments(t, "xnys")
	c := XNYS()
	xmas, sat := ny(2026, 12, 25, 0, 0), ny(2026, 12, 26, 0, 0)
	require.True(t, c.IsHoliday(xmas))
	require.NoError(t, SetAdjustments("xnys", []Adjustment{
		{Date: sat, Open: ny(2026, 12, 26, 9, 30), Close: ny(2026, 12, 26, 12, 0)},
		{Date: xmas, Open: ny(2026, 12, 25, 9, 30), Close: ny(2026, 12, 25, 16, 0)},
	}))
	assert.True(t, c.IsBusinessDay(xmas))
	assert.False(t, c.IsHoliday(xmas), "an opened holiday is no longer a holiday")
	assert.True(t, c.IsBusinessDay(sat), "an extra weekend session")
	assert.Equal(t, sat, c.NextBusinessDay(xmas))
	hs := c.Holidays(ny(2026, 12, 1, 0, 0), ny(2026, 12, 31, 0, 0))
	assert.Empty(t, hs)
	list := Adjustments("xnys")
	require.Len(t, list, 2)
	assert.Equal(t, xmas, list[0].Date, "sorted by date")
}

func TestAdjustmentWithBreak(t *testing.T) {
	resetAdjustments(t, "xhkg")
	c := XHKG()
	hk := func(h, m int) time.Time { return time.Date(2026, 11, 25, h, m, 0, 0, HongKong) }
	require.NoError(t, SetAdjustments("xhkg", []Adjustment{{Date: hk(0, 0),
		Open: hk(10, 0), BreakStart: hk(12, 0), BreakEnd: hk(14, 0), Close: hk(16, 0)}}))
	assert.False(t, c.IsOpen(hk(13, 0)))
	assert.True(t, c.IsOpen(hk(14, 30)))
}

func TestSetAdjustmentsRejectsBadInput(t *testing.T) {
	resetAdjustments(t, "xnys")
	day := ny(2026, 12, 28, 0, 0)
	session := Adjustment{Date: day, Open: ny(2026, 12, 28, 9, 30), Close: ny(2026, 12, 28, 16, 0)}
	cases := map[string][]Adjustment{
		"out of range":   {{Date: ny(1950, 1, 3, 0, 0), Closed: true}},
		"duplicate date": {{Date: day, Closed: true}, {Date: day, Closed: true}},
		"no hours":       {{Date: day}},
		"close <= open":  {{Date: day, Open: ny(2026, 12, 28, 16, 0), Close: ny(2026, 12, 28, 9, 30)}},
		"half a break":   {{Date: day, Open: session.Open, Close: session.Close, BreakStart: ny(2026, 12, 28, 12, 0)}},
		"break outside":  {{Date: day, Open: session.Open, Close: session.Close, BreakStart: ny(2026, 12, 28, 8, 0), BreakEnd: ny(2026, 12, 28, 9, 0)}},
	}
	for name, adj := range cases {
		t.Run(name, func(t *testing.T) {
			assert.Error(t, CheckAdjustments("xnys", adj))
			assert.Error(t, SetAdjustments("xnys", adj))
		})
	}
	assert.NoError(t, CheckAdjustments("xnys", []Adjustment{session}))
	assert.Empty(t, Adjustments("xnys"), "checking installs nothing")
	assert.Error(t, CheckAdjustments("nope", nil))
	err := SetAdjustments("xnys", cases["out of range"])
	assert.True(t, errors.Is(err, ErrOutOfRange))
	assert.Error(t, SetAdjustments("nope", nil))
	assert.Nil(t, Adjustments("nope"))

	// A rejected set leaves the previous one in force.
	require.NoError(t, SetAdjustments("xnys", []Adjustment{{Date: day, Closed: true}}))
	require.Error(t, SetAdjustments("xnys", cases["no hours"]))
	assert.False(t, XNYS().IsBusinessDay(day))
}

// Queries race with SetAdjustments; run with -race. Each query sees one whole
// set (the swap is atomic), so a lookup of a single date is never torn.
func TestSetAdjustmentsIsSafeForConcurrentUse(t *testing.T) {
	resetAdjustments(t, "xnys")
	c := XNYS()
	a := ny(2026, 12, 28, 0, 0)
	b := ny(2026, 12, 29, 0, 0)
	setA := []Adjustment{{Date: a, Closed: true}}
	setB := []Adjustment{{Date: b, Closed: true}}
	var wg sync.WaitGroup
	stop := make(chan struct{})
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-stop:
					return
				default:
				}
				c.IsBusinessDay(a)
				c.IsHoliday(b)
				c.Holidays(a, b)
				c.NextClose(a)
			}
		}()
	}
	for i := 0; i < 200; i++ {
		if i%2 == 0 {
			require.NoError(t, SetAdjustments("xnys", setA))
		} else {
			require.NoError(t, SetAdjustments("xnys", setB))
		}
	}
	close(stop)
	wg.Wait()
}

// Adjustments are per calendar, even where two calendars share data.
func TestAdjustmentsDoNotLeakAcrossCalendars(t *testing.T) {
	resetAdjustments(t, "xnys")
	require.NoError(t, SetAdjustments("xnys", []Adjustment{{Date: ny(2026, 12, 28, 0, 0), Closed: true}}))
	assert.True(t, XNAS().IsBusinessDay(ny(2026, 12, 28, 0, 0)))
}

func TestUnadjustedIgnoresRuntimeAdjustments(t *testing.T) {
	resetAdjustments(t, "xnys")
	c := XNYS()
	day := ny(2026, 12, 28, 0, 0)
	xmas := ny(2026, 12, 25, 0, 0)
	require.NoError(t, SetAdjustments("xnys", []Adjustment{
		{Date: day, Closed: true, Name: "Day of mourning"},
		{Date: xmas, Open: ny(2026, 12, 25, 9, 30), Close: ny(2026, 12, 25, 16, 0)},
	}))
	base := c.Unadjusted()
	assert.False(t, c.IsBusinessDay(day))
	assert.True(t, base.IsBusinessDay(day), "the generated data says open")
	assert.True(t, base.IsHoliday(xmas))
	assert.False(t, c.IsHoliday(xmas))
	assert.Len(t, base.Holidays(ny(2026, 12, 1, 0, 0), ny(2026, 12, 31, 0, 0)), 1)
	assert.Equal(t, ny(2026, 12, 28, 16, 0), base.NextClose(day))
	assert.False(t, c.Unadjusted() == c, "a copy; c itself still honours adjustments")
	assert.False(t, c.IsBusinessDay(day))
}
