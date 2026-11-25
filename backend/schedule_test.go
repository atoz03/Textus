package backend

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"textus/backend/plugins"
	"textus/backend/schedule"
)

// grants is a state with plugin a.b on and granted these.
func grants(granted ...string) plugins.State {
	s := plugins.NewState()
	s.Plugins["a.b"] = &plugins.Entry{Enabled: true, Granted: granted}
	return s
}

func jobSpec(open, run bool) schedule.JobSpec {
	spec := schedule.JobSpec{Daily: new("09:00"), Notify: &schedule.NotificationSpec{Title: "t"}}
	if open {
		spec.Notify.Open = &schedule.NotificationOpen{Path: "a.md"}
	}
	if run {
		spec.Run = new("tick")
	}
	return spec
}

func TestAJobNeedsNotificationsToNotifyAndBackgroundToRun(t *testing.T) {
	if requireJobGrants(grants(), "a.b", jobSpec(false, false), nil) == nil {
		t.Error("notified without notifications")
	}
	if err := requireJobGrants(grants("notifications"), "a.b", jobSpec(false, false), nil); err != nil {
		t.Error(err)
	}
	err := requireJobGrants(grants("notifications"), "a.b", jobSpec(false, true), nil)
	if err == nil || !strings.Contains(err.Error(), "background") {
		t.Errorf("ran without background: %v", err)
	}
	if err := requireJobGrants(grants("notifications", "background"), "a.b", jobSpec(false, true), nil); err != nil {
		t.Error(err)
	}
}

func TestANotificationThatOpensAFileNeedsFilesReadAndAFolder(t *testing.T) {
	folder := "/n"
	if requireJobGrants(grants("notifications"), "a.b", jobSpec(true, false), &folder) == nil {
		t.Error("opened a file without files.read")
	}
	if requireJobGrants(grants("notifications", "files.read"), "a.b", jobSpec(true, false), nil) == nil {
		t.Error("opened a file with no folder open")
	}
	if err := requireJobGrants(grants("notifications", "files.read"), "a.b", jobSpec(true, false), &folder); err != nil {
		t.Error(err)
	}
}

func TestAPluginTurnedOffSchedulesNothing(t *testing.T) {
	s := grants("notifications")
	s.AllOn = false
	if requireJobGrants(s, "a.b", jobSpec(false, false), nil) == nil {
		t.Error("scheduled while off")
	}
}

func TestNotificationIDsAreLongAndNeverRepeat(t *testing.T) {
	a, b := newNotificationID(), newNotificationID()
	if len(a) != 32 || a == b {
		t.Errorf("%q %q", a, b)
	}
}

func TestANotificationOpensOnlyAFileOfItsFolder(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "today.md"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	line := uint32(3)
	open := func(path string) schedule.NotificationOpen { return schedule.NotificationOpen{Path: path, Line: &line} }
	inside, err := resolveNotificationOpen(open("today.md"), &dir)
	if err != nil || !strings.HasSuffix(inside.Path, "today.md") || !filepath.IsAbs(inside.Path) || *inside.Line != 3 {
		t.Errorf("inside: %+v %v", inside, err)
	}
	for _, outside := range []string{"../elsewhere.md", "/etc/hosts"} {
		if _, err := resolveNotificationOpen(open(outside), &dir); err == nil {
			t.Errorf("%s opened", outside)
		}
	}
	if _, err := resolveNotificationOpen(open("today.md"), nil); err == nil {
		t.Error("opened with no folder")
	}
}

func TestAClickOpensWhatItsNotificationNamedAndOnlyTheLastFifty(t *testing.T) {
	a := pluginApp(t)
	for i := range keptTargets + 5 {
		a.keepTarget(newNotificationID(), schedule.NotificationOpen{Path: "/n/a.md"}, int64(i)+keptTargetsMS)
	}
	// A week old: let go.
	a.keepTarget("old", schedule.NotificationOpen{Path: "/n/old.md"}, 0)
	a.keepTarget("new", schedule.NotificationOpen{Path: "/n/new.md"}, keptTargetsMS+100)
	if len(a.notes.targets) > keptTargets {
		t.Errorf("%d kept", len(a.notes.targets))
	}
	// Read again after a restart, from the file.
	again := &App{dirs: a.dirs}
	again.notificationClicked("new")
	if open := again.takeNotificationOpen(); open == nil || open.Path != "/n/new.md" {
		t.Errorf("clicked: %+v", open)
	}
	if again.takeNotificationOpen() != nil {
		t.Error("taken twice")
	}
}

func TestAPluginsJobsAreSetListedRunAndCleared(t *testing.T) {
	a := pluginApp(t)
	if err := writePrettyJSON(a.pluginStatePath(), grants("notifications", "background", "files.read")); err != nil {
		t.Fatal(err)
	}
	a.startSchedule()
	if _, err := run(a, "schedule_set", `{"id":"a.b","jobId":"sync","job":{"every":"1h","run":"sync"},"root":"/notes"}`); err != nil {
		t.Fatal(err)
	}
	if _, err := run(a, "schedule_set", `{"id":"c.d","jobId":"x","job":{"every":"1h","run":"sync"},"root":null}`); err == nil {
		t.Error("a plugin that isn't on scheduled a job")
	}
	listed, err := run(a, "schedule_list", `{"id":"a.b"}`)
	if err != nil || !strings.Contains(listed, `"id":"sync"`) || !strings.Contains(listed, `"catchUp":"once"`) {
		t.Errorf("listed: %s %v", listed, err)
	}
	saved, _ := os.ReadFile(a.scheduleFile())
	if !strings.Contains(string(saved), `"root": "/notes"`) {
		t.Errorf("saved: %s", saved)
	}
	// A due run waits for a window.
	a.sched.runs = append(a.sched.runs, jobRun{Plugin: "a.b", Job: "sync", Run: "sync", Due: 1})
	if taken, _ := run(a, "schedule_take_runs", `{}`); taken != `[{"plugin":"a.b","job":"sync","run":"sync","root":null,"due":1}]` {
		t.Errorf("taken: %s", taken)
	}
	if taken, _ := run(a, "schedule_take_runs", `{}`); taken != `[]` {
		t.Errorf("taken twice: %s", taken)
	}
	if cleared, _ := run(a, "schedule_clear", `{"id":"a.b","jobId":"sync"}`); cleared != "true" {
		t.Errorf("cleared: %s", cleared)
	}
	if listed, _ := run(a, "schedule_list", `{"id":"a.b"}`); listed != `[]` {
		t.Errorf("after clearing: %s", listed)
	}
}
