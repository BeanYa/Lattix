package panel

import (
	"context"
	"fmt"
	"time"

	"lattix/backend/internal/alert"
	"lattix/backend/internal/store"
)

// Ban 告警巡检（§告警分类 Ban告警）：与三网目录测速同频（chain-latency.probe 的
// 调度周期）逐轮评估每台在线服务器的三网样本。单轮三网目标齐全且全部失败记一次
// 全丢包：连续 5 轮进入 suspected（疑似被 Ban），连续 10 轮进入 banned（已被 Ban）；
// banned 后每半小时确认一次三网是否恢复在线，确认在线进入 recovering，下一轮评估
// 仍在线即发出取消告警并归零。状态机为纯函数（advanceBanWatch），快照持久化在
// server_ban_watch，面板重启后从上一次状态继续。
const (
	banSuspectStreak   = 5                // 连续全丢包轮次 → 疑似被 Ban
	banBannedStreak    = 10               // 连续全丢包轮次 → 判定已被 Ban
	banConfirmInterval = 30 * time.Minute // banned 后的恢复确认周期（每半小时）
)

// 单轮三网评估结论。
const (
	banVerdictAllLoss      = "all_loss"     // 三网目标齐全且全部失败（100% 丢包）
	banVerdictOnline       = "online"       // 三网齐全且至少一路成功（恢复在线）
	banVerdictInconclusive = "inconclusive" // 样本不全（目录缺失/上报迟滞）：本轮不计数也不重置
)

// banCarrierKeys 是三网运营商（与 store.catalogProbeCarriers 的 key 一致，此处独立
// 常量避免反依赖 store 私有变量）。
var banCarrierKeys = []string{"telecom", "unicom", "mobile"}

// banVerdictFromOutcomes 依窗口内逐运营商成败计数给出单轮结论：
// 三网样本齐全才可判定——全败即全丢包，任一成功即在线。
func banVerdictFromOutcomes(outcomes map[string]store.CatalogCarrierOutcome) string {
	anySuccess := false
	for _, carrier := range banCarrierKeys {
		outcome, ok := outcomes[carrier]
		if !ok || outcome.Samples == 0 {
			return banVerdictInconclusive
		}
		if !outcome.AllLost() {
			anySuccess = true
		}
	}
	if anySuccess {
		return banVerdictOnline
	}
	return banVerdictAllLoss
}

// banState 归一化观察状态（无行/未初始化按 normal 处理）。
func banState(row store.ServerBanWatch) string {
	if row.State == "" {
		return store.BanStateNormal
	}
	return row.State
}

// resetBanWatch 归零观察快照（丢包被打断或取消告警后回到静默观察）。
func resetBanWatch(prev store.ServerBanWatch, now time.Time) store.ServerBanWatch {
	return store.ServerBanWatch{ServerID: prev.ServerID, State: store.BanStateNormal, UpdatedAt: now}
}

// advanceBanWatch 是 Ban 观察状态机的单步推进（纯函数）：
//
//	normal --连续5轮全丢--> suspected --连续10轮--> banned --半小时确认在线--> recovering --下一轮仍在线--> normal
//
// 前提是 agent 在线：离线轮无法区分宕机与被 Ban，观察中（normal/suspected）直接
// 归零重来；banned/recovering 保持（无数据不等于恢复），待 agent 回来后继续判定。
// 返回推进后的快照与应推送的告警事件（空串 = 无）。无实质变化的轮次原样返回
// prev（runner 据此跳过落库），只有状态推进才刷新 updated_at。
func advanceBanWatch(prev store.ServerBanWatch, agentOnline bool, verdict string, now time.Time, confirmEvery time.Duration) (store.ServerBanWatch, string) {
	if !agentOnline {
		switch banState(prev) {
		case store.BanStateSuspected:
			return resetBanWatch(prev, now), ""
		case store.BanStateNormal:
			if prev.LossStreak != 0 {
				return resetBanWatch(prev, now), ""
			}
		}
		return prev, ""
	}
	switch verdict {
	case banVerdictInconclusive:
		// 本轮样本不全（目录缺失/上报迟滞）：不计数也不重置。
		return prev, ""
	case banVerdictAllLoss:
		next := prev
		next.LossStreak = prev.LossStreak + 1
		if next.StreakStartedAt == nil {
			started := now
			next.StreakStartedAt = &started
		}
		next.UpdatedAt = now
		switch {
		case next.LossStreak >= banBannedStreak:
			next.State = store.BanStateBanned
			if prev.LastConfirmAt == nil {
				confirmed := now
				next.LastConfirmAt = &confirmed // banned 起算半小时确认节流
			}
			switch banState(prev) {
			case store.BanStateRecovering:
				// 复发：待取消状态被驳回，重新判定已被 Ban。
				return next, alert.EventServerBanned
			case store.BanStateBanned:
				return next, "" // 已在 banned，不重复告警
			default:
				next.BannedAt = &now
				return next, alert.EventServerBanned
			}
		case next.LossStreak >= banSuspectStreak:
			next.State = store.BanStateSuspected
			if banState(prev) == store.BanStateNormal {
				suspected := now
				next.SuspectedAt = &suspected
				return next, alert.EventServerBanSuspected
			}
			return next, ""
		default:
			next.State = store.BanStateNormal
			return next, ""
		}
	default: // banVerdictOnline：三网至少一路恢复
		switch banState(prev) {
		case store.BanStateBanned:
			// 每半小时确认一次：到点确认在线才进入 recovering，下一轮评估正式取消。
			if prev.LastConfirmAt == nil || now.Sub(*prev.LastConfirmAt) >= confirmEvery {
				next := prev
				next.State = store.BanStateRecovering
				confirmed := now
				next.LastConfirmAt = &confirmed
				next.UpdatedAt = now
				return next, ""
			}
			return prev, ""
		case store.BanStateRecovering:
			// 下一轮告警时取消：确认恢复成立，归零并发出取消告警。
			return resetBanWatch(prev, now), alert.EventServerBanRecovered
		case store.BanStateSuspected:
			return resetBanWatch(prev, now), ""
		default:
			if prev.LossStreak != 0 {
				return resetBanWatch(prev, now), ""
			}
			return prev, ""
		}
	}
}

// banEventDetail 渲染 Ban 告警的推送详情文案。
func banEventDetail(event string, streak int64) string {
	switch event {
	case alert.EventServerBanSuspected:
		return fmt.Sprintf("三网测速连续 %d 轮 100%% 丢包，疑似被 Ban（可能被 ban），持续观察中", streak)
	case alert.EventServerBanned:
		return "三网测速连续 10 轮以上 100% 丢包，判定 Banned；面板每半小时复查一次，恢复在线后自动取消告警"
	case alert.EventServerBanRecovered:
		return "三网测速恢复在线，取消该服务器的被 Ban 告警"
	}
	return ""
}

// notifyAlert 推送一条事件告警（alerter 未接线时静默忽略）。
func (s *Server) notifyAlert(serverID int64, event, detail string) {
	if s.alerter == nil {
		return
	}
	s.alerter.Notify(serverID, event, "", detail)
}

// latencyProbeRoundInterval 返回测速轮次周期（LATTIX_LATENCY_PROBE_INTERVAL 可覆盖，
// 与 chain-latency.probe 同源）：Ban 观察与之同频，一次评估对应一轮三网测速。
func latencyProbeRoundInterval() time.Duration {
	return envDuration("LATTIX_LATENCY_PROBE_INTERVAL", 5*time.Minute)
}

// runBanWatch 是 server.ban-watch 定时任务的单轮评估：对每台服务器推进状态机并
// 落库快照。评估窗口为上一轮任务时刻至本次（与测速轮次同频推进，调度延迟时窗口
// 变宽只会少计轮次，方向保守）。
func (s *Server) runBanWatch(ctx context.Context) error {
	targets, _, err := s.st.CatalogProbeTargets(ctx)
	if err != nil {
		return err
	}
	targetKeys := make([]string, 0, len(targets))
	for _, target := range targets {
		targetKeys = append(targetKeys, target.Key)
	}
	servers, err := s.st.ListServers(ctx)
	if err != nil {
		return err
	}
	watches, err := s.st.ServerBanWatchMap(ctx)
	if err != nil {
		return err
	}
	now := time.Now()
	from := s.banWatchLastEval
	if from.IsZero() {
		from = now.Add(-latencyProbeRoundInterval())
	}
	s.banWatchLastEval = now
	for _, srv := range servers {
		// 非直连服务器（大陆不可直连）：三网丢包属预期，Ban 告警不适用；
		// 跳过评估并清除既有观察状态（含 suspected/banned 徽标与推送）。
		if srv.NonDirect {
			if _, watched := watches[srv.ID]; watched {
				if err := s.st.DeleteServerBanWatch(ctx, srv.ID); err != nil {
					return err
				}
			}
			continue
		}
		online := s.req.IsOnline(srv.ID)
		verdict := banVerdictInconclusive
		if online && len(targetKeys) > 0 {
			outcomes, err := s.st.CatalogCarrierOutcomes(ctx, srv.ID, targetKeys, from, now)
			if err != nil {
				return err
			}
			verdict = banVerdictFromOutcomes(outcomes)
		}
		// 无观察行的服务器以零值行推进：补上 ServerID，避免以 server_id=0 落库。
		prev := watches[srv.ID]
		prev.ServerID = srv.ID
		next, event := advanceBanWatch(prev, online, verdict, now, banConfirmInterval)
		if event != "" {
			s.notifyAlert(srv.ID, event, banEventDetail(event, next.LossStreak))
		}
		if next != watches[srv.ID] {
			if err := s.st.UpsertServerBanWatch(ctx, next); err != nil {
				return err
			}
		}
	}
	return nil
}
