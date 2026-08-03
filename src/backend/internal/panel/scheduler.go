package panel

import (
	"context"
	"errors"
	"log"
	"sort"
	"sync"
	"time"
)

// inspectionSchedule supports fixed intervals and calendar-aligned checks.
// At is used by day/month/year schedules and is interpreted in the panel timezone.
type inspectionSchedule struct {
	Every int    `json:"every"`
	Unit  string `json:"unit"` // minute|hour|day|month|year
	At    string `json:"at,omitempty"`
}

func (s inspectionSchedule) validate() error {
	if s.Every < 1 || s.Every > 10000 {
		return errors.New("巡检间隔须为 1-10000")
	}
	switch s.Unit {
	case "minute", "hour":
		return nil
	case "day", "month", "year":
		if _, err := time.Parse("15:04", s.At); err != nil {
			return errors.New("巡检执行时间须形如 HH:MM")
		}
		return nil
	default:
		return errors.New("巡检频率单位须为 minute、hour、day、month 或 year")
	}
}

func (s inspectionSchedule) next(after time.Time, loc *time.Location) time.Time {
	after = after.In(loc)
	switch s.Unit {
	case "minute":
		return after.Add(time.Duration(s.Every) * time.Minute)
	case "hour":
		return after.Add(time.Duration(s.Every) * time.Hour)
	}
	parsed, _ := time.Parse("15:04", s.At)
	candidate := time.Date(after.Year(), after.Month(), after.Day(), parsed.Hour(), parsed.Minute(), 0, 0, loc)
	if candidate.After(after) {
		return candidate
	}
	switch s.Unit {
	case "month":
		return candidate.AddDate(0, s.Every, 0)
	case "year":
		return candidate.AddDate(s.Every, 0, 0)
	default:
		return candidate.AddDate(0, 0, s.Every)
	}
}

type taskTrigger interface {
	next(time.Time, *time.Location) time.Time
}

type intervalTrigger time.Duration

func (i intervalTrigger) next(after time.Time, _ *time.Location) time.Time {
	return after.Add(time.Duration(i))
}

type scheduledTask struct {
	name       string
	trigger    func(context.Context) taskTrigger
	runOnStart bool
	timeout    time.Duration
	run        func(context.Context) error
}

type taskResult struct {
	name     string
	started  time.Time
	finished time.Time
	err      error
}

type scheduledTaskStatus struct {
	Name           string     `json:"name"`
	Running        bool       `json:"running"`
	Runs           uint64     `json:"runs"`
	LastStartedAt  *time.Time `json:"last_started_at,omitempty"`
	LastFinishedAt *time.Time `json:"last_finished_at,omitempty"`
	LastDurationMS int64      `json:"last_duration_ms"`
	LastError      string     `json:"last_error,omitempty"`
	NextRunAt      *time.Time `json:"next_run_at,omitempty"`
}

// taskScheduler owns all panel-side recurring work. Tasks share lifecycle and
// wake-up handling while still running independently and never overlapping themselves.
type taskScheduler struct {
	location func(context.Context) *time.Location

	mu      sync.Mutex
	tasks   map[string]scheduledTask
	status  map[string]scheduledTaskStatus
	changed chan struct{}
	workers sync.WaitGroup
}

func newTaskScheduler(location func(context.Context) *time.Location) *taskScheduler {
	return &taskScheduler{
		location: location,
		tasks:    make(map[string]scheduledTask),
		status:   make(map[string]scheduledTaskStatus),
		changed:  make(chan struct{}, 1),
	}
}

func (s *taskScheduler) register(task scheduledTask) {
	if task.name == "" || task.trigger == nil || task.run == nil {
		panic("panel: invalid scheduled task")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, exists := s.tasks[task.name]; exists {
		panic("panel: duplicate scheduled task " + task.name)
	}
	s.tasks[task.name] = task
	s.status[task.name] = scheduledTaskStatus{Name: task.name}
}

func (s *taskScheduler) notifyChanged() {
	select {
	case s.changed <- struct{}{}:
	default:
	}
}

func (s *taskScheduler) snapshot() map[string]scheduledTask {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make(map[string]scheduledTask, len(s.tasks))
	for name, task := range s.tasks {
		out[name] = task
	}
	return out
}

func (s *taskScheduler) statusSnapshot() []scheduledTaskStatus {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]scheduledTaskStatus, 0, len(s.status))
	for _, status := range s.status {
		out = append(out, status)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

func (s *taskScheduler) setNextRun(name string, next time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()
	status := s.status[name]
	status.NextRunAt = &next
	s.status[name] = status
}

func (s *taskScheduler) markStarted(name string, started time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()
	status := s.status[name]
	status.Running = true
	status.LastStartedAt = &started
	status.NextRunAt = nil
	s.status[name] = status
}

func (s *taskScheduler) markFinished(result taskResult, next time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()
	status := s.status[result.name]
	status.Running = false
	status.Runs++
	status.LastStartedAt = &result.started
	status.LastFinishedAt = &result.finished
	status.LastDurationMS = result.finished.Sub(result.started).Milliseconds()
	status.LastError = ""
	if result.err != nil {
		status.LastError = result.err.Error()
	}
	status.NextRunAt = &next
	s.status[result.name] = status
}

func (s *taskScheduler) run(ctx context.Context) {
	tasks := s.snapshot()
	now := time.Now()
	next := make(map[string]time.Time, len(tasks))
	running := make(map[string]bool, len(tasks))
	results := make(chan taskResult, len(tasks))
	for name, task := range tasks {
		if task.runOnStart {
			next[name] = now
		} else {
			next[name] = task.trigger(ctx).next(now, s.location(ctx))
		}
		s.setNextRun(name, next[name])
	}

	launch := func(name string, task scheduledTask) {
		running[name] = true
		started := time.Now()
		s.markStarted(name, started)
		s.workers.Add(1)
		go func() {
			defer s.workers.Done()
			runCtx := ctx
			cancel := func() {}
			if task.timeout > 0 {
				runCtx, cancel = context.WithTimeout(ctx, task.timeout)
			}
			defer cancel()
			err := task.run(runCtx)
			result := taskResult{name: name, started: started, finished: time.Now(), err: err}
			select {
			case results <- result:
			case <-ctx.Done():
			}
		}()
	}

	for {
		now = time.Now()
		var wake time.Time
		for name, at := range next {
			if running[name] {
				continue
			}
			if !at.After(now) {
				launch(name, tasks[name])
				continue
			}
			if wake.IsZero() || at.Before(wake) {
				wake = at
			}
		}
		wait := time.Hour
		if !wake.IsZero() {
			wait = time.Until(wake)
			if wait < 0 {
				wait = 0
			}
		}
		timer := time.NewTimer(wait)
		select {
		case <-ctx.Done():
			timer.Stop()
			s.workers.Wait()
			return
		case result := <-results:
			timer.Stop()
			running[result.name] = false
			task := tasks[result.name]
			next[result.name] = task.trigger(ctx).next(result.finished, s.location(ctx))
			s.markFinished(result, next[result.name])
			if result.err != nil {
				log.Printf("panel: scheduled task %s failed after %s: %v", result.name, result.finished.Sub(result.started), result.err)
			}
		case <-s.changed:
			timer.Stop()
			tasks = s.snapshot()
			now = time.Now()
			for name, task := range tasks {
				if !running[name] {
					next[name] = task.trigger(ctx).next(now, s.location(ctx))
					s.setNextRun(name, next[name])
				}
			}
		case <-timer.C:
		}
	}
}
