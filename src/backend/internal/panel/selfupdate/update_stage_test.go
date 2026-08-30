package selfupdate

import "testing"

// TestPanelUpdateStageTransitions 校验自更新阶段状态机的线性推进与非法迁移拒绝。
func TestPanelUpdateStageTransitions(t *testing.T) {
	u := &Updater{}
	u.setStage(updStageCheck, 0, "check")
	u.setStage(updStageDownload, 50, "download")
	u.setStage(updStageDownload, 100, "download done") // 同阶段进度更新
	if u.st.Stage != updStageDownload || u.st.Percent != 100 {
		t.Fatalf("stage = %s percent = %d", u.st.Stage, u.st.Percent)
	}
	// 非法倒退：download → check 拒绝
	u.setStage(updStageCheck, 0, "back")
	if u.st.Stage != updStageDownload {
		t.Fatalf("illegal regression applied, stage = %s", u.st.Stage)
	}
	// 线性推进到 restart
	for _, next := range []string{updStageVerify, updStageExtract, updStageApply, updStageRestart} {
		u.setStage(next, 100, next)
		if u.st.Stage != next {
			t.Fatalf("stage = %s, want %s", u.st.Stage, next)
		}
	}
	// restart 为终态前一步：不得再回退 apply
	u.setStage(updStageApply, 50, "back")
	if u.st.Stage != updStageRestart {
		t.Fatalf("terminal regression applied, stage = %s", u.st.Stage)
	}
}

// TestPanelUpdateStageTerminals 终态（done/failed）不得被普通阶段推进覆盖；
// failed 由 fail 路径显式写入，新一次更新启动时重置。
func TestPanelUpdateStageTerminals(t *testing.T) {
	u := &Updater{st: Status{Stage: updStageDone}}
	u.setStage(updStageDownload, 0, "no")
	if u.st.Stage != updStageDone {
		t.Fatalf("done terminal overridden: %s", u.st.Stage)
	}
	u2 := &Updater{st: Status{Stage: updStageFailed}}
	u2.setStage(updStageVerify, 0, "no")
	if u2.st.Stage != updStageFailed {
		t.Fatalf("failed terminal overridden: %s", u2.st.Stage)
	}
}
