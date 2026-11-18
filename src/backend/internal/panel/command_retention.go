package panel

import (
	"context"
	"log"
	"time"
)

// commandRetention 是 commands 表的保留期：终态命令（acked/failed/abandoned）
// 只是操作日志，超过保留期即删；queued/sent 是离线补发队列，永不清理。
//
// 线上库实测：10.9 万行终态命令占 94MB（整库 234MB 的 40%），其中 9.8 万行是
// latency.probe——这类探测命令写入即用，长期保留没有价值。
const commandRetention = 14 * 24 * time.Hour

// probeCommandRetention 是探测类命令的保留期：它们是「立即投递、结果走事件
// 通道落库」的测量指令，命令行的价值只在故障排查的最近一轮，默认保留 1 天。
// 实测 14 天窗口内 63k 行里 62.6k 行（28MB）是这类命令。
const probeCommandRetention = 24 * time.Hour

// probeCommandTypes 是需要短保留期的命令类型。
var probeCommandTypes = []string{"latency.probe"}

// commandRetentionBatches 是每轮清理的最大批次数（每批 2000 行）：分批提交避免
// 一次删除十万行长期持有 SQLite 写锁，单轮上限同时兜住极端积压。
const commandRetentionBatches = 60

// cleanupCommands 按保留期清理终态命令，并在真正删除后刷新查询计划统计。
func (s *Server) cleanupCommands(ctx context.Context) error {
	now := time.Now().UTC()
	deleted, err := s.pruneTerminalCommands(ctx, now.Add(-probeCommandRetention), probeCommandTypes...)
	if err != nil {
		return err
	}
	rest, err := s.pruneTerminalCommands(ctx, now.Add(-commandRetention))
	if err != nil {
		return err
	}
	deleted += rest
	if deleted == 0 {
		return nil
	}
	if err := s.st.AnalyzeTables(ctx, "commands"); err != nil {
		log.Printf("panel: analyze commands after retention: %v", err)
	}
	log.Printf("panel: commands retention deleted %d terminal rows", deleted)
	return nil
}

// pruneTerminalCommands 分批删除直到清空或到达批次上限。
func (s *Server) pruneTerminalCommands(ctx context.Context, cutoff time.Time, types ...string) (int64, error) {
	var total int64
	for i := 0; i < commandRetentionBatches; i++ {
		n, err := s.st.DeleteTerminalCommands(ctx, cutoff, 2000, types...)
		if err != nil {
			return total, err
		}
		total += n
		if n < 2000 {
			break
		}
	}
	return total, nil
}
