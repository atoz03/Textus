package schedule

import (
	"strings"
	"testing"
	"time"
	_ "time/tzdata"
)

// central is US Central time: in 2026, CDT from 8 March 08:00 UTC to
// 1 November 07:00 UTC, CST otherwise.
func central(t *testing.T) *time.Location {
	t.Helper()
	loc, err := time.LoadLocation("America/Chicago")
	if err != nil {
		t.Fatal(err)
	}
	return loc
}

func utc(y int, m time.Month, d, h, min int) int64 {
	return time.Date(y, m, d, h, min, 0, 0, time.UTC).UnixMilli()
}

func TestEveryReadsMinutesHoursAndDaysAndRefusesUnderFifteenMinutes(t *testing.T) {
	for text, want := range map[string]uint32{"15m": 15, "1h": 60, "1h30m": 90, "2d": 2 * 24 * 60} {
		if got, err := ParseEvery(text); err != nil || got != want {
			t.Errorf("ParseEvery(%q) = %d, %v; want %d", text, got, err, want)
		}
	}
	if _, err := ParseEvery("14m"); err == nil || !strings.Contains(err.Error(), "shortest") {
		t.Errorf("14m: %v", err)
	}
	for _, bad := range []string{"", "15", "m", "15s", "1.5h", "-15m", "0m"} {
		if _, err := ParseEvery(bad); err == nil {
			t.Errorf("ParseEvery(%q) passed", bad)
		}
	}
}

func TestDailyReadsATwentyFourHourTime(t *testing.T) {
	if h, m, err := ParseDaily("09:30"); err != nil || h != 9 || m != 30 {
		t.Errorf("09:30: %d %d %v", h, m, err)
	}
	if h, m, err := ParseDaily("23:59"); err != nil || h != 23 || m != 59 {
		t.Errorf("23:59: %d %d %v", h, m, err)
	}
	for _, bad := range []string{"24:00", "9", "9:30pm", "nine"} {
		if _, _, err := ParseDaily(bad); err == nil {
			t.Errorf("ParseDaily(%q) passed", bad)
		}
	}
}

func TestAtReadsAnOffsetOrLocalTime(t *testing.T) {
	loc := central(t)
	if got, err := ParseAt("2026-11-02T09:00:00-06:00", loc); err != nil || got != utc(2026, 11, 2, 15, 0) {
		t.Errorf("with an offset: %d, %v", got, err)
	}
	if got, err := ParseAt("2026-07-01T09:00", loc); err != nil || got != utc(2026, 7, 1, 14, 0) {
		t.Errorf("local: %d, %v", got, err)
	}
	if _, err := ParseAt("tomorrow", loc); err == nil {
		t.Error("tomorrow passed")
	}
}

func next(t *testing.T, tr Trigger, after int64, loc *time.Location) int64 {
	t.Helper()
	at, ok := NextAfter(tr, after, loc)
	if !ok {
		t.Fatalf("no time after %d", after)
	}
	return at
}

func TestEveryCountsFromWhenItWasScheduledWithoutDrifting(t *testing.T) {
	loc := central(t)
	anchor := utc(2026, 5, 1, 12, 0)
	every := Trigger{Every: &Every{Minutes: 15, Anchor: anchor}}
	if got := next(t, every, anchor, loc); got != anchor+15*60_000 {
		t.Errorf("first: %d", got)
	}
	// Fired a little late: the next is still on the anchor's beat.
	late := anchor + 15*60_000 + 40_000
	if got := next(t, every, late, loc); got != anchor+30*60_000 {
		t.Errorf("after a late one: %d", got)
	}
}

func TestDailyIsTodayUntilItsTimeThenTomorrow(t *testing.T) {
	loc := central(t)
	daily := Trigger{Daily: &Daily{Hour: 9}}
	// 08:00 CDT on 1 July: today's 09:00 CDT.
	if got := next(t, daily, utc(2026, 7, 1, 13, 0), loc); got != utc(2026, 7, 1, 14, 0) {
		t.Errorf("before: %d", got)
	}
	// At 09:00 exactly it is tomorrow's.
	if got := next(t, daily, utc(2026, 7, 1, 14, 0), loc); got != utc(2026, 7, 2, 14, 0) {
		t.Errorf("at: %d", got)
	}
}

func TestDailyKeepsItsLocalTimeAcrossDaylightSaving(t *testing.T) {
	loc := central(t)
	daily := Trigger{Daily: &Daily{Hour: 9}}
	// Saturday 31 October, 09:00 CDT, then Sunday 1 November, 09:00 CST:
	// 25 hours apart, both at nine on the clock.
	saturday := next(t, daily, utc(2026, 10, 31, 12, 0), loc)
	if saturday != utc(2026, 10, 31, 14, 0) {
		t.Errorf("saturday: %d", saturday)
	}
	if sunday := next(t, daily, saturday, loc); sunday != utc(2026, 11, 1, 15, 0) {
		t.Errorf("sunday: %d", sunday)
	}
	// And in spring, 23 hours apart.
	before := next(t, daily, utc(2026, 3, 7, 12, 0), loc)
	if before != utc(2026, 3, 7, 15, 0) {
		t.Errorf("before spring: %d", before)
	}
	if got := next(t, daily, before, loc); got != utc(2026, 3, 8, 14, 0) {
		t.Errorf("spring: %d", got)
	}
}

func TestADailyTimeTheClocksSkipFiresAfterTheGapAndARepeatedOneOnce(t *testing.T) {
	loc := central(t)
	// 02:30 doesn't exist on 8 March: 03:00 CDT instead.
	skipped := Trigger{Daily: &Daily{Hour: 2, Minute: 30}}
	if got := next(t, skipped, utc(2026, 3, 8, 0, 0), loc); got != utc(2026, 3, 8, 8, 0) {
		t.Errorf("skipped: %d", got)
	}
	// 01:30 happens twice on 1 November: the first, then the next day's.
	repeated := Trigger{Daily: &Daily{Hour: 1, Minute: 30}}
	first := next(t, repeated, utc(2026, 11, 1, 0, 0), loc)
	if first != utc(2026, 11, 1, 6, 30) {
		t.Errorf("repeated: %d", first)
	}
	if got := next(t, repeated, first, loc); got != utc(2026, 11, 2, 7, 30) {
		t.Errorf("the day after: %d", got)
	}
}

func TestAtIsDueOnce(t *testing.T) {
	loc := central(t)
	at := utc(2026, 7, 1, 14, 0)
	if got, ok := NextAfter(Trigger{At: &at}, at-1, loc); !ok || got != at {
		t.Errorf("before: %d %v", got, ok)
	}
	if _, ok := NextAfter(Trigger{At: &at}, at, loc); ok {
		t.Error("at it: still due")
	}
}

func TestATimeReachedOnTheTimerFiresWhateverCatchUpSays(t *testing.T) {
	loc := central(t)
	daily := Trigger{Daily: &Daily{Hour: 9}}
	due := utc(2026, 7, 1, 14, 0)
	for _, catchUp := range []CatchUp{Once, Skip} {
		fire, next, again := OnDue(daily, catchUp, due, due+30_000, loc)
		if !fire || !again || next != utc(2026, 7, 2, 14, 0) {
			t.Errorf("%s: %v %d %v", catchUp, fire, next, again)
		}
	}
}

func TestAMissedTimeFiresOnceOrIsSkippedAndTheNextIsAfterNow(t *testing.T) {
	loc := central(t)
	anchor := utc(2026, 7, 1, 12, 0)
	every := Trigger{Every: &Every{Minutes: 60, Anchor: anchor}}
	// Textus was closed for five hours: one catch-up, not five.
	due := anchor + 60*60_000
	now := anchor + 5*60*60_000 + 10*60_000
	if fire, next, again := OnDue(every, Once, due, now, loc); !fire || !again || next != anchor+6*60*60_000 {
		t.Errorf("once: %v %d %v", fire, next, again)
	}
	if fire, next, again := OnDue(every, Skip, due, now, loc); fire || !again || next != anchor+6*60*60_000 {
		t.Errorf("skip: %v %d %v", fire, next, again)
	}
	// A missed `at` is done either way.
	at := Trigger{At: &due}
	if fire, _, again := OnDue(at, Once, due, now, loc); !fire || again {
		t.Errorf("at, once: %v %v", fire, again)
	}
	if fire, _, again := OnDue(at, Skip, due, now, loc); fire || again {
		t.Errorf("at, skip: %v %v", fire, again)
	}
}
