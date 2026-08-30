package scheduler

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestTaskSchedulerPreventsTaskOverlap(t *testing.T) {
	scheduler := NewTaskScheduler(func(context.Context) *time.Location { return time.UTC })
	var active atomic.Int32
	var maximum atomic.Int32
	var runs atomic.Int32
	done := make(chan struct{})
	scheduler.Register(ScheduledTask{
		Name:       "slow",
		RunOnStart: true,
		Trigger:    func(context.Context) TaskTrigger { return IntervalTrigger(5 * time.Millisecond) },
		Run: func(context.Context) error {
			current := active.Add(1)
			for {
				old := maximum.Load()
				if current <= old || maximum.CompareAndSwap(old, current) {
					break
				}
			}
			time.Sleep(25 * time.Millisecond)
			active.Add(-1)
			if runs.Add(1) == 3 {
				close(done)
			}
			return nil
		},
	})
	ctx, cancel := context.WithCancel(context.Background())
	finished := make(chan struct{})
	go func() { scheduler.Run(ctx); close(finished) }()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("scheduler did not complete three runs")
	}
	cancel()
	<-finished
	if got := maximum.Load(); got != 1 {
		t.Fatalf("maximum concurrent runs = %d, want 1", got)
	}
}

func TestTaskSchedulerScheduleChangeWakesLoop(t *testing.T) {
	scheduler := NewTaskScheduler(func(context.Context) *time.Location { return time.UTC })
	var mu sync.RWMutex
	delay := 10 * time.Second
	run := make(chan struct{}, 1)
	scheduler.Register(ScheduledTask{
		Name: "configurable",
		Trigger: func(context.Context) TaskTrigger {
			mu.RLock()
			defer mu.RUnlock()
			return IntervalTrigger(delay)
		},
		Run: func(context.Context) error { run <- struct{}{}; return nil },
	})
	ctx, cancel := context.WithCancel(context.Background())
	finished := make(chan struct{})
	go func() { scheduler.Run(ctx); close(finished) }()

	time.Sleep(20 * time.Millisecond)
	mu.Lock()
	delay = 5 * time.Millisecond
	mu.Unlock()
	scheduler.NotifyChanged()
	select {
	case <-run:
	case <-time.After(500 * time.Millisecond):
		t.Fatal("schedule change did not wake the scheduler")
	}
	cancel()
	<-finished
}

func TestInspectionScheduleUsesPanelTimezone(t *testing.T) {
	loc, err := time.LoadLocation("Asia/Shanghai")
	if err != nil {
		t.Fatal(err)
	}
	schedule := InspectionSchedule{Every: 1, Unit: "day", At: "00:05"}
	after := time.Date(2026, time.July, 9, 16, 4, 0, 0, time.UTC)
	got := schedule.Next(after, loc)
	want := time.Date(2026, time.July, 10, 0, 5, 0, 0, loc)
	if !got.Equal(want) {
		t.Fatalf("next = %s, want %s", got, want)
	}
}

func TestTaskSchedulerStatusTracksCompletedRuns(t *testing.T) {
	scheduler := NewTaskScheduler(func(context.Context) *time.Location { return time.UTC })
	ran := make(chan struct{}, 1)
	scheduler.Register(ScheduledTask{
		Name:       "observed",
		RunOnStart: true,
		Trigger:    func(context.Context) TaskTrigger { return IntervalTrigger(time.Hour) },
		Run: func(context.Context) error {
			ran <- struct{}{}
			return nil
		},
	})

	ctx, cancel := context.WithCancel(context.Background())
	finished := make(chan struct{})
	go func() { scheduler.Run(ctx); close(finished) }()
	select {
	case <-ran:
	case <-time.After(time.Second):
		t.Fatal("scheduled task did not run")
	}

	deadline := time.Now().Add(time.Second)
	var status ScheduledTaskStatus
	for time.Now().Before(deadline) {
		items := scheduler.StatusSnapshot()
		if len(items) == 1 && items[0].Runs == 1 {
			status = items[0]
			break
		}
		time.Sleep(time.Millisecond)
	}
	cancel()
	<-finished

	if status.Name != "observed" || status.Running || status.LastFinishedAt == nil || status.NextRunAt == nil {
		t.Fatalf("status = %+v, want completed run with next schedule", status)
	}
}

func TestInspectionScheduleNextUsesCalendarTime(t *testing.T) {
	loc := time.FixedZone("test", 8*60*60)
	after := time.Date(2026, time.July, 28, 4, 0, 0, 0, loc)
	next := (InspectionSchedule{Every: 1, Unit: "day", At: "03:00"}).Next(after, loc)
	want := time.Date(2026, time.July, 29, 3, 0, 0, 0, loc)
	if !next.Equal(want) {
		t.Fatalf("next = %s, want %s", next, want)
	}
}
