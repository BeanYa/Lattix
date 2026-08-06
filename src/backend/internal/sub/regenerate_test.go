package sub

import (
	"context"
	"testing"
	"time"

	"lattix/backend/internal/progress"
	"lattix/backend/internal/store"
)

// TestObserverNotifyUserPublished 验证 regenerator 发布循环的旁路回调：
// SetObserver 注入后，每个用户发布完成都推进观察（失败计入警告），
// 观察注册表缺省（nil）时发布循环行为不变。
func TestObserverNotifyUserPublished(t *testing.T) {
	st, err := store.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })

	srv := New(st, nil, nil)
	reg := progress.NewRegistry()
	srv.SetObserver(reg)
	o := reg.Start("user_group.regenerate", "重新生成订阅文件",
		[]progress.Stage{{Key: "regenerate", Label: "重新生成订阅文件"}})
	o.WatchUsers([]int64{7}) // 用户 7 不存在 → PublishUser 立即失败 → 旁路记录失败通知

	ctx, cancel := context.WithCancel(context.Background())
	srv.StartRegenerator(ctx)
	srv.EnqueueUsers([]int64{7}, "")

	deadline := time.Now().Add(3 * time.Second)
	for {
		got, ok := reg.Get(o.ID)
		if ok && len(got.Warnings) > 0 {
			if got.Percent != 100 {
				t.Fatalf("percent = %d, want 100", got.Percent)
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("observer was not notified: %+v", got)
		}
		time.Sleep(20 * time.Millisecond)
	}
	cancel()
	waitCtx, waitCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer waitCancel()
	if err := srv.WaitRegenerator(waitCtx); err != nil {
		t.Fatalf("wait regenerator: %v", err)
	}
}
