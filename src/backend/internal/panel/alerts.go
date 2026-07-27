package panel

import (
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"time"
)

// handleTestAlerts 处理 POST /api/settings/alerts/test（§19）：
// 按当前已保存的告警配置向两通道各发一条测试消息，返回各通道成败。
func (s *Server) handleTestAlerts(w http.ResponseWriter, r *http.Request) {
	if s.alerter == nil {
		writeError(w, http.StatusServiceUnavailable, "告警模块未启用")
		return
	}
	result := s.alerter.Test(r.Context())
	action := "settings.alert_test_succeeded"
	for _, channel := range result {
		if channel.Configured && !channel.OK {
			action = "settings.alert_test_failed"
			break
		}
	}
	s.audit(r, action, nil, nil, result)
	writeJSON(w, http.StatusOK, result)
}

// handleBackup 处理 GET /api/backup（§19）：VACUUM INTO 到临时文件后以附件返回，
// 发送完成异步清理临时文件；失败 500。
func (s *Server) handleBackup(w http.ResponseWriter, r *http.Request) {
	tmp := filepath.Join(os.TempDir(), fmt.Sprintf("lattix-backup-%d.db", time.Now().UnixNano()))
	defer os.Remove(tmp) // 响应写出后清理临时文件
	if err := s.st.Backup(r.Context(), tmp); err != nil {
		s.audit(r, "panel.backup_failed", nil, nil, map[string]string{"error": err.Error()})
		writeError(w, http.StatusInternalServerError, "备份失败: "+err.Error())
		return
	}
	f, err := os.Open(tmp)
	if err != nil {
		s.audit(r, "panel.backup_failed", nil, nil, map[string]string{"error": err.Error()})
		writeError(w, http.StatusInternalServerError, "读取备份失败: "+err.Error())
		return
	}
	defer f.Close()
	name := "lattix-backup-" + time.Now().Format("20060102-150405") + ".db"
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%q", name))
	if info, err := f.Stat(); err == nil {
		s.audit(r, "panel.backup_downloaded", nil, nil, map[string]any{
			"bytes": info.Size(), "includes_logs": false,
		})
		http.ServeContent(w, r, name, info.ModTime(), f)
	} else {
		http.ServeContent(w, r, name, time.Time{}, f)
	}
}
