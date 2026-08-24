package dispatch

import (
	"context"
	"errors"
	"testing"

	"lattix/backend/internal/store"
	"lattix/shared"
)

// gateDispatcher 构造 PanelLifecycle 固定为 state 的 Dispatcher（fakeRequester 中服务器 7 在线）。
func gateDispatcher(state string) (*Dispatcher, *fakeRequester) {
	req := &fakeRequester{online: map[int64]bool{7: true}}
	d := New(nil, req, Options{
		PanelLifecycle: func() shared.PanelLifecycleSnapshot {
			return shared.PanelLifecycleSnapshot{State: state}
		},
	}, Events{})
	return d, req
}

func TestStartupBlocksBusinessCommandsButAllowsControlMessages(t *testing.T) {
	d, req := gateDispatcher(shared.PanelStateStartup)

	business := shared.Envelope{Kind: shared.KindRequest, Type: shared.TypeApplyNode}
	if err := d.send(context.Background(), 7, business); !errors.Is(err, ErrPanelNotActive) {
		t.Fatalf("business send error = %v, want ErrPanelNotActive", err)
	}
	control := shared.Envelope{Kind: shared.KindRequest, Type: shared.TypeLifecycleChanged}
	if err := d.send(context.Background(), 7, control); err != nil {
		t.Fatalf("control send error = %v", err)
	}
	if len(req.sent) != 1 || req.sent[0].Type != shared.TypeLifecycleChanged {
		t.Fatalf("sent = %+v, want only the lifecycle control envelope", req.sent)
	}
}

// startup/faulted 状态必须拒投全部业务命令类型（节点/用户/链跳/共享端点/维护/server-test 共 14 类）。
func TestStartupAndFaultedBlockAllBusinessCommands(t *testing.T) {
	businessTypes := []string{
		shared.TypeApplyNode,
		shared.TypeRemoveNode,
		shared.TypeApplyChainHop,
		shared.TypeRemoveChainHop,
		shared.TypeAddUser,
		shared.TypeRemoveUser,
		shared.TypeUninstall,
		shared.TypeUpgradeXray,
		shared.TypeUpgradeAgent,
		shared.TypeApplySharedEndpoint,
		shared.TypeRemoveSharedEndpoint,
		shared.TypeCleanupXray,
		shared.TypeRebuildXray,
		shared.TypeServerTestRun,
	}
	states := []string{shared.PanelStateStartup, shared.PanelStateFaulted}
	for _, state := range states {
		for _, typ := range businessTypes {
			d, _ := gateDispatcher(state)
			env := shared.Envelope{Kind: shared.KindRequest, Type: typ}
			if err := d.send(context.Background(), 7, env); !errors.Is(err, ErrPanelNotActive) {
				t.Fatalf("state %s type %s: send error = %v, want ErrPanelNotActive", state, typ, err)
			}
		}
	}
}

// active 状态门控不拦截：业务命令透传 Requester（与 startup/faulted 拒投互为镜像，
// 防门控条件反转导致全线命令滞留 queued）。
func TestActiveAllowsBusinessCommands(t *testing.T) {
	d, req := gateDispatcher(shared.PanelStateActive)

	business := shared.Envelope{Kind: shared.KindRequest, Type: shared.TypeApplyNode}
	if err := d.send(context.Background(), 7, business); err != nil {
		t.Fatalf("active send error = %v, want nil", err)
	}
	if len(req.sent) != 1 || req.sent[0].Type != shared.TypeApplyNode {
		t.Fatalf("sent = %+v, want the business envelope delivered", req.sent)
	}
}

// 门控拒绝时 Flush 与离线语义一致：命令滞留 queued，面板恢复 active 后补发。
func TestFlushStallsWhilePanelNotActive(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	serverID, err := st.CreateServer(ctx, store.ServerDraft{Alias: "agent", Address: "agent.test", BootstrapToken: "token", MachineType: store.MachineTypeDirect, CountryCode: "US", Location: "Test"})
	if err != nil {
		t.Fatal(err)
	}
	req := &fakeRequester{online: map[int64]bool{serverID: true}}
	state := shared.PanelStateStartup
	d := New(st, req, Options{
		PanelLifecycle: func() shared.PanelLifecycleSnapshot {
			return shared.PanelLifecycleSnapshot{State: state}
		},
	}, Events{})

	commandID, err := d.Enqueue(ctx, serverID, shared.TypeApplyNode, shared.ApplyNodePayload{})
	if err != nil {
		t.Fatal(err)
	}
	if len(req.sent) != 0 {
		t.Fatalf("startup 下不应下发业务命令，sent = %+v", req.sent)
	}
	queued, err := st.QueuedCommands(ctx, serverID)
	if err != nil || len(queued) != 1 || queued[0].ID != commandID {
		t.Fatalf("queued = %+v, err = %v, want command %d 滞留", queued, err, commandID)
	}

	state = shared.PanelStateActive
	d.Flush(ctx, serverID)
	if len(req.sent) != 1 || req.sent[0].Type != shared.TypeApplyNode {
		t.Fatalf("active 后应补发，sent = %+v", req.sent)
	}
}
