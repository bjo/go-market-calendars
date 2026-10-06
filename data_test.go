package calendar

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os/exec"
	"strconv"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// scheduleDigest mirrors canonical_digest in gen/generate.py: one line per
// session with wall-clock offsets in seconds from the session date's midnight.
func scheduleDigest(d *calData) (string, int) {
	off := func(v time.Duration, ok bool) string {
		if !ok {
			return "-"
		}
		return strconv.FormatInt(int64(v/time.Second), 10)
	}
	h := sha256.New()
	n := 0
	for k := d.first; k <= d.last; k++ {
		s, ok := d.session(k)
		if !ok {
			continue
		}
		n++
		fmt.Fprintf(h, "%s %s %s %s %s\n", k, off(s.open, true), off(s.close, true),
			off(s.breakStart, s.hasBreak), off(s.breakEnd, s.hasBreak))
	}
	return hex.EncodeToString(h.Sum(nil)), n
}

// The runtime must rebuild exactly the schedule the generator read from
// pandas_market_calendars: every session date and every open, close and break.
func TestScheduleMatchesGeneratedDigest(t *testing.T) {
	for _, code := range Names() {
		t.Run(code, func(t *testing.T) {
			c := GetCalendar(code)
			require.NotNil(t, c)
			digest, n := scheduleDigest(c.d)
			assert.Equal(t, c.d.sessions, n, "session count")
			assert.Equal(t, c.d.sha256, digest, "schedule digest")
		})
	}
}

// Regenerating from the pinned pandas_market_calendars must reproduce the
// committed data byte for byte. Needs uv; skipped in -short mode or without it.
func TestDataMatchesPandasMarketCalendars(t *testing.T) {
	if testing.Short() {
		t.Skip("regeneration takes ~30s; skipped in -short mode")
	}
	uv, err := exec.LookPath("uv")
	if err != nil {
		t.Skip("uv not installed; run `uv run --locked --project gen gen/generate.py --check` to check drift")
	}
	out, err := exec.Command(uv, "run", "--locked", "--project", "gen", "gen/generate.py", "--check").CombinedOutput()
	require.NoError(t, err, "generated data drifted from pandas_market_calendars:\n%s", out)
}

func TestEveryCalendarLoads(t *testing.T) {
	assert.Len(t, Names(), len(registryNames))
	for _, code := range Names() {
		c := GetCalendar(code)
		require.NotNil(t, c, code)
		assert.Equal(t, code, c.Code())
		assert.NotNil(t, c.Loc, code)
		assert.False(t, c.Session().IsZero(), code)
		first, last := c.Range()
		assert.True(t, first.Before(last), code)
	}
	assert.Nil(t, GetCalendar("nope"))
	assert.Equal(t, "xnys", GetCalendar("XNYS").Code())
}
