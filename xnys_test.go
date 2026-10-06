package calendar

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

// Public sources for the NYSE spot checks below.
const (
	// NYSE Archives, "History of New York Stock Exchange Holidays" and "New
	// York Stock Exchange Special Closings, 1885-date" (rev. January 2011).
	srcNYSEClosings = "https://s3.amazonaws.com/armstrongeconomics-wp/2013/07/NYSE-Closings.pdf"
	// Uniform Monday Holiday Act, Pub. L. 90-363, 82 Stat. 250, effective 1971-01-01.
	srcMondayHolidayAct = "https://www.govinfo.gov/content/pkg/STATUTE-82/pdf/STATUTE-82-Pg250-3.pdf"
	// Proclamation 4176, national day of mourning for President Truman.
	srcTruman = "https://www.presidency.ucsb.edu/documents/proclamation-4176-announcing-the-death-harry-s-truman"
	// Proclamation 4180, national day of mourning for President Johnson.
	srcJohnson = "https://www.presidency.ucsb.edu/documents/proclamation-4180-announcing-the-death-lyndon-baines-johnson"
	// NYSE closure for President George H. W. Bush; Proclamation 9830 (83 FR 63039).
	srcBush     = "https://ir.theice.com/press/news-details/2018/New-York-Stock-Exchange-to-Honor-President-George-H-W-Bush/default.aspx"
	srcBushProc = "https://www.govinfo.gov/content/pkg/FR-2018-12-06/pdf/2018-26612.pdf"
	// NYSE closure for President Carter; Proclamation 10876 (90 FR 185).
	srcCarter     = "https://ir.theice.com/press/news-details/2024/The-New-York-Stock-Exchange-Will-Close-Markets-on-January-9-to-Honor-the-Passing-of-Former-President-Jimmy-Carter-on-National-Day-of-Mourning/default.aspx"
	srcCarterProc = "https://www.govinfo.gov/content/pkg/FR-2025-01-03/pdf/2024-31762.pdf"
)

// Dates where historical NYSE rules are easy to get wrong. Each row must hold
// in the generated data; each cites a public primary source.
func TestXNYSHistoricalSpotChecks(t *testing.T) {
	c := XNYS()
	for _, tc := range []struct {
		date   string
		open   bool
		why    string
		source string
	}{
		// Washington's Birthday and Memorial Day moved to Mondays in 1971.
		{"1970-02-16", true, "third Monday of February; the Monday rule starts in 1971", srcMondayHolidayAct},
		{"1970-02-23", false, "Washington's Birthday (Sun Feb 22) observed on Monday", srcNYSEClosings},
		{"1970-05-25", true, "last Monday of May; the Monday rule starts in 1971", srcMondayHolidayAct},
		// Presidential election days, closed through 1980.
		{"1972-11-07", false, "presidential election day", srcNYSEClosings},
		{"1976-11-02", false, "presidential election day", srcNYSEClosings},
		{"1980-11-04", false, "presidential election day", srcNYSEClosings},
		{"1984-11-06", true, "election days are sessions after 1980", srcNYSEClosings},
		// Special closings.
		{"1972-12-28", false, "national day of mourning, President Truman", srcTruman},
		{"1973-01-25", false, "national day of mourning, President Johnson", srcJohnson},
		{"1977-07-14", false, "New York City blackout", srcNYSEClosings},
		{"1985-09-27", false, "Hurricane Gloria", srcNYSEClosings},
		{"2018-11-30", true, "President G. H. W. Bush died this day; the closure was 5 December", srcBushProc},
		{"2018-12-05", false, "national day of mourning, President G. H. W. Bush", srcBush},
		{"2025-01-08", true, "day before the Carter closure", srcCarter},
		{"2025-01-09", false, "national day of mourning, President Carter", srcCarterProc},
		{"2025-01-10", true, "day after the Carter closure", srcCarter},
		// Martin Luther King Jr. Day: a full-day closure only from 1998.
		{"1997-01-20", true, "MLK Day before 1998", srcNYSEClosings},
		{"1998-01-19", false, "first full-day MLK Day closure", srcNYSEClosings},
	} {
		d, err := time.ParseInLocation(time.DateOnly, tc.date, NewYork)
		if err != nil {
			t.Fatal(err)
		}
		assert.Equal(t, tc.open, c.IsBusinessDay(d), "%s: %s (%s)", tc.date, tc.why, tc.source)
	}
}

// Every third Monday of January from 1970 through 1997 was a trading day.
func TestXNYSMLKDayTradedBefore1998(t *testing.T) {
	c := XNYS()
	for y := 1970; y <= 2035; y++ {
		mlk := NthWeekday(y, time.January, time.Monday, 3, NewYork)
		assert.Equal(t, y < 1998, c.IsBusinessDay(mlk), "%s (%s)", mlk.Format(time.DateOnly), srcNYSEClosings)
	}
}
