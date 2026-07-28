package panel

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestTaskSchedulerPreventsTaskOverlap(t *testing.T) {
	scheduler := newTaskScheduler(func(context.Context) *time.Location { return time.UTC })
	var active atomic.Int32
	var maximum atomic.Int32
	var runs atomic.Int32
	done := make(chan struct{})
	scheduler.register(scheduledTask{
		name:       "slow",
		runOnStart: true,
		trigger:    func(context.Context) taskTrigger { return intervalTrigger(5 * time.Millisecond) },
		run: func(context.Context) error {
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
	go func() { scheduler.run(ctx); close(finished) }()
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
	scheduler := newTaskScheduler(func(context.Context) *time.Location { return time.UTC })
	var mu sync.RWMutex
	delay := 10 * time.Second
	run := make(chan struct{}, 1)
	scheduler.register(scheduledTask{
		name: "configurable",
		trigger: func(context.Context) taskTrigger {
			mu.RLock()
			defer mu.RUnlock()
			return intervalTrigger(delay)
		},
		run: func(context.Context) error { run <- struct{}{}; return nil },
	})
	ctx, cancel := context.WithCancel(context.Background())
	finished := make(chan struct{})
	go func() { scheduler.run(ctx); close(finished) }()

	time.Sleep(20 * time.Millisecond)
	mu.Lock()
	delay = 5 * time.Millisecond
	mu.Unlock()
	scheduler.notifyChanged()
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
	schedule := inspectionSchedule{Every: 1, Unit: "day", At: "00:05"}
	after := time.Date(2026, time.July, 9, 16, 4, 0, 0, time.UTC)
	got := schedule.next(after, loc)
	want := time.Date(2026, time.July, 10, 0, 5, 0, 0, loc)
	if !got.Equal(want) {
		t.Fatalf("next = %s, want %s", got, want)
	}
}
