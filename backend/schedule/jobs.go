package schedule

import (
	"errors"
	"fmt"
	"maps"
	"slices"
	"strings"
	"time"
)

// MaxJobsPerPlugin is the most jobs one plugin may keep.
const MaxJobsPerPlugin = 32

// NotificationOpen is where a notification's click goes: a file, inside
// the folder the job or call came from, at a line. Mirrors NotificationOpen
// in src/platform/bindings/schedule.ts.
type NotificationOpen struct {
	Path string  `json:"path"`
	Line *uint32 `json:"line,omitzero"`
}

// NotificationSpec is a system notification as a plugin asks for it.
// Mirrors NotificationSpec in src/platform/bindings/schedule.ts.
type NotificationSpec struct {
	Title string            `json:"title"`
	Body  string            `json:"body"`
	Open  *NotificationOpen `json:"open,omitzero"`
}

// JobSpec is a job as a plugin asks for it: one of At, Every, Daily, and a
// notification, a handler of its background code, or both. Mirrors JobSpec
// in src/platform/bindings/schedule.ts.
type JobSpec struct {
	At      *string           `json:"at,omitzero"`
	Every   *string           `json:"every,omitzero"`
	Daily   *string           `json:"daily,omitzero"`
	CatchUp CatchUp           `json:"catchUp"`
	Notify  *NotificationSpec `json:"notify,omitzero"`
	Run     *string           `json:"run,omitzero"`
}

// Job is a job kept: what was asked, read, and where it stands.
type Job struct {
	Spec    JobSpec `json:"spec"`
	Trigger Trigger `json:"trigger"`
	// Root is the folder open when it was scheduled, for its files and
	// its notification's file (only with files.read or project.data).
	Root      *string `json:"root,omitzero"`
	NextDue   *int64  `json:"nextDue"`
	LastFired *int64  `json:"lastFired,omitzero"`
}

// ScheduledJob is a job as the plugin and Settings see it. Mirrors
// ScheduledJob in src/platform/bindings/schedule.ts.
type ScheduledJob struct {
	ID string `json:"id"`
	JobSpec
	// NextDue is Unix milliseconds; null once an `at` has fired.
	NextDue   *float64 `json:"nextDue"`
	LastFired *float64 `json:"lastFired"`
}

// Firing is a job that came due and fires.
type Firing struct {
	Plugin string
	ID     string
	Spec   JobSpec
	Root   *string
	Due    int64
}

// Store is plugins/schedule.json: every plugin's jobs, by id.
type Store struct {
	Plugins map[string]map[string]*Job `json:"plugins"`
}

// RequireJobID checks a job's id: what a plugin calls it, safe to keep and
// show.
func RequireJobID(id string) error {
	ok := id != "" && len(id) <= 64
	for _, c := range id {
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '-' || c == '_' || c == '.') {
			ok = false
		}
	}
	if !ok {
		return fmt.Errorf("A job id is 1 to 64 letters, digits, '-', '_' or '.', not \"%s\".", id)
	}
	return nil
}

// TriggerOf is the trigger spec names: exactly one of at, every, daily.
// now is when it was scheduled, which `every` counts from.
func TriggerOf(spec JobSpec, now int64, loc *time.Location) (Trigger, error) {
	named := 0
	for _, t := range []*string{spec.At, spec.Every, spec.Daily} {
		if t != nil {
			named++
		}
	}
	if named != 1 {
		return Trigger{}, errors.New("A job has one of at, every or daily.")
	}
	if spec.Notify == nil && spec.Run == nil {
		return Trigger{}, errors.New("A job has notify, run or both.")
	}
	if spec.Notify != nil && strings.TrimSpace(spec.Notify.Title) == "" {
		return Trigger{}, errors.New("A notification has a title.")
	}
	switch {
	case spec.At != nil:
		at, err := ParseAt(*spec.At, loc)
		if err != nil {
			return Trigger{}, err
		}
		return Trigger{At: &at}, nil
	case spec.Every != nil:
		minutes, err := ParseEvery(*spec.Every)
		if err != nil {
			return Trigger{}, err
		}
		return Trigger{Every: &Every{Minutes: minutes, Anchor: now}}, nil
	}
	hour, minute, err := ParseDaily(*spec.Daily)
	if err != nil {
		return Trigger{}, err
	}
	return Trigger{Daily: &Daily{Hour: hour, Minute: minute}}, nil
}

// Set adds or replaces a plugin's job. An `at` in the past fires at once
// with catchUp "once", and is refused with "skip".
func (s *Store) Set(plugin, id string, spec JobSpec, root *string, now int64, loc *time.Location) (ScheduledJob, error) {
	if err := RequireJobID(id); err != nil {
		return ScheduledJob{}, err
	}
	trigger, err := TriggerOf(spec, now, loc)
	if err != nil {
		return ScheduledJob{}, err
	}
	if spec.CatchUp != Skip {
		spec.CatchUp = Once
	}
	var nextDue *int64
	if trigger.At != nil && *trigger.At <= now {
		if spec.CatchUp == Skip {
			return ScheduledJob{}, errors.New("That time has passed.")
		}
		nextDue = &now
	} else if next, ok := NextAfter(trigger, now, loc); ok {
		nextDue = &next
	}
	jobs := s.Plugins[plugin]
	if _, ok := jobs[id]; !ok && len(jobs) >= MaxJobsPerPlugin {
		return ScheduledJob{}, fmt.Errorf("A plugin keeps at most %d jobs.", MaxJobsPerPlugin)
	}
	if jobs == nil {
		if s.Plugins == nil {
			s.Plugins = map[string]map[string]*Job{}
		}
		jobs = map[string]*Job{}
		s.Plugins[plugin] = jobs
	}
	job := &Job{Spec: spec, Trigger: trigger, Root: root, NextDue: nextDue}
	jobs[id] = job
	return scheduled(id, job), nil
}

// Clear takes one job away; whether it was there.
func (s *Store) Clear(plugin, id string) bool {
	jobs := s.Plugins[plugin]
	if _, ok := jobs[id]; !ok {
		return false
	}
	delete(jobs, id)
	if len(jobs) == 0 {
		delete(s.Plugins, plugin)
	}
	return true
}

// DropPlugin takes every job of a plugin away (it was uninstalled).
func (s *Store) DropPlugin(plugin string) bool {
	if _, ok := s.Plugins[plugin]; !ok {
		return false
	}
	delete(s.Plugins, plugin)
	return true
}

// List is a plugin's jobs, by id.
func (s *Store) List(plugin string) []ScheduledJob {
	jobs := s.Plugins[plugin]
	out := []ScheduledJob{}
	for _, id := range slices.Sorted(maps.Keys(jobs)) {
		out = append(out, scheduled(id, jobs[id]))
	}
	return out
}

// Earliest is the soonest due time among the plugins that run; a plugin
// turned off keeps its jobs, paused.
func (s *Store) Earliest(runs func(string) bool) (int64, bool) {
	var earliest int64
	found := false
	for plugin, jobs := range s.Plugins {
		if !runs(plugin) {
			continue
		}
		for _, j := range jobs {
			if j.NextDue != nil && (!found || *j.NextDue < earliest) {
				earliest, found = *j.NextDue, true
			}
		}
	}
	return earliest, found
}

// TakeDue moves the jobs of running plugins due by now on to their next
// time (or removes them when done): the ones that fire, by plugin and id,
// and whether any job was due, which changed the store.
func (s *Store) TakeDue(now int64, runs func(string) bool, loc *time.Location) ([]Firing, bool) {
	var firings []Firing
	changed := false
	for _, plugin := range slices.Sorted(maps.Keys(s.Plugins)) {
		if !runs(plugin) {
			continue
		}
		jobs := s.Plugins[plugin]
		for _, id := range slices.Sorted(maps.Keys(jobs)) {
			job := jobs[id]
			if job.NextDue == nil || *job.NextDue > now {
				continue
			}
			due := *job.NextDue
			changed = true
			fire, next, again := OnDue(job.Trigger, job.Spec.CatchUp, due, now, loc)
			if fire {
				fired := now
				job.LastFired = &fired
				firings = append(firings, Firing{Plugin: plugin, ID: id, Spec: job.Spec, Root: job.Root, Due: due})
			}
			if !again {
				delete(jobs, id)
				continue
			}
			job.NextDue = &next
		}
		if len(jobs) == 0 {
			delete(s.Plugins, plugin)
		}
	}
	return firings, changed
}

func scheduled(id string, job *Job) ScheduledJob {
	ms := func(t *int64) *float64 {
		if t == nil {
			return nil
		}
		f := float64(*t)
		return &f
	}
	return ScheduledJob{ID: id, JobSpec: job.Spec, NextDue: ms(job.NextDue), LastFired: ms(job.LastFired)}
}
