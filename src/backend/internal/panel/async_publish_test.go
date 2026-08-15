package panel

import (
	"context"
	"testing"
	"time"

	"lattix/backend/internal/store"
	"lattix/backend/internal/sub"
)

// 用户/模板指派类操作已改为异步发布（EnqueueUsers + observe 收口）；
// 下列辅助启动发布循环并等待结果，替代旧的"请求内同步发布"断言。

// startTestRegenerator 启动订阅异步重发布循环，测试结束时停止并等待退出。
func startTestRegenerator(t *testing.T, srv *sub.Server) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	srv.StartRegenerator(ctx)
	t.Cleanup(func() {
		cancel()
		_ = srv.WaitRegenerator(context.Background())
	})
}

// awaitSnapshotReady 等待用户订阅快照由异步发布循环生成（最长 5 秒）。
func awaitSnapshotReady(t *testing.T, st *store.Store, userID int64) store.SubscriptionSnapshotStatus {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		snapshot, err := st.SubscriptionSnapshotStatus(context.Background(), userID)
		if err == nil && snapshot.Status == store.SubscriptionGenerationReady {
			return snapshot
		}
		if time.Now().After(deadline) {
			t.Fatalf("user %d 订阅快照未及时发布: %+v (err %v)", userID, snapshot, err)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// awaitSubscriptionFileRevision 等待用户订阅文件修订号超过 minRevision（异步重发布完成标志）。
func awaitSubscriptionFileRevision(t *testing.T, st *store.Store, userID int64, format string, minRevision int64) store.SubscriptionFile {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		file, err := st.PublishedSubscriptionFile(context.Background(), userID, format)
		if err == nil && file.Revision > minRevision {
			return file
		}
		if time.Now().After(deadline) {
			t.Fatalf("user %d 订阅文件 %s 修订号未超过 %d: %+v (err %v)", userID, format, minRevision, file, err)
		}
		time.Sleep(20 * time.Millisecond)
	}
}
