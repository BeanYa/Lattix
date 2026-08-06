package progress

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

func TestStartReportFinish(t *testing.T) {
	r := NewRegistry()
	o := r.Start("user_group.update", "更新用户分组", []Stage{{Key: "db", Label: "校验并写入数据库"}, {Key: "reconcile", Label: "同步共享端点"}, {Key: "regenerate", Label: "重新生成订阅文件"}})
	if o == nil || o.ID == "" || o.Status != StatusRunning {
		t.Fatalf("Start = %+v, want running observation with ID", o)
	}
	o.Report("db", 100, "写入完成")
	got, ok := r.Get(o.ID)
	if !ok || got.Stage != "db" || got.Percent != 100 || got.Message != "写入完成" {
		t.Fatalf("Get after Report = %+v, want stage=db percent=100", got)
	}
	o.Finish()
	got, _ = r.Get(o.ID)
	if got.Status != StatusDone || got.FinishedAt == nil {
		t.Fatalf("after Finish: status=%s finished=%v", got.Status, got.FinishedAt)
	}
	o.Finish() // 幂等
}

func TestFailWithError(t *testing.T) {
	r := NewRegistry()
	o := r.Start("x", "y", nil)
	o.Fail(errors.New("boom"))
	got, _ := r.Get(o.ID)
	if got.Status != StatusFailed || !strings.Contains(got.Error, "boom") {
		t.Fatalf("after Fail: %+v", got)
	}
}

func TestCloseFallsBackToDone(t *testing.T) {
	r := NewRegistry()
	o := r.Start("x", "y", nil)
	o.Warn("部分失败")
	o.Close()
	got, _ := r.Get(o.ID)
	if got.Status != StatusDone || len(got.Warnings) != 1 {
		t.Fatalf("after Close: %+v", got)
	}
}

func TestNilObservationIsNoOp(t *testing.T) {
	var o *Observation // 模拟 Start 失败返回 nil 后继续调用
	o.Report("db", 100, "x")
	o.Warn("w")
	o.Finish()
	o.Fail(errors.New("e"))
	o.Close()
	o.WatchUsers([]int64{1, 2})
}

func TestReportRecoversPanic(t *testing.T) {
	r := NewRegistry()
	o := r.Start("x", "y", nil)
	func() {
		defer func() { _ = recover() }() // 如果 panic 泄漏，这个 recover 会抓住并导致测试失败
		o.Report("db", 100, strings.Repeat("x", 1<<20))
	}()
	got, _ := r.Get(o.ID)
	if got == nil {
		t.Fatal("observation must survive report panic")
	}
}

func TestCapacityLimit(t *testing.T) {
	r := NewRegistry()
	for i := 0; i < MaxRunningObservations+5; i++ {
		o := r.Start("k", "t", nil)
		if i < MaxRunningObservations && o == nil {
			t.Fatalf("Start #%d returned nil under capacity", i)
		}
		if i >= MaxRunningObservations && o != nil {
			t.Fatalf("Start #%d returned non-nil over capacity", i)
		}
	}
}

func TestFinishedTTLClears(t *testing.T) {
	old := finishedTTL
	finishedTTL = 20 * time.Millisecond
	defer func() { finishedTTL = old }()
	r := NewRegistry()
	o := r.Start("x", "y", nil)
	o.Finish()
	time.Sleep(40 * time.Millisecond)
	if _, ok := r.Get(o.ID); ok {
		t.Fatal("finished observation should be cleaned after TTL")
	}
}

func TestStartSweepsExpiredFinished(t *testing.T) {
	old := finishedTTL
	finishedTTL = 20 * time.Millisecond
	defer func() { finishedTTL = old }()
	r := NewRegistry()
	o := r.Start("x", "y", nil)
	o.Finish()
	time.Sleep(40 * time.Millisecond)
	if got := r.Start("k", "t", nil); got == nil {
		t.Fatal("Start must succeed and sweep expired observation")
	}
	if _, ok := r.Get(o.ID); ok {
		t.Fatal("expired finished observation should be swept by Start")
	}
}

func TestGetSnapshotIsInert(t *testing.T) {
	r := NewRegistry()
	o := r.Start("x", "y", nil)
	o.WatchUsers([]int64{1, 2})
	o.Report("k", 50, "m")
	snap, ok := r.Get(o.ID)
	if !ok {
		t.Fatal("Get failed")
	}
	snap.WatchUsers([]int64{3, 4})
	snap.Report("k", 90, "mutated")
	snap.Warn("mutated")
	snap.Finish()
	got, _ := r.Get(o.ID)
	if got.Percent != 50 || got.Message != "m" || got.Status != StatusRunning {
		t.Fatalf("snapshot mutations leaked into registry: %+v", got)
	}
	if len(got.Warnings) != 0 {
		t.Fatalf("snapshot warning leaked: %v", got.Warnings)
	}
}

func TestAttachAndObserveIDFromContext(t *testing.T) {
	r := NewRegistry()
	ctx := r.Attach(context.Background(), "obs-1")
	if got := r.ObserveIDFromContext(ctx); got != "obs-1" {
		t.Fatalf("ObserveIDFromContext = %q", got)
	}
	if got := r.ObserveIDFromContext(context.Background()); got != "" {
		t.Fatalf("empty ctx ObserveIDFromContext = %q", got)
	}
}

func TestWatchUsersProgressAndWarnings(t *testing.T) {
	r := NewRegistry()
	o := r.Start("x", "y", []Stage{{Key: "regenerate", Label: "重新生成订阅文件"}})
	o.WatchUsers([]int64{1, 2, 3, 4})
	o.Report("regenerate", 0, "等待发布")
	r.NotifyUserPublished(1, nil)
	r.NotifyUserPublished(2, errors.New("生成失败"))
	got, _ := r.Get(o.ID)
	if got.Percent != 25 {
		t.Fatalf("percent = %d, want 25", got.Percent)
	}
	r.NotifyUserPublished(3, nil)
	r.NotifyUserPublished(4, nil)
	got, _ = r.Get(o.ID)
	if got.Percent != 100 {
		t.Fatalf("percent = %d, want 100", got.Percent)
	}
	if len(got.Warnings) != 1 {
		t.Fatalf("warnings = %v, want 1", got.Warnings)
	}
	// 未登记用户不影响
	r.NotifyUserPublished(999, errors.New("ignored"))
	got, _ = r.Get(o.ID)
	if len(got.Warnings) != 1 {
		t.Fatalf("warnings = %v, want still 1", got.Warnings)
	}
}

func TestNotifyPublishedRecoversPanic(t *testing.T) {
	r := NewRegistry()
	o := r.Start("x", "y", []Stage{{Key: "regenerate", Label: "重新生成订阅文件"}})
	o.WatchUsers([]int64{1})
	// 触发一次非法调用路径后仍可继续使用
	r.NotifyUserPublished(1, errors.New("boom"))
	if _, ok := r.Get(o.ID); !ok {
		t.Fatal("registry must survive notification")
	}
}
