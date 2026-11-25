// Package schedule keeps plugins' scheduled jobs: when each is due, read
// from what the plugin asked for, and which are due now. Pure; times are
// Unix milliseconds, local time comes from the *time.Location passed in.
package schedule

import (
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"
)

// MinEveryMinutes is the shortest `every` a plugin may ask for.
const MinEveryMinutes = 15

// MissedAfterMS: a due time this late was missed (Textus wasn't running,
// or asleep), not just reached on the timer's next tick.
const MissedAfterMS = 2 * 60 * 1000

// Trigger is when a job is due: exactly one of its fields is set. Kept in
// schedule.json as {"at": ms}, {"every": {...}} or {"daily": {...}}.
type Trigger struct {
	// At is once, at this moment.
	At *int64 `json:"at,omitzero"`
	// Every is every so many minutes from the moment it was scheduled.
	Every *Every `json:"every,omitzero"`
	// Daily is every day at this local time.
	Daily *Daily `json:"daily,omitzero"`
}

// Every is a trigger every Minutes from Anchor.
type Every struct {
	Minutes uint32 `json:"minutes"`
	Anchor  int64  `json:"anchor"`
}

// Daily is a trigger at Hour:Minute local time.
type Daily struct {
	Hour   int `json:"hour"`
	Minute int `json:"minute"`
}

// CatchUp is what a time missed while Textus wasn't running becomes:
// "once" (the default, also when left out) or "skip".
type CatchUp string

const (
	// Once: a time missed fires once when Textus is next running.
	Once CatchUp = "once"
	// Skip: a time missed is let go.
	Skip CatchUp = "skip"
)

// MarshalJSON writes the default as "once".
func (c CatchUp) MarshalJSON() ([]byte, error) {
	if c == Skip {
		return []byte(`"skip"`), nil
	}
	return []byte(`"once"`), nil
}

// UnmarshalJSON takes "once" or "skip".
func (c *CatchUp) UnmarshalJSON(b []byte) error {
	switch string(b) {
	case `"once"`:
		*c = Once
	case `"skip"`:
		*c = Skip
	default:
		return fmt.Errorf("catchUp is \"once\" or \"skip\", not %s", b)
	}
	return nil
}

// ParseEvery reads "15m", "1h", "1h30m", "2d": minutes, at least
// MinEveryMinutes.
func ParseEvery(text string) (uint32, error) {
	bad := fmt.Errorf("every \"%s\" is not like \"15m\", \"1h\" or \"1h30m\".", text)
	var minutes uint64
	number := ""
	for _, c := range strings.TrimSpace(text) {
		if c >= '0' && c <= '9' {
			number += string(c)
			continue
		}
		var unit uint64
		switch c {
		case 'm':
			unit = 1
		case 'h':
			unit = 60
		case 'd':
			unit = 24 * 60
		default:
			return 0, bad
		}
		n, err := strconv.ParseUint(number, 10, 64)
		if err != nil {
			return 0, bad
		}
		minutes = saturatingAdd(minutes, saturatingMul(n, unit))
		number = ""
	}
	if number != "" || minutes == 0 || minutes > math.MaxUint32 {
		return 0, bad
	}
	if minutes < MinEveryMinutes {
		return 0, fmt.Errorf("every \"%s\" is under the shortest, %dm.", text, MinEveryMinutes)
	}
	return uint32(minutes), nil
}

func saturatingMul(a, b uint64) uint64 {
	if b != 0 && a > math.MaxUint64/b {
		return math.MaxUint64
	}
	return a * b
}

func saturatingAdd(a, b uint64) uint64 {
	if a > math.MaxUint64-b {
		return math.MaxUint64
	}
	return a + b
}

// ParseDaily reads "09:30": hour and minute.
func ParseDaily(text string) (int, int, error) {
	t, err := time.Parse("15:04", strings.TrimSpace(text))
	if err != nil {
		return 0, 0, fmt.Errorf("daily \"%s\" is not a time like \"09:30\".", text)
	}
	return t.Hour(), t.Minute(), nil
}

// ParseAt reads an RFC 3339 date and time ("2026-11-02T09:00:00-06:00"),
// or one without an offset, read as local time in loc.
func ParseAt(text string, loc *time.Location) (int64, error) {
	bad := fmt.Errorf("at \"%s\" is not a date and time like \"2026-11-02T09:00\".", text)
	trimmed := strings.TrimSpace(text)
	if at, err := time.Parse(time.RFC3339, trimmed); err == nil {
		return at.UnixMilli(), nil
	}
	for _, layout := range []string{"2006-01-02T15:04:05", "2006-01-02T15:04"} {
		wall, err := time.Parse(layout, trimmed)
		if err != nil {
			continue
		}
		if at, ok := firstValid(loc, wall); ok {
			return at, nil
		}
		return 0, bad
	}
	return 0, bad
}

// firstValid is the instant the local time wall names (its fields read in
// UTC): the earlier of two when clocks go back, and the first minute after
// the gap when they go forward.
func firstValid(loc *time.Location, wall time.Time) (int64, bool) {
	// A gap is at most a few hours anywhere.
	for minutes := 0; minutes <= 4*60; minutes++ {
		if at, ok := earliestAt(loc, wall.Add(time.Duration(minutes)*time.Minute)); ok {
			return at.UnixMilli(), true
		}
	}
	return 0, false
}

// earliestAt is the earliest instant whose clock in loc reads wall; false
// when the clocks skip it.
func earliestAt(loc *time.Location, wall time.Time) (time.Time, bool) {
	var best time.Time
	found := false
	seen := map[int]bool{}
	// Any instant reading wall is within 14 hours of it: the offsets in
	// effect around it are the only ones it can have.
	for h := -16; h <= 16; h++ {
		_, offset := wall.Add(time.Duration(h) * time.Hour).In(loc).Zone()
		if seen[offset] {
			continue
		}
		seen[offset] = true
		at := wall.Add(-time.Duration(offset) * time.Second)
		l := at.In(loc)
		reads := time.Date(l.Year(), l.Month(), l.Day(), l.Hour(), l.Minute(), l.Second(), 0, time.UTC)
		if reads.Equal(wall) && (!found || at.Before(best)) {
			best, found = at, true
		}
	}
	return best, found
}

// NextAfter is the first time the trigger is due strictly after after;
// false once an `at` has passed.
func NextAfter(t Trigger, after int64, loc *time.Location) (int64, bool) {
	switch {
	case t.At != nil:
		return *t.At, *t.At > after
	case t.Every != nil:
		step := int64(t.Every.Minutes) * 60_000
		if step <= 0 {
			return 0, false
		}
		anchor := t.Every.Anchor
		if after < anchor {
			return anchor + step, true
		}
		return anchor + ((after-anchor)/step+1)*step, true
	case t.Daily != nil:
		if t.Daily.Hour < 0 || t.Daily.Hour > 23 || t.Daily.Minute < 0 || t.Daily.Minute > 59 {
			return 0, false
		}
		y, m, d := time.UnixMilli(after).In(loc).Date()
		// Today's, else tomorrow's: a day later is always past after.
		for days := 0; days <= 2; days++ {
			wall := time.Date(y, m, d+days, t.Daily.Hour, t.Daily.Minute, 0, 0, time.UTC)
			if at, ok := firstValid(loc, wall); ok && at > after {
				return at, true
			}
		}
	}
	return 0, false
}

// OnDue is what a job whose due time due has come does, looked at now:
// whether it fires, and when it is due next (false: it is done).
func OnDue(t Trigger, catchUp CatchUp, due, now int64, loc *time.Location) (fire bool, next int64, again bool) {
	missed := now-due > MissedAfterMS
	fire = !missed || catchUp != Skip
	next, again = NextAfter(t, now, loc)
	return fire, next, again
}
