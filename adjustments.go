package calendar

import (
	"errors"
	"fmt"
	"sort"
	"time"
)

// Adjustment overrides one date of a calendar at runtime: an exchange closure,
// an extra session or changed hours announced too recently for the generated
// data. Install adjustments with SetAdjustments.
type Adjustment struct {
	// Date is the calendar date, read as written in its own location.
	Date time.Time
	// Closed makes the date a closure. Name labels it, as for a holiday.
	Closed bool
	Name   string
	// Open and Close are the session's instants when the date is not
	// Closed. A session may also have a break.
	Open, Close          time.Time
	BreakStart, BreakEnd time.Time
}

// adjustedView is a calendar's runtime adjustments and the holidays they
// produce. It is immutable once built; SetAdjustments swaps it atomically.
type adjustedView struct {
	byDay       map[day]adjusted
	holidays    map[day]string
	holidayDays []day
	list        []Adjustment
}

type adjusted struct {
	closed bool
	hours  hours
}

// SetAdjustments replaces the runtime adjustments of the calendar registered
// under code. They apply to every Calendar for that code, including values
// created earlier, and take precedence over the generated data. A nil or
// empty slice removes all adjustments. It is safe to call concurrently with
// queries; each query sees either the old or the new set.
func SetAdjustments(code string, adjustments []Adjustment) error {
	c := GetCalendar(code)
	if c == nil {
		return fmt.Errorf("calendar %q: unknown", code)
	}
	if len(adjustments) == 0 {
		c.d.adj.Store(nil)
		return nil
	}
	v, err := c.d.buildAdjusted(adjustments)
	if err != nil {
		return fmt.Errorf("calendar %q: %w", c.d.code, err)
	}
	c.d.adj.Store(v)
	return nil
}

// Adjustments returns the runtime adjustments installed for code, sorted by
// date.
func Adjustments(code string) []Adjustment {
	c := GetCalendar(code)
	if c == nil {
		return nil
	}
	v := c.d.adj.Load()
	if v == nil {
		return nil
	}
	return append([]Adjustment(nil), v.list...)
}

func (d *calData) buildAdjusted(adjustments []Adjustment) (*adjustedView, error) {
	v := &adjustedView{byDay: make(map[day]adjusted, len(adjustments)), holidays: make(map[day]string, len(d.holidays))}
	for k, name := range d.holidays {
		v.holidays[k] = name
	}
	for _, a := range adjustments {
		k := dayOf(a.Date)
		if k < d.first || k > d.last {
			return nil, fmt.Errorf("adjustment %s: %w (%s to %s)", k, ErrOutOfRange, d.first, d.last)
		}
		if _, dup := v.byDay[k]; dup {
			return nil, fmt.Errorf("adjustment %s: more than one for the date", k)
		}
		weekday := d.weekmask[k.date().Weekday()]
		if a.Closed {
			v.byDay[k] = adjusted{closed: true}
			if weekday {
				v.holidays[k] = a.Name
			}
			continue
		}
		h, err := d.adjustedHours(k, a)
		if err != nil {
			return nil, fmt.Errorf("adjustment %s: %w", k, err)
		}
		v.byDay[k] = adjusted{hours: h}
		delete(v.holidays, k)
	}
	for k := range v.holidays {
		v.holidayDays = append(v.holidayDays, k)
	}
	sort.Slice(v.holidayDays, func(i, j int) bool { return v.holidayDays[i] < v.holidayDays[j] })
	v.list = append([]Adjustment(nil), adjustments...)
	sort.Slice(v.list, func(i, j int) bool { return dayOf(v.list[i].Date) < dayOf(v.list[j].Date) })
	return v, nil
}

// adjustedHours converts an adjustment's instants to wall-clock offsets from
// the start of day k in the calendar's location.
func (d *calData) adjustedHours(k day, a Adjustment) (hours, error) {
	if a.Open.IsZero() || a.Close.IsZero() {
		return hours{}, errors.New("a session needs Open and Close")
	}
	if !a.Open.Before(a.Close) {
		return hours{}, errors.New("Open must be before Close")
	}
	h := hours{open: d.offset(k, a.Open), close: d.offset(k, a.Close)}
	if a.BreakStart.IsZero() != a.BreakEnd.IsZero() {
		return hours{}, errors.New("a break needs both BreakStart and BreakEnd")
	}
	if !a.BreakStart.IsZero() {
		if !(a.Open.Before(a.BreakStart) && a.BreakStart.Before(a.BreakEnd) && a.BreakEnd.Before(a.Close)) {
			return hours{}, errors.New("the break must fall inside the session")
		}
		h.breakStart, h.breakEnd, h.hasBreak = d.offset(k, a.BreakStart), d.offset(k, a.BreakEnd), true
	}
	return h, nil
}

func (d *calData) offset(k day, t time.Time) time.Duration {
	lt := t.In(d.loc)
	days := dayOf(lt) - k
	return time.Duration(days)*24*time.Hour +
		time.Duration(lt.Hour())*time.Hour + time.Duration(lt.Minute())*time.Minute + time.Duration(lt.Second())*time.Second
}

// effectiveHolidays returns the holidays in force: the generated ones, or
// those of the installed adjustments.
func (d *calData) effectiveHolidays() (map[day]string, []day) {
	if v := d.adj.Load(); v != nil {
		return v.holidays, v.holidayDays
	}
	return d.holidays, d.holidayDays
}
