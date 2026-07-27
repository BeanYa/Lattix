package panel

import (
	"log"
	"net/http"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// handleRestart 处理 POST /api/settings/restart：应答后延迟重启面板进程。
// 用于设置页 TLS 变更（重启生效项）与后续面板自更新：进程退出后监听按最新 DB 设置重建。
func (s *Server) handleRestart(w http.ResponseWriter, r *http.Request) {
	if err := readJSON(r, &struct{}{}); err != nil {
		writeProtocolError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	// 在进程重启前留痕（异步重启会终止当前进程，事后无法补记）。
	s.audit(r, "panel.restart_requested", nil, nil, nil)
	writeJSON(w, http.StatusAccepted, map[string]string{"status": "restarting"})
	if f, ok := w.(http.Flusher); ok {
		f.Flush()
	}
	go func() {
		// 留出让响应到达客户端的窗口。
		time.Sleep(500 * time.Millisecond)
		if s.cfg.RequestRestart == nil {
			log.Printf("restart: 重启回调未配置")
			return
		}
		s.cfg.RequestRestart("manual")
	}()
}

// RestartSelf 重启当前进程：
//   - systemd 或 Docker 托管：直接退出，由 Restart= / restart policy 拉起；
//   - 否则自派生一个脱离会话的新进程（同二进制同参数），随后退出。
//     新进程经 LATTIX_RESTART_WAIT_PID 等待本进程退出释放端口后再启动（见 main）。
//
// 二进制在运行期间被替换（面板更新场景）时，os.Executable 会带 " (deleted)" 后缀，
// 去掉后执行原路径，即拿到更新后的二进制。
func RestartSelf() error {
	if os.Getenv("LATTIX_DEPLOY_MODE") == "docker" {
		log.Printf("restart: Docker 托管，退出等待容器 restart policy 拉起")
		os.Exit(0)
	}
	if os.Getenv("INVOCATION_ID") != "" {
		log.Printf("restart: systemd 托管，退出等待拉起")
		os.Exit(0)
	}
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	exe = strings.TrimSuffix(exe, " (deleted)")
	// Atomic self-update renames the running inode to <binary>.bak before putting
	// the new binary at the original path. /proc/self/exe therefore resolves to
	// the backup name until this process exits; restart the original path.
	if strings.HasSuffix(exe, ".bak") {
		if current := strings.TrimSuffix(exe, ".bak"); fileExists(current) {
			exe = current
		}
	}
	cmd := exec.Command(exe, os.Args[1:]...)
	cmd.Env = append(os.Environ(), "LATTIX_RESTART_WAIT_PID="+strconv.Itoa(os.Getpid()))
	if wd, err := os.Getwd(); err == nil {
		cmd.Dir = wd
	}
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := cmd.Start(); err != nil {
		return err
	}
	log.Printf("restart: 新进程 %d 已派生，本进程退出", cmd.Process.Pid)
	os.Exit(0)
	return nil // 不可达
}
