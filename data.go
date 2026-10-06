package calendar

import (
	"embed"
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

//go:embed data/*.json
var dataFS embed.FS

// day is a calendar date as days since 1970-01-01.
type day int64

func dayOf(t time.Time) day {
	y, m, d := t.Date()
	return day(time.Date(y, m, d, 0, 0, 0, 0, time.UTC).Unix() / 86400)
}

func (k day) date() time.Time { return time.Unix(int64(k)*86400, 0).UTC() }

func (k day) year() int { return k.date().Year() }

func (k day) String() string { return k.date().Format(time.DateOnly) }

// midnight returns the first instant of day k in loc. Where a DST change
// skips local midnight (e.g. Brazil before 2019), that is the first wall-clock
// time that exists, so the result still falls on day k.
func (k day) midnight(loc *time.Location) time.Time {
	y, m, d := k.date().Date()
	t := time.Date(y, m, d, 0, 0, 0, 0, loc)
	for h := 1; t.Day() != d && h < 24; h++ {
		t = time.Date(y, m, d, h, 0, 0, 0, loc)
	}
	return t
}

// wall returns the wall-clock time off after the start of day k in loc. off
// may be negative or exceed a day for sessions that span midnight.
func (k day) wall(off time.Duration, loc *time.Location) time.Time {
	days := day(off / (24 * time.Hour))
	rem := off % (24 * time.Hour)
	if rem < 0 {
		days--
		rem += 24 * time.Hour
	}
	y, m, d := (k + days).date().Date()
	return time.Date(y, m, d, int(rem/time.Hour), int(rem%time.Hour/time.Minute), int(rem%time.Minute/time.Second), 0, loc)
}

// hours are a session's times as wall-clock offsets from its date's midnight.
type hours struct {
	open, close          time.Duration
	breakStart, breakEnd time.Duration
	hasBreak             bool
}

func (s hours) hours(k day, loc *time.Location) SessionHours {
	h := SessionHours{Open: k.wall(s.open, loc), Close: k.wall(s.close, loc)}
	if s.hasBreak {
		h.BreakStart = k.wall(s.breakStart, loc)
		h.BreakEnd = k.wall(s.breakEnd, loc)
	}
	return h
}

type period struct {
	from       day
	hours      hours
	earlyClose time.Duration
}

type special struct {
	extra bool // a session on a day the weekmask closes
	hours hours
}

// calData is the parsed, immutable data of one exchange, shared by every
// Calendar value for that exchange.
type calData struct {
	code, name, source string
	loc                *time.Location
	weekmask           [7]bool // by time.Weekday
	first, last        day
	periods            []period
	pre, post          time.Duration
	holidays           map[day]string
	holidayDays        []day // sorted keys of holidays
	special            map[day]special
	sessions           int
	sha256             string
	adj                atomic.Pointer[adjustedView] // runtime adjustments; nil when none
}

func (d *calData) regular(k day) hours {
	i := sort.Search(len(d.periods), func(i int) bool { return d.periods[i].from > k }) - 1
	if i < 0 {
		i = 0
	}
	return d.periods[i].hours
}

// session returns the hours of the session on day k, if k is one. Runtime
// adjustments take precedence over the generated data.
func (d *calData) session(k day) (hours, bool) {
	if k < d.first || k > d.last {
		return hours{}, false
	}
	if v := d.adj.Load(); v != nil {
		if a, ok := v.byDay[k]; ok {
			return a.hours, !a.closed
		}
	}
	sp, isSpecial := d.special[k]
	if isSpecial && sp.extra {
		return sp.hours, true
	}
	if !d.weekmask[k.date().Weekday()] {
		return hours{}, false
	}
	if _, closed := d.holidays[k]; closed {
		return hours{}, false
	}
	if isSpecial {
		return sp.hours, true
	}
	return d.regular(k), true
}

func holidayIndex(days []day, k day) int {
	return sort.Search(len(days), func(i int) bool { return days[i] >= k })
}

// --------------------------------------------------------------------------
// Loading

type rawData struct {
	Code     string              `json:"code"`
	Name     string              `json:"name"`
	Source   string              `json:"source"`
	TZ       string              `json:"tz"`
	Weekmask string              `json:"weekmask"`
	First    string              `json:"first"`
	Last     string              `json:"last"`
	Regular  []map[string]string `json:"regular"`
	Extended map[string]string   `json:"extended"`
	Holidays map[string][]string `json:"holidays"`
	Special  map[string][]string `json:"special"`
	Sessions int                 `json:"sessions"`
	SHA256   string              `json:"sha256"`
}

type entry struct {
	once sync.Once
	data *calData
	err  error
}

var cache sync.Map // code -> *entry

// load returns a new Calendar for code. The data is parsed once per process.
// It panics only if the embedded data is corrupt or the time zone database
// lacks the exchange's zone (import time/tzdata to embed one).
func load(code string) *Calendar {
	v, _ := cache.LoadOrStore(code, &entry{})
	e := v.(*entry)
	e.once.Do(func() {
		e.data, e.err = parse(code)
	})
	if e.err != nil {
		panic(fmt.Sprintf("calendar: load %s: %v", code, e.err))
	}
	return newCalendar(e.data)
}

func newCalendar(d *calData) *Calendar {
	c := &Calendar{Name: d.name, Loc: d.loc, d: d}
	k := dayOf(time.Now().In(d.loc))
	if k < d.first {
		k = d.first
	}
	if k > d.last {
		k = d.last
	}
	r := d.regular(k)
	s := &Session{Open: r.open, Close: r.close, EarlyOpen: d.pre, LateClose: d.post}
	if r.hasBreak {
		s.BreakStart, s.BreakStop = r.breakStart, r.breakEnd
	}
	for i := len(d.periods) - 1; i >= 0; i-- {
		if d.periods[i].from <= k && d.periods[i].earlyClose != 0 {
			s.EarlyClose = d.periods[i].earlyClose
			break
		}
	}
	c.session = s
	return c
}

func parse(code string) (*calData, error) {
	b, err := dataFS.ReadFile("data/" + code + ".json")
	if err != nil {
		return nil, err
	}
	return parseData(b)
}

func parseData(b []byte) (*calData, error) {
	var err error
	var r rawData
	if err := json.Unmarshal(b, &r); err != nil {
		return nil, err
	}
	d := &calData{
		code: r.Code, name: r.Name, source: r.Source,
		holidays: make(map[day]string), special: make(map[day]special),
		sessions: r.Sessions, sha256: r.SHA256,
	}
	if d.loc, err = time.LoadLocation(r.TZ); err != nil {
		return nil, err
	}
	if len(r.Weekmask) != 7 {
		return nil, fmt.Errorf("bad weekmask %q", r.Weekmask)
	}
	for i, ch := range r.Weekmask { // Monday first
		d.weekmask[(i+1)%7] = ch == '1'
	}
	if d.first, err = parseDay(r.First); err != nil {
		return nil, err
	}
	if d.last, err = parseDay(r.Last); err != nil {
		return nil, err
	}
	if d.pre, err = parseOptDuration(r.Extended["pre"]); err != nil {
		return nil, err
	}
	if d.post, err = parseOptDuration(r.Extended["post"]); err != nil {
		return nil, err
	}
	for _, p := range r.Regular {
		from, err := parseDay(p["from"])
		if err != nil {
			return nil, err
		}
		var h hours
		if err := h.set(p); err != nil {
			return nil, err
		}
		ec, err := parseOptDuration(p["early_close"])
		if err != nil {
			return nil, err
		}
		d.periods = append(d.periods, period{from: from, hours: h, earlyClose: ec})
	}
	if len(d.periods) == 0 {
		return nil, fmt.Errorf("no regular hours")
	}
	for year, list := range r.Holidays {
		for _, e := range list {
			k, rest, err := parseEntry(year, e)
			if err != nil {
				return nil, err
			}
			d.holidays[k] = rest
			d.holidayDays = append(d.holidayDays, k)
		}
	}
	sort.Slice(d.holidayDays, func(i, j int) bool { return d.holidayDays[i] < d.holidayDays[j] })
	for year, list := range r.Special {
		for _, e := range list {
			k, rest, err := parseEntry(year, e)
			if err != nil {
				return nil, err
			}
			sp := special{hours: d.regular(k)}
			fields := map[string]string{}
			for _, f := range strings.Fields(rest) {
				if f == "extra" {
					sp.extra = true
					continue
				}
				name, val, ok := strings.Cut(f, "=")
				if !ok {
					return nil, fmt.Errorf("special %s: bad field %q", k, f)
				}
				fields[name] = val
			}
			if err := sp.hours.set(fields); err != nil {
				return nil, fmt.Errorf("special %s: %w", k, err)
			}
			d.special[k] = sp
		}
	}
	return d, nil
}

// set applies the time fields present in f; "-" clears a field.
func (h *hours) set(f map[string]string) error {
	for name, dst := range map[string]*time.Duration{
		"open": &h.open, "close": &h.close, "break_start": &h.breakStart, "break_end": &h.breakEnd,
	} {
		v, ok := f[name]
		if !ok {
			continue
		}
		if v == "-" {
			*dst = 0
			if name == "break_start" {
				h.hasBreak = false
			}
			continue
		}
		dur, err := time.ParseDuration(v)
		if err != nil {
			return err
		}
		*dst = dur
		if name == "break_start" {
			h.hasBreak = true
		}
	}
	return nil
}

func parseDay(s string) (day, error) {
	t, err := time.Parse(time.DateOnly, s)
	if err != nil {
		return 0, err
	}
	return dayOf(t), nil
}

func parseOptDuration(s string) (time.Duration, error) {
	if s == "" {
		return 0, nil
	}
	return time.ParseDuration(s)
}

// parseEntry splits a "MM-DD rest" entry filed under year.
func parseEntry(year, e string) (day, string, error) {
	if _, err := strconv.Atoi(year); err != nil || len(e) < 5 {
		return 0, "", fmt.Errorf("bad entry %s %q", year, e)
	}
	k, err := parseDay(year + "-" + e[:5])
	if err != nil {
		return 0, "", err
	}
	return k, strings.TrimSpace(e[5:]), nil
}

// GetCalendar returns the calendar registered under name (a MIC such as
// "xnys", case-insensitive), or nil if there is none. Year arguments are
// accepted for compatibility with scmhub/calendar and ignored.
func GetCalendar(name string, years ...int) *Calendar {
	name = strings.ToLower(name)
	for _, n := range registryNames {
		if n == name {
			return load(n)
		}
	}
	return nil
}

// Names returns the codes of all available calendars.
func Names() []string {
	return append([]string(nil), registryNames...)
}
