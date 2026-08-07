package main

import (
	"sync/atomic"
	"testing"
	"time"
)

func TestOfflineDebouncerScheduleFires(t *testing.T) {
	d := newOfflineDebouncer(20 * time.Millisecond)
	defer d.close()
	var fired atomic.Int32
	d.schedule(1, func() { fired.Add(1) })
	time.Sleep(100 * time.Millisecond)
	if got := fired.Load(); got != 1 {
		t.Fatalf("expected fn to fire once, got %d", got)
	}
}

func TestOfflineDebouncerCancelSuppresses(t *testing.T) {
	d := newOfflineDebouncer(20 * time.Millisecond)
	defer d.close()
	var fired atomic.Int32
	d.schedule(1, func() { fired.Add(1) })
	d.cancel(1)
	d.cancel(1) // 无待执行任务时为空操作
	time.Sleep(100 * time.Millisecond)
	if got := fired.Load(); got != 0 {
		t.Fatalf("expected fn to be cancelled, fired %d times", got)
	}
}

func TestOfflineDebouncerScheduleReplacesPending(t *testing.T) {
	d := newOfflineDebouncer(40 * time.Millisecond)
	defer d.close()
	var fired atomic.Int32
	d.schedule(1, func() { fired.Add(1) })
	time.Sleep(10 * time.Millisecond)
	d.schedule(1, func() { fired.Add(1) }) // 替换：只触发一次，且从 reschedule 起算
	time.Sleep(20 * time.Millisecond)
	if got := fired.Load(); got != 0 {
		t.Fatalf("rescheduled fn fired within the original window")
	}
	time.Sleep(70 * time.Millisecond)
	if got := fired.Load(); got != 1 {
		t.Fatalf("expected exactly one fire after reschedule, got %d", got)
	}
}

func TestOfflineDebouncerCloseStopsPending(t *testing.T) {
	d := newOfflineDebouncer(20 * time.Millisecond)
	var fired atomic.Int32
	d.schedule(1, func() { fired.Add(1) })
	d.close()
	d.schedule(2, func() { fired.Add(1) }) // closed 后 schedule 为空操作
	time.Sleep(100 * time.Millisecond)
	if got := fired.Load(); got != 0 {
		t.Fatalf("expected no fires after close, got %d", got)
	}
}
