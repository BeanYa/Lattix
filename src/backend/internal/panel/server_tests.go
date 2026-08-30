package panel

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"strings"

	"lattix/backend/internal/cdncatalog"
	"lattix/backend/internal/store"
	"lattix/backend/internal/testcatalog"
	"lattix/shared"
)

type runServerTestRequest struct {
	ServerID   int64                       `json:"server_id"`
	Categories []shared.ServerTestCategory `json:"categories"`
}

type serverTestDTO struct {
	ServerID       int64                             `json:"server_id"`
	TaskID         string                            `json:"task_id"`
	Generation     int64                             `json:"generation"`
	Status         shared.ServerTestTaskStatus       `json:"status"`
	Categories     []shared.ServerTestCategory       `json:"categories"`
	CatalogVersion string                            `json:"catalog_version"`
	CatalogHashes  map[string]string                 `json:"catalog_hashes"`
	Progress       *shared.ServerTestProgressPayload `json:"progress,omitempty"`
	Result         *shared.ServerTestReport          `json:"result,omitempty"`
	ErrorCode      string                            `json:"error_code,omitempty"`
	ErrorMessage   string                            `json:"error_message,omitempty"`
	AgentVersion   string                            `json:"agent_version,omitempty"`
	CreatedAt      string                            `json:"created_at"`
	AcceptedAt     *string                           `json:"accepted_at,omitempty"`
	StartedAt      *string                           `json:"started_at,omitempty"`
	CompletedAt    *string                           `json:"completed_at,omitempty"`
	UpdatedAt      string                            `json:"updated_at"`
}

func (s *Server) handleRunServerTest(w http.ResponseWriter, r *http.Request) {
	var request runServerTestRequest
	if err := readJSON(r, &request); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if request.ServerID < 1 {
		writeError(w, http.StatusBadRequest, "invalid server id")
		return
	}
	if _, err := s.st.ServerByID(r.Context(), request.ServerID); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeError(w, http.StatusNotFound, "服务器不存在")
			return
		}
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if err := validateServerTestCategories(request.Categories); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	catalog, err := s.serverTestCatalogSnapshot(r.Context(), request.Categories)
	if err != nil {
		writeError(w, http.StatusServiceUnavailable, err.Error())
		return
	}
	task, err := s.disp.EnqueueServerTest(r.Context(), request.ServerID, request.Categories, catalog)
	if errors.Is(err, store.ErrServerTestInProgress) {
		writeError(w, http.StatusConflict, "该服务器已有测试任务正在运行")
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusAccepted, s.serverTestDTO(task))
}

func (s *Server) handleGetServerTest(w http.ResponseWriter, r *http.Request) {
	serverID, err := strconv.ParseInt(r.URL.Query().Get("server_id"), 10, 64)
	if err != nil || serverID < 1 {
		writeError(w, http.StatusBadRequest, "invalid server id")
		return
	}
	task, err := s.st.ServerTestByServerID(r.Context(), serverID)
	if errors.Is(err, store.ErrNotFound) {
		writeJSON(w, http.StatusOK, nil)
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, s.serverTestDTO(task))
}

func (s *Server) serverTestDTO(task *store.ServerTestTask) serverTestDTO {
	return serverTestDTO{
		ServerID: task.ServerID, TaskID: task.TaskID, Generation: task.Generation,
		Status: task.Status, Categories: task.Categories,
		CatalogVersion: task.CatalogVersion, CatalogHashes: task.CatalogHashes,
		Progress: s.disp.ServerTestProgress(task.ServerID), Result: task.Result,
		ErrorCode: task.ErrorCode, ErrorMessage: task.ErrorMessage, AgentVersion: task.AgentVersion,
		CreatedAt: task.CreatedAt, AcceptedAt: task.AcceptedAt, StartedAt: task.StartedAt,
		CompletedAt: task.CompletedAt, UpdatedAt: task.UpdatedAt,
	}
}

func (s *Server) handleCDNCatalogStatus(w http.ResponseWriter, r *http.Request) {
	status, err := s.cdn.Status(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, status)
}

func (s *Server) handleRefreshCDNCatalog(w http.ResponseWriter, r *http.Request) {
	err := s.cdn.Refresh(r.Context())
	status, statusErr := s.cdn.Status(r.Context())
	if statusErr != nil {
		writeError(w, http.StatusInternalServerError, statusErr.Error())
		return
	}
	if err != nil {
		writeRPC(w, shared.CodeUpstreamError, err.Error(), status)
		return
	}
	writeJSON(w, http.StatusOK, status)
}

func validateServerTestCategories(categories []shared.ServerTestCategory) error {
	if len(categories) == 0 || len(categories) > len(shared.ServerTestCategories()) {
		return errors.New("至少选择一个测试项目")
	}
	seen := make(map[shared.ServerTestCategory]struct{}, len(categories))
	for _, category := range categories {
		if !category.Valid() {
			return fmt.Errorf("未知测试项目 %q", category)
		}
		if _, exists := seen[category]; exists {
			return fmt.Errorf("测试项目 %q 重复", category)
		}
		seen[category] = struct{}{}
	}
	return nil
}

func (s *Server) serverTestCatalogSnapshot(ctx context.Context, categories []shared.ServerTestCategory) (shared.ServerTestCatalogSnapshot, error) {
	raw, err := s.st.GetSetting(ctx, store.SettingCDNNodeCatalog)
	if err != nil {
		return shared.ServerTestCatalogSnapshot{}, err
	}
	if raw == "" {
		status, _ := s.cdn.Status(ctx)
		if status.LastError != "" {
			return shared.ServerTestCatalogSnapshot{}, fmt.Errorf("测试节点目录不可用：%s", status.LastError)
		}
		return shared.ServerTestCatalogSnapshot{}, errors.New("测试节点目录尚未完成首次抓取，请先刷新目录")
	}
	var document cdncatalog.Document
	if err := json.Unmarshal([]byte(raw), &document); err != nil {
		return shared.ServerTestCatalogSnapshot{}, fmt.Errorf("测试节点目录解析失败：%w", err)
	}
	if document.Version != cdncatalog.SchemaVersion || len(document.Provinces) == 0 || document.Source.CatalogSHA256 == "" {
		return shared.ServerTestCatalogSnapshot{}, errors.New("测试节点目录无有效缓存")
	}
	selected := make(map[shared.ServerTestCategory]bool, len(categories))
	for _, category := range categories {
		selected[category] = true
	}
	var targets []shared.ServerTestTarget
	staticCatalog, err := testcatalog.Load()
	if err != nil {
		return shared.ServerTestCatalogSnapshot{}, fmt.Errorf("加载静态测试目录失败：%w", err)
	}
	for _, province := range document.Provinces {
		for carrierKey, endpoints := range map[string]cdncatalog.ProtocolEndpoints{
			"telecom": province.Carriers.Telecom,
			"unicom":  province.Carriers.Unicom,
			"mobile":  province.Carriers.Mobile,
		} {
			if selected[shared.ServerTestTCPIPv4] {
				targets = append(targets, catalogEndpointTarget(shared.ServerTestTCPIPv4, province, carrierKey, endpoints.IPv4))
			}
			if selected[shared.ServerTestTCPIPv6] {
				targets = append(targets, catalogEndpointTarget(shared.ServerTestTCPIPv6, province, carrierKey, endpoints.IPv6))
			}
			if selected[shared.ServerTestLargePacketIPv4] {
				targets = append(targets, catalogEndpointTarget(shared.ServerTestLargePacketIPv4, province, carrierKey, endpoints.IPv4))
			}
			if selected[shared.ServerTestReturnRouteIPv4] {
				targets = append(targets, catalogEndpointTarget(shared.ServerTestReturnRouteIPv4, province, carrierKey, endpoints.IPv4))
			}
			if selected[shared.ServerTestReturnRouteIPv6] {
				targets = append(targets, catalogEndpointTarget(shared.ServerTestReturnRouteIPv6, province, carrierKey, endpoints.IPv6))
			}
		}
	}
	if selected[shared.ServerTestCERNETIPv4] || selected[shared.ServerTestCERNET2IPv6] {
		for index, target := range staticCatalog.Education {
			if selected[shared.ServerTestCERNETIPv4] {
				targets = append(targets, shared.ServerTestTarget{
					ID: fmt.Sprintf("cernet:%02d", index+1), Category: shared.ServerTestCERNETIPv4,
					Label: target.Province + "教育网", Province: target.Province,
					AddressFamily: shared.ServerTestIPv4, Host: target.Host, Port: 443, Source: "education",
				})
			}
			if selected[shared.ServerTestCERNET2IPv6] {
				targets = append(targets, shared.ServerTestTarget{
					ID: fmt.Sprintf("cernet2:%02d", index+1), Category: shared.ServerTestCERNET2IPv6,
					Label: target.Province + "CERNET2", Province: target.Province,
					AddressFamily: shared.ServerTestIPv6, Host: target.Host, Port: 443, Source: "education",
				})
			}
		}
	}
	if selected[shared.ServerTestInternational] {
		for index, target := range staticCatalog.International {
			targets = append(targets, shared.ServerTestTarget{
				ID: fmt.Sprintf("international:%02d", index+1), Category: shared.ServerTestInternational,
				Label: target.Label, AddressFamily: shared.ServerTestIPv4,
				Host: target.Host, Port: 443, Source: "international",
			})
		}
	}
	if selected[shared.ServerTestSpeed] {
		for _, target := range staticCatalog.Speed {
			family := shared.ServerTestIPv4
			if target.Family == "ipv6" {
				family = shared.ServerTestIPv6
			}
			targets = append(targets, shared.ServerTestTarget{
				ID: "speed:" + target.ID, Category: shared.ServerTestSpeed, Label: target.Label,
				AddressFamily: family, Host: target.Host, Port: 443, SNI: target.SNI, Source: "speed",
				Path: target.Path, UploadPath: target.UploadPath, OoklaServerID: target.OoklaServerID,
			})
		}
	}
	hashes := map[string]string{"zstatic": document.Source.CatalogSHA256}
	versionParts := []string{"zstatic-v1:" + document.Source.CatalogSHA256[:12]}
	for name, selectedCatalog := range map[string]bool{
		"education":     selected[shared.ServerTestCERNETIPv4] || selected[shared.ServerTestCERNET2IPv6],
		"international": selected[shared.ServerTestInternational],
		"speed":         selected[shared.ServerTestSpeed],
	} {
		if selectedCatalog {
			hashes[name] = staticCatalog.Hashes[name]
			versionParts = append(versionParts, name+":"+staticCatalog.Hashes[name][:12])
		}
	}
	sort.Strings(versionParts)
	return shared.ServerTestCatalogSnapshot{
		Version: strings.Join(versionParts, "+"),
		Hashes:  hashes,
		Targets: targets,
	}, nil
}

func catalogEndpointTarget(category shared.ServerTestCategory, province cdncatalog.Province, carrier string, endpoint cdncatalog.Endpoint) shared.ServerTestTarget {
	target := shared.ServerTestTarget{
		ID:       strings.Join([]string{string(category), province.Code, carrier}, ":"),
		Category: category, Label: endpoint.Label, Carrier: carrier, Province: province.Name,
		AddressFamily: shared.ServerTestAddressFamily(endpoint.AddressFamily),
		Host:          endpoint.Host, Port: endpoint.Port, Source: "zstatic",
	}
	if endpoint.Backup != nil {
		target.Backup = &shared.ServerTestTarget{
			ID: target.ID + ":backup", Category: category, Label: endpoint.Backup.Label,
			Carrier: carrier, Province: province.Name,
			AddressFamily: shared.ServerTestAddressFamily(endpoint.Backup.AddressFamily),
			Host:          endpoint.Backup.Host, Port: endpoint.Backup.Port, Source: "zstatic",
		}
	}
	return target
}
