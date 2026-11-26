package backend

import (
	"context"
	"log"
	"path/filepath"
	"sync"
	"time"

	"github.com/egoist/mygo"

	"textus/backend/plugins"
	"textus/backend/schedule"
)

// Plugins' scheduled jobs, kept in plugins/schedule.json so they outlive a
// restart, and one timer for the soonest. A due job shows its notification
// from here and hands its run to a window's plugins, or, with no window
// open, to a hidden one opened for it and closed once it has run them.

const (
	scheduleFile = "schedule.json"
	// longestSleep: the timer looks at the wall clock at least this often,
	// as the clock it sleeps by stops while the computer sleeps.
	longestSleep = 60 * time.Second
	// backgroundHostLabel is the hidden window that runs plugins'
	// background code when no window is open.
	backgroundHostLabel = "background-host"
	// hostLifetime: the hidden window is closed this long after it opened,
	// whatever it is doing: each run has 30 s, and a page that never
	// reports back can't stay.
	hostLifetime = 120 * time.Second
	// eventScheduleRun tells an editor window runs are waiting
	// (schedule_take_runs).
	eventScheduleRun = "schedule-run"
)

// jobRun is one handler of a plugin's background code to run. Mirrors
// JobRun in src/platform/bindings/schedule.ts.
type jobRun struct {
	Plugin string `json:"plugin"`
	// Job is the job's id.
	Job string `json:"job"`
	// Run is the function of the background code that runs.
	Run string `json:"run"`
	// Root is the folder its files are in, if it was granted files.read
	// or project.data.
	Root *string `json:"root"`
	// Due is when it was due, Unix milliseconds.
	Due float64 `json:"due"`
}

// scheduler is the jobs, the timer's wake-up and the runs waiting for a
// window.
type scheduler struct {
	mu      sync.Mutex
	store   schedule.Store
	started bool
	wake    chan struct{}
	runs    []jobRun
	// hostGeneration says which hidden window the closing timer is for.
	hostGeneration uint32
}

func (a *App) scheduleFile() string { return filepath.Join(a.pluginsRoot(), scheduleFile) }

func nowMS() int64 { return time.Now().UnixMilli() }

// startSchedule reads the jobs and starts the timer; jobs missed while
// Textus wasn't running come due on its first look.
func (a *App) startSchedule() {
	store, err := readJSONStore[schedule.Store](a.scheduleFile())
	if err != nil {
		log.Printf("textus: schedule: %v", err)
	}
	a.sched.mu.Lock()
	a.sched.store = store
	a.sched.wake = make(chan struct{}, 1)
	a.sched.started = true
	a.sched.mu.Unlock()
	go a.runSchedule()
}

func (a *App) runSchedule() {
	for {
		a.fireDue()
		runs := a.pluginState().Runs
		a.sched.mu.Lock()
		due, ok := a.sched.store.Earliest(runs)
		wake := a.sched.wake
		a.sched.mu.Unlock()
		if !ok {
			<-wake
			continue
		}
		timer := time.NewTimer(min(max(time.Duration(due-nowMS())*time.Millisecond, 0), longestSleep))
		select {
		case <-timer.C:
		case <-wake:
			timer.Stop()
		}
	}
}

// saveSchedule writes the jobs; the caller holds a.sched.mu.
func (a *App) saveSchedule() {
	if err := writePrettyJSON(a.scheduleFile(), a.sched.store); err != nil {
		log.Printf("textus: schedule: could not save the jobs: %v", err)
	}
}

// changeSchedule changes the jobs, saves them and has the timer look again.
func changeSchedule[T any](a *App, change func(s *schedule.Store) T) T {
	a.sched.mu.Lock()
	out := change(&a.sched.store)
	a.saveSchedule()
	a.sched.mu.Unlock()
	a.rearmSchedule()
	return out
}

// rearmSchedule has the timer look at the jobs again: a plugin switched on
// again may have jobs due.
func (a *App) rearmSchedule() {
	a.sched.mu.Lock()
	wake := a.sched.wake
	a.sched.mu.Unlock()
	if wake == nil {
		return
	}
	select {
	case wake <- struct{}{}:
	default:
	}
}

// forgetPluginJobs drops an uninstalled plugin's jobs.
func (a *App) forgetPluginJobs(id string) {
	a.sched.mu.Lock()
	started := a.sched.started
	a.sched.mu.Unlock()
	if started {
		changeSchedule(a, func(s *schedule.Store) bool { return s.DropPlugin(id) })
	}
}

func (a *App) fireDue() {
	state := a.pluginState()
	a.sched.mu.Lock()
	firings, changed := a.sched.store.TakeDue(nowMS(), state.Runs, time.Local)
	if changed {
		a.saveSchedule()
	}
	a.sched.mu.Unlock()
	for _, f := range firings {
		a.fire(state, f)
	}
}

func (a *App) fire(state plugins.State, f schedule.Firing) {
	if n := f.Spec.Notify; n != nil {
		// macOS asks the user the first time: the timer doesn't wait.
		go func() {
			if err := a.showNotification(context.Background(), f.Plugin, *n, f.Root); err != nil {
				log.Printf("textus: schedule: %s %s: %v", f.Plugin, f.ID, err)
			}
		}()
	}
	if f.Spec.Run != nil && state.Allows(f.Plugin, "background") {
		a.queueRun(jobRun{Plugin: f.Plugin, Job: f.ID, Run: *f.Spec.Run, Root: f.Root, Due: float64(f.Due)})
	}
}

// queueRun keeps a run for a window: the editor window in front is told,
// or with none open a hidden one opens for it.
func (a *App) queueRun(run jobRun) {
	a.sched.mu.Lock()
	a.sched.runs = append(a.sched.runs, run)
	a.sched.mu.Unlock()
	if label := a.focusedEditorWindow(""); label != "" {
		_ = a.emitTo(label, eventScheduleRun, nil)
		return
	}
	a.openBackgroundHost()
}

// openBackgroundHost opens the hidden window, unless it is open or the app
// is quitting, which it would hold up.
func (a *App) openBackgroundHost() {
	if a.window(backgroundHostLabel) != nil || a.quitting.Load() || a.exiting.Load() {
		return
	}
	_, err := a.openWindow(windowSpec{
		label: backgroundHostLabel, url: "/index.html?view=background", title: "Textus",
		width: 320, height: 240,
		tweak: func(o *mygo.WindowOptions) {
			o.Hidden = true
			o.SkipTaskbar = true
		},
	})
	if err != nil {
		log.Printf("textus: schedule: could not open the background window: %v", err)
		return
	}
	a.sched.mu.Lock()
	a.sched.hostGeneration++
	generation := a.sched.hostGeneration
	a.sched.mu.Unlock()
	time.AfterFunc(hostLifetime, func() {
		a.sched.mu.Lock()
		current := a.sched.hostGeneration
		a.sched.mu.Unlock()
		if current == generation {
			a.closeBackgroundHost()
		}
	})
}

func (a *App) closeBackgroundHost() {
	if w := a.window(backgroundHostLabel); w != nil {
		w.destroy()
	}
}

// requireJobGrants is what a job needs granted, beyond the plugin being on.
func requireJobGrants(s plugins.State, id string, spec schedule.JobSpec, root *string) error {
	if !s.Runs(id) {
		return plugins.TurnedOff(id)
	}
	var needs []string
	if n := spec.Notify; n != nil {
		needs = append(needs, "notifications")
		if n.Open != nil {
			if root == nil {
				return errorf("A notification opens a file of the open folder; none is open.")
			}
			needs = append(needs, "files.read")
		}
	}
	if spec.Run != nil {
		needs = append(needs, "background")
	}
	for _, p := range needs {
		if !s.Allows(id, p) {
			return plugins.Refused(id, p)
		}
	}
	return nil
}

func init() {
	// textus.schedule.set: adds or replaces one of the plugin's jobs. Root,
	// the folder open in the window, is kept only with files.read or
	// project.data.
	command("schedule_set", func(c *Call, a struct {
		ID    string           `json:"id"`
		JobID string           `json:"jobId"`
		Job   schedule.JobSpec `json:"job"`
		Root  *string          `json:"root"`
	}) (schedule.ScheduledJob, error) {
		s := c.app.pluginState()
		// Checked with the folder as it is, so a missing files.read is
		// named as such rather than as no folder open.
		if err := requireJobGrants(s, a.ID, a.Job, a.Root); err != nil {
			return schedule.ScheduledJob{}, err
		}
		// The folder its run works in: its files, or its project data.
		root := a.Root
		if !s.Allows(a.ID, "files.read") && !s.Allows(a.ID, "project.data") {
			root = nil
		}
		type result struct {
			job schedule.ScheduledJob
			err error
		}
		r := changeSchedule(c.app, func(store *schedule.Store) result {
			job, err := store.Set(a.ID, a.JobID, a.Job, root, nowMS(), time.Local)
			return result{job, err}
		})
		return r.job, r.err
	})
	command("schedule_clear", func(c *Call, a struct {
		ID    string `json:"id"`
		JobID string `json:"jobId"`
	}) (bool, error) {
		return changeSchedule(c.app, func(s *schedule.Store) bool { return s.Clear(a.ID, a.JobID) }), nil
	})
	// A plugin's jobs, for the plugin and for its page in Settings.
	command("schedule_list", func(c *Call, a struct {
		ID string `json:"id"`
	}) ([]schedule.ScheduledJob, error) {
		c.app.sched.mu.Lock()
		defer c.app.sched.mu.Unlock()
		return c.app.sched.store.List(a.ID), nil
	})
	// The runs waiting, taken by the window that runs them.
	command("schedule_take_runs", func(c *Call, _ none) ([]jobRun, error) {
		c.app.sched.mu.Lock()
		defer c.app.sched.mu.Unlock()
		runs := c.app.sched.runs
		c.app.sched.runs = nil
		if runs == nil {
			runs = []jobRun{}
		}
		return runs, nil
	})
	// The hidden window ran everything it took: it closes, unless more
	// came.
	command("background_host_idle", func(c *Call, _ none) (nothing, error) {
		c.app.sched.mu.Lock()
		waiting := len(c.app.sched.runs) > 0
		c.app.sched.mu.Unlock()
		if !waiting {
			c.app.closeBackgroundHost()
		}
		return nil, nil
	})
	// textus.notify.show, from a window's plugin host or a background run.
	command("notify_show", func(c *Call, a struct {
		ID           string                    `json:"id"`
		Notification schedule.NotificationSpec `json:"notification"`
		Root         *string                   `json:"root"`
	}) (nothing, error) {
		return nil, c.app.showNotification(c.ctx, a.ID, a.Notification, a.Root)
	})
	// The file a clicked notification opens, taken by the window that
	// shows it.
	command("notify_take_open", func(c *Call, _ none) (*schedule.NotificationOpen, error) {
		return c.app.takeNotificationOpen(), nil
	})
}
