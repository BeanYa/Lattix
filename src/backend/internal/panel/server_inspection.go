package panel

import (
	"context"
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"time"

	"lattix/backend/internal/inspection"
	"lattix/backend/internal/panel/scheduler"
	"lattix/backend/internal/store"
	"lattix/shared"
)

// 服务器周体检（§server-testing）：复用现有 server-test 原子任务，只启用
// ip_quality / tcp_ipv4 / connectivity 三类；仅直连机型参与。Agent 仍只保存
// 最后一次完整报告，面板把每次自动体检的紧凑摘要写入 server_inspection_runs，
// 由运行监控汇总展示。现有三网测速（server_catalog_probes）在完成时一并快照。
const (
	serverInspectionInterval          = 7 * 24 * time.Hour
	serverInspectionSchedulerInterval = 15 * time.Minute
	serverInspectionMaxPerRun         = 5
	serverInspectionHistoryLimit      = 8
)

func serverInspectionCategories() []shared.ServerTestCategory {
	return []shared.ServerTestCategory{
		shared.ServerTestIPQuality,
		shared.ServerTestTCPIPv4,
		shared.ServerTestConnectivity,
	}
}

func (s *Server) registerServerInspectionTask() {
	s.scheduler.Register(scheduler.ScheduledTask{
		Name: "server.inspection", RunOnStart: true, Timeout: 5 * time.Minute,
		Trigger: func(context.Context) scheduler.TaskTrigger {
			return scheduler.IntervalTrigger(serverInspectionSchedulerInterval)
		},
		Run: s.runServerInspections,
	})
}

// StartServerInspectionOnConnect 是 Agent 首次连上/重连时的入口：没有体检记录或
// 上次体检已过 7 天时立即排队，避免“加机后等一周”的空白。目录尚未就绪时静默
// 返回，由 15 分钟调度任务在目录刷新后补跑。
func (s *Server) StartServerInspectionOnConnect(ctx context.Context, serverID int64) {
	s.reconcileServerInspectionRuns(ctx)
	if err := s.maybeStartServerInspection(ctx, serverID, true); err != nil && !errors.Is(err, store.ErrServerTestInProgress) {
		log.Printf("panel: server %d: start inspection on connect: %v", serverID, err)
	}
}

func (s *Server) runServerInspections(ctx context.Context) error {
	s.reconcileServerInspectionRuns(ctx)
	servers, err := s.st.ListServers(ctx)
	if err != nil {
		return err
	}
	started := 0
	for _, srv := range servers {
		if started >= serverInspectionMaxPerRun {
			break
		}
		if srv.MachineType != store.MachineTypeDirect {
			continue
		}
		if s.req == nil || !s.req.IsOnline(srv.ID) {
			// 离线节点在重连时由 StartServerInspectionOnConnect 补跑，避免把
			// 过期目标快照长时间排在队列里。
			continue
		}
		due, err := s.serverInspectionDue(ctx, srv.ID)
		if err != nil {
			log.Printf("panel: server %d: inspection due check: %v", srv.ID, err)
			continue
		}
		if !due {
			continue
		}
		if err := s.maybeStartServerInspection(ctx, srv.ID, false); err != nil {
			if !errors.Is(err, store.ErrServerTestInProgress) {
				log.Printf("panel: server %d: start weekly inspection: %v", srv.ID, err)
			}
			continue
		}
		started++
	}
	return nil
}

// maybeStartServerInspection 是唯一的入队入口，调用方需保证服务器为直连且在线
// （调度任务与 OnConnect 各检查一次）。返回 ErrServerTestInProgress 表示已有测试
// 正在跑，调用方应静默跳过。
func (s *Server) maybeStartServerInspection(ctx context.Context, serverID int64, onConnect bool) error {
	srv, err := s.st.ServerByID(ctx, serverID)
	if err != nil {
		return err
	}
	if srv.MachineType != store.MachineTypeDirect {
		return nil
	}
	if !onConnect && (s.req == nil || !s.req.IsOnline(serverID)) {
		return nil
	}
	due, err := s.serverInspectionDue(ctx, serverID)
	if err != nil {
		return err
	}
	if !due {
		return nil
	}
	catalog, err := s.inspectionCatalogSnapshot(ctx)
	if err != nil {
		return err
	}
	task, err := s.disp.EnqueueServerTestInspection(ctx, serverID, serverInspectionCategories(), catalog)
	if err != nil {
		return err
	}
	if _, err := s.st.CreateServerInspectionRun(ctx, serverID, task.TaskID); err != nil {
		// 任务已入队；历史写入失败不应阻断体检本身。回调会因找不到 run 而忽略摘要。
		log.Printf("panel: server %d: create inspection run: %v", serverID, err)
		return nil
	}
	return nil
}

// inspectionCatalogSnapshot 复用 server-test 的目录快照。新面板首次启动时 CDN
// 目录可能尚未抓取（刷新任务默认 6 小时一次），这里在目录为空时补拉一次，
// 让首次接入的体检不必等下一轮定时刷新；补拉失败仍返回原错误由调度重试。
func (s *Server) inspectionCatalogSnapshot(ctx context.Context) (shared.ServerTestCatalogSnapshot, error) {
	catalog, err := s.serverTestCatalogSnapshot(ctx, serverInspectionCategories())
	if err == nil || s.cdn == nil {
		return catalog, err
	}
	raw, settingErr := s.st.GetSetting(ctx, store.SettingCDNNodeCatalog)
	if settingErr != nil || raw != "" {
		return catalog, err
	}
	if refreshErr := s.cdn.Refresh(ctx); refreshErr != nil {
		log.Printf("panel: refresh CDN catalog for first inspection: %v", refreshErr)
		return catalog, err
	}
	return s.serverTestCatalogSnapshot(ctx, serverInspectionCategories())
}

// serverInspectionDue 以最近一次体检尝试（含失败/在途）为锚：已过 7 天才再次
// 入队；在途任务等待其自然完成，不在每轮调度重复排队。
func (s *Server) serverInspectionDue(ctx context.Context, serverID int64) (bool, error) {
	latest, err := s.st.LatestServerInspectionRun(ctx, serverID)
	if errors.Is(err, store.ErrNotFound) {
		return true, nil
	}
	if err != nil {
		return false, err
	}
	if !shared.ServerTestTaskStatus(latest.Status).Terminal() && latest.Status != "superseded" {
		return false, nil
	}
	anchor := latest.StartedAt
	if latest.CompletedAt != nil && *latest.CompletedAt != "" {
		anchor = *latest.CompletedAt
	}
	parsed, ok := parseInspectionTime(anchor)
	if !ok {
		return true, nil
	}
	return time.Since(parsed) >= serverInspectionInterval, nil
}

// onServerTestCompleted 由 dispatcher 在权威结果落库后调用。只有存在对应
// inspection run 的任务才镜像摘要；手动测试的任务没有 run，直接忽略。
func (s *Server) onServerTestCompleted(ctx context.Context, serverID int64, taskID string) {
	_, err := s.st.ServerInspectionRunByTaskID(ctx, taskID)
	if errors.Is(err, store.ErrNotFound) {
		return
	}
	if err != nil {
		log.Printf("panel: server %d: read inspection run: %v", serverID, err)
		return
	}
	task, err := s.st.ServerTestByServerID(ctx, serverID)
	if err != nil {
		log.Printf("panel: server %d: read server test for inspection: %v", serverID, err)
		return
	}
	if task.TaskID != taskID {
		_ = s.st.SupersedeServerInspectionRun(ctx, serverID, taskID, "测试任务已被新的任务取代")
		return
	}
	if !task.Status.Terminal() {
		return
	}
	if task.Result == nil {
		_ = s.st.CompleteServerInspectionRun(ctx, serverID, taskID, string(shared.ServerTestFailed),
			"", task.ErrorCode, task.ErrorMessage)
		return
	}
	summary := inspection.Build(task.Result)
	probes := s.inspectionCatalogProbes(ctx, serverID)
	payload := serverInspectionSummary{Summary: summary, CatalogProbes: probes}
	raw, err := json.Marshal(payload)
	if err != nil {
		log.Printf("panel: server %d: marshal inspection summary: %v", serverID, err)
		return
	}
	if err := s.st.CompleteServerInspectionRun(ctx, serverID, taskID, string(task.Status),
		string(raw), task.ErrorCode, task.ErrorMessage); err != nil && !errors.Is(err, store.ErrNotFound) {
		log.Printf("panel: server %d: complete inspection run: %v", serverID, err)
	}
}

func (s *Server) onServerTestFailed(ctx context.Context, serverID int64, taskID, code, message string) {
	if _, err := s.st.ServerInspectionRunByTaskID(ctx, taskID); errors.Is(err, store.ErrNotFound) {
		return
	} else if err != nil {
		return
	}
	status := "failed"
	if code == "superseded" {
		status = "superseded"
	}
	if err := s.st.CompleteServerInspectionRun(ctx, serverID, taskID, status, "", code, message); err != nil &&
		!errors.Is(err, store.ErrNotFound) {
		log.Printf("panel: server %d: fail inspection run: %v", serverID, err)
	}
}

// reconcileServerInspectionRuns 收敛面板停机/重启期间错过回调的 run：按 task_id
// 与当前权威任务比对，已完成则补摘要，已被替换则标记 superseded。
func (s *Server) reconcileServerInspectionRuns(ctx context.Context) {
	pending, err := s.st.PendingServerInspectionRuns(ctx)
	if err != nil {
		log.Printf("panel: list pending inspection runs: %v", err)
		return
	}
	for _, run := range pending {
		task, err := s.st.ServerTestByServerID(ctx, run.ServerID)
		if errors.Is(err, store.ErrNotFound) {
			_ = s.st.SupersedeServerInspectionRun(ctx, run.ServerID, run.TaskID, "测试任务已不存在")
			continue
		}
		if err != nil {
			continue
		}
		if task.TaskID != run.TaskID {
			_ = s.st.SupersedeServerInspectionRun(ctx, run.ServerID, run.TaskID, "测试任务已被新的任务取代")
			continue
		}
		if !task.Status.Terminal() {
			continue
		}
		if task.Result == nil {
			_ = s.st.CompleteServerInspectionRun(ctx, run.ServerID, run.TaskID,
				string(shared.ServerTestFailed), "", task.ErrorCode, task.ErrorMessage)
			continue
		}
		summary := inspection.Build(task.Result)
		probes := s.inspectionCatalogProbes(ctx, run.ServerID)
		raw, err := json.Marshal(serverInspectionSummary{Summary: summary, CatalogProbes: probes})
		if err != nil {
			continue
		}
		_ = s.st.CompleteServerInspectionRun(ctx, run.ServerID, run.TaskID, string(task.Status),
			string(raw), task.ErrorCode, task.ErrorMessage)
	}
}

// serverInspectionSummary 在紧凑摘要之外附上完成时刻的三网测速快照；三网测速
// 本身由 latency.probe 持续刷新，这里只取新鲜窗口内的最新值，不重复探测。
type serverInspectionSummary struct {
	inspection.Summary
	CatalogProbes *serverInspectionCatalogProbes `json:"catalog_probes,omitempty"`
}

type serverInspectionCatalogProbe struct {
	Label       string   `json:"label"`
	Carrier     string   `json:"carrier"`
	LatencyMS   *float64 `json:"latency_ms"`
	LossPercent *float64 `json:"loss_percent"`
	MeasuredAt  string   `json:"measured_at,omitempty"`
}

type serverInspectionCatalogProbes struct {
	Province string                         `json:"province"`
	City     string                         `json:"city,omitempty"`
	Rows     []serverInspectionCatalogProbe `json:"rows"`
}

func (s *Server) inspectionCatalogProbes(ctx context.Context, serverID int64) *serverInspectionCatalogProbes {
	targets, region, err := s.st.CatalogProbeTargets(ctx)
	if err != nil || len(targets) == 0 {
		return nil
	}
	fresh, err := s.st.FreshServerCatalogProbes(ctx, catalogProbeFreshWindow)
	if err != nil {
		return nil
	}
	lossRows, err := s.st.CatalogProbeLossStats(ctx, catalogProbeLossWindow)
	if err != nil {
		return nil
	}
	freshByKey := make(map[string]store.CatalogProbeRow, len(fresh))
	for _, row := range fresh {
		if row.FromServer == serverID {
			freshByKey[row.TargetKey] = row
		}
	}
	lossByKey := make(map[string]store.CatalogProbeLossRow, len(lossRows))
	for _, row := range lossRows {
		if row.FromServer == serverID {
			lossByKey[row.TargetKey] = row
		}
	}
	out := &serverInspectionCatalogProbes{Province: region.Province, City: region.City,
		Rows: make([]serverInspectionCatalogProbe, 0, len(targets))}
	for _, target := range targets {
		row, ok := freshByKey[target.Key]
		loss := lossByKey[target.Key]
		entry := serverInspectionCatalogProbe{Label: target.Label, Carrier: target.Carrier}
		if ok {
			entry.LatencyMS = row.LatencyMS
			entry.MeasuredAt = row.MeasuredAt.UTC().Format(time.RFC3339)
		}
		if loss.Samples > 0 {
			entry.LossPercent = loss.LossPercent()
		}
		out.Rows = append(out.Rows, entry)
	}
	return out
}

// handleListServerInspections 返回全部直连机型的最近一次体检与最近 8 次历史。
// 运行监控页轮询该接口；NAT 机型不参与周体检，因此不出现在列表中。
func (s *Server) handleListServerInspections(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	servers, err := s.st.ListServers(ctx)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	items := make([]serverInspectionItemDTO, 0, len(servers))
	for _, srv := range servers {
		if srv.MachineType != store.MachineTypeDirect {
			continue
		}
		item := serverInspectionItemDTO{
			ServerID: srv.ID, ServerAlias: srv.Alias, MachineType: srv.MachineType,
			History: []serverInspectionRunDTO{},
		}
		latest, err := s.st.LatestServerInspectionRun(ctx, srv.ID)
		if err == nil {
			item.Latest = serverInspectionRunDTOFromStore(latest)
		} else if !errors.Is(err, store.ErrNotFound) {
			log.Printf("panel: server %d: read latest inspection: %v", srv.ID, err)
		}
		if runs, err := s.st.ListServerInspectionRuns(ctx, srv.ID, serverInspectionHistoryLimit); err == nil {
			for index := range runs {
				item.History = append(item.History, *serverInspectionRunDTOFromStore(&runs[index]))
			}
		}
		items = append(items, item)
	}
	writeJSON(w, http.StatusOK, serverInspectionsDTO{
		GeneratedAt: time.Now().UTC().Format(time.RFC3339),
		Items:       items,
	})
}

type serverInspectionsDTO struct {
	GeneratedAt string                    `json:"generated_at"`
	Items       []serverInspectionItemDTO `json:"items"`
}

type serverInspectionItemDTO struct {
	ServerID    int64                    `json:"server_id"`
	ServerAlias string                   `json:"server_alias"`
	MachineType string                   `json:"machine_type"`
	Latest      *serverInspectionRunDTO  `json:"latest,omitempty"`
	History     []serverInspectionRunDTO `json:"history"`
}

type serverInspectionRunDTO struct {
	TaskID       string          `json:"task_id"`
	Status       string          `json:"status"`
	StartedAt    string          `json:"started_at"`
	CompletedAt  *string         `json:"completed_at,omitempty"`
	ErrorCode    string          `json:"error_code,omitempty"`
	ErrorMessage string          `json:"error_message,omitempty"`
	Summary      json.RawMessage `json:"summary,omitempty"`
}

func serverInspectionRunDTOFromStore(run *store.ServerInspectionRun) *serverInspectionRunDTO {
	dto := &serverInspectionRunDTO{
		TaskID: run.TaskID, Status: run.Status,
		StartedAt: formatInspectionTime(run.StartedAt),
		ErrorCode: run.ErrorCode, ErrorMessage: run.ErrorMessage,
	}
	if run.CompletedAt != nil {
		value := formatInspectionTime(*run.CompletedAt)
		dto.CompletedAt = &value
	}
	if run.SummaryJSON != "" && json.Valid([]byte(run.SummaryJSON)) {
		dto.Summary = json.RawMessage(run.SummaryJSON)
	}
	return dto
}

func parseInspectionTime(value string) (time.Time, bool) {
	for _, layout := range []string{time.RFC3339Nano, "2006-01-02 15:04:05", "2006-01-02T15:04:05Z"} {
		if parsed, err := time.Parse(layout, value); err == nil {
			return parsed, true
		}
	}
	return time.Time{}, false
}

func formatInspectionTime(value string) string {
	if parsed, ok := parseInspectionTime(value); ok {
		return parsed.UTC().Format(time.RFC3339)
	}
	return value
}
