package schedule

import (
	"encoding/json/v2"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"
)

var west6 = time.FixedZone("", -6*3600)

func at(h, m int) int64 { return utc(2026, 7, 1, h, m) }

func text(s string) *string { return &s }

func reminder(daily string) JobSpec {
	return JobSpec{Daily: text(daily), Notify: &NotificationSpec{Title: "Write"}}
}

func ids(firings []Firing) []string {
	out := []string{}
	for _, f := range firings {
		out = append(out, f.ID)
	}
	return out
}

func all(string) bool  { return true }
func none(string) bool { return false }

func TestAJobNamesOneTriggerAndSomethingToDo(t *testing.T) {
	var store Store
	both := reminder("09:00")
	both.Every = text("1h")
	if _, err := store.Set("a.b", "x", both, nil, at(12, 0), west6); err == nil {
		t.Error("two triggers passed")
	}
	nothing := JobSpec{Daily: text("09:00")}
	if _, err := store.Set("a.b", "x", nothing, nil, at(12, 0), west6); err == nil {
		t.Error("nothing to do passed")
	}
	if _, err := store.Set("a.b", "../x", reminder("09:00"), nil, at(12, 0), west6); err == nil {
		t.Error("a bad id passed")
	}
	job, err := store.Set("a.b", "morning", reminder("09:00"), nil, at(12, 0), west6)
	if err != nil {
		t.Fatal(err)
	}
	// 12:00 UTC is 06:00 here: due at 09:00 local, 15:00 UTC.
	if job.NextDue == nil || *job.NextDue != float64(at(15, 0)) {
		t.Errorf("next due: %v", job.NextDue)
	}
}

func TestAPluginKeepsABoundedNumberOfJobs(t *testing.T) {
	var store Store
	for i := range MaxJobsPerPlugin {
		if _, err := store.Set("a.b", fmt.Sprintf("j%d", i), reminder("09:00"), nil, at(12, 0), west6); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := store.Set("a.b", "one-more", reminder("09:00"), nil, at(12, 0), west6); err == nil {
		t.Error("one more passed")
	}
	// Replacing one is fine.
	if _, err := store.Set("a.b", "j0", reminder("10:00"), nil, at(12, 0), west6); err != nil {
		t.Error(err)
	}
}

func TestDueJobsFireAndMoveOnAndAFiredAtIsRemoved(t *testing.T) {
	var store Store
	if _, err := store.Set("a.b", "daily", reminder("09:00"), nil, at(12, 0), west6); err != nil {
		t.Fatal(err)
	}
	once := reminder("")
	once.Daily, once.At = nil, text("2026-07-01T09:30:00-06:00")
	if _, err := store.Set("a.b", "once", once, nil, at(12, 0), west6); err != nil {
		t.Fatal(err)
	}
	if e, ok := store.Earliest(all); !ok || e != at(15, 0) {
		t.Errorf("earliest: %d %v", e, ok)
	}
	if fired, changed := store.TakeDue(at(14, 59), all, west6); len(fired) != 0 || changed {
		t.Errorf("before: %v", ids(fired))
	}
	if fired, _ := store.TakeDue(at(15, 0), all, west6); !reflect.DeepEqual(ids(fired), []string{"daily"}) {
		t.Errorf("at 15:00: %v", ids(fired))
	}
	if fired, _ := store.TakeDue(at(15, 31), all, west6); !reflect.DeepEqual(ids(fired), []string{"once"}) {
		t.Errorf("at 15:31: %v", ids(fired))
	}
	left := store.List("a.b")
	if len(left) != 1 || left[0].LastFired == nil || *left[0].LastFired != float64(at(15, 0)) {
		t.Errorf("left: %+v", left)
	}
}

func TestAPausedPluginsJobsWaitAndCatchUpOnceWhenItRunsAgain(t *testing.T) {
	var store Store
	if _, err := store.Set("a.b", "daily", reminder("09:00"), nil, at(12, 0), west6); err != nil {
		t.Fatal(err)
	}
	// Turned off over 09:00: nothing fires, nothing moves.
	if fired, changed := store.TakeDue(at(16, 0), none, west6); len(fired) != 0 || changed {
		t.Error("a plugin turned off fired")
	}
	if _, ok := store.Earliest(none); ok {
		t.Error("a plugin turned off has a due time")
	}
	// On again at 10:00 local: the missed 09:00 fires once.
	if fired, _ := store.TakeDue(at(16, 0), all, west6); len(fired) != 1 {
		t.Errorf("caught up: %v", ids(fired))
	}
	if fired, _ := store.TakeDue(at(16, 1), all, west6); len(fired) != 0 {
		t.Errorf("twice: %v", ids(fired))
	}
}

func TestASkippedCatchUpLetsTheMissedTimeGo(t *testing.T) {
	var store Store
	spec := reminder("09:00")
	spec.CatchUp = Skip
	if _, err := store.Set("a.b", "daily", spec, nil, at(12, 0), west6); err != nil {
		t.Fatal(err)
	}
	if fired, _ := store.TakeDue(at(18, 0), all, west6); len(fired) != 0 {
		t.Errorf("fired: %v", ids(fired))
	}
	if e, ok := store.Earliest(all); !ok || e != at(15, 0)+24*3_600_000 {
		t.Errorf("earliest: %d", e)
	}
}

func TestAnAtInThePastFiresAtOnceOrIsRefusedWithSkip(t *testing.T) {
	var store Store
	past := reminder("")
	past.Daily, past.At = nil, text("2026-07-01T01:00:00-06:00")
	job, err := store.Set("a.b", "late", past, nil, at(12, 0), west6)
	if err != nil || job.NextDue == nil || *job.NextDue != float64(at(12, 0)) {
		t.Errorf("late: %+v %v", job, err)
	}
	past.CatchUp = Skip
	if _, err := store.Set("a.b", "late2", past, nil, at(12, 0), west6); err == nil {
		t.Error("a skipped past time passed")
	}
}

func TestTheStoreSurvivesARoundTripThroughItsFile(t *testing.T) {
	var store Store
	every := JobSpec{Every: text("15m"), Run: text("sync"), CatchUp: Skip}
	if _, err := store.Set("a.b", "sync", every, text("/notes"), at(12, 0), west6); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Set("c.d", "daily", reminder("21:30"), nil, at(12, 0), west6); err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(store, json.Deterministic(true))
	if err != nil {
		t.Fatal(err)
	}
	var back Store
	if err := json.Unmarshal(raw, &back); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(back, store) {
		t.Errorf("round trip:\n%s\n%+v", raw, back)
	}
	for _, want := range []string{`"catchUp":"skip"`, `"every":{"minutes":15,`, `"daily":{"hour":21,"minute":30}`, `"root":"/notes"`} {
		if !strings.Contains(string(raw), want) {
			t.Errorf("%s lacks %s", raw, want)
		}
	}
	// A missing catchUp is "once".
	var spec JobSpec
	if err := json.Unmarshal([]byte(`{"daily":"09:00"}`), &spec); err != nil || spec.CatchUp == Skip {
		t.Errorf("default catchUp: %q %v", spec.CatchUp, err)
	}
	if err := json.Unmarshal([]byte(`{"catchUp":"later"}`), &spec); err == nil {
		t.Error("an unknown catchUp passed")
	}
	// An empty file is no jobs.
	var empty Store
	if err := json.Unmarshal([]byte(`{}`), &empty); err != nil || len(empty.Plugins) != 0 {
		t.Errorf("empty: %+v %v", empty, err)
	}
}

func TestUninstallingOrClearingTakesJobsAway(t *testing.T) {
	var store Store
	for _, id := range []string{"x", "y"} {
		if _, err := store.Set("a.b", id, reminder("09:00"), nil, at(12, 0), west6); err != nil {
			t.Fatal(err)
		}
	}
	if !store.Clear("a.b", "x") || store.Clear("a.b", "x") {
		t.Error("clear")
	}
	if !store.DropPlugin("a.b") || len(store.List("a.b")) != 0 || len(store.Plugins) != 0 {
		t.Error("drop")
	}
}

func TestAListedJobReadsAsThePluginWroteIt(t *testing.T) {
	var store Store
	if _, err := store.Set("a.b", "x", reminder("09:00"), nil, at(12, 0), west6); err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(store.List("a.b")[0], json.Deterministic(true))
	if err != nil {
		t.Fatal(err)
	}
	var shown map[string]any
	if err := json.Unmarshal(raw, &shown); err != nil {
		t.Fatal(err)
	}
	want := map[string]any{
		"id": "x", "daily": "09:00", "catchUp": "once",
		"notify":  map[string]any{"title": "Write", "body": ""},
		"nextDue": float64(at(15, 0)), "lastFired": nil,
	}
	if !reflect.DeepEqual(shown, want) {
		t.Errorf("listed: %s", raw)
	}
}
