package main

import (
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"lattix/agent/internal/xray"
)

// 测试可替换的进程/文件系统钩子（P5）：生产默认走真实 exec/os。
var (
	lookPathFn = exec.LookPath
	runCmdFn   = func(name string, args ...string) ([]byte, error) {
		return exec.Command(name, args...).CombinedOutput()
	}
	startSetsidFn = func(script string) (int, error) {
		cmd := exec.Command("bash", "-c", script)
		cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
		if err := cmd.Start(); err != nil {
			return 0, err
		}
		return cmd.Process.Pid, nil
	}
	writeFileFn = os.WriteFile
	removeFn    = os.Remove
	statFn      = os.Stat
)

// cleanerSpawnMethod 记录 cleaner 实际采用的派生方式（测试断言用）。
type cleanerSpawnMethod string

const (
	spawnSystemdRun    cleanerSpawnMethod = "systemd-run"
	spawnSystemdOneshot cleanerSpawnMethod = "systemd-oneshot"
	spawnSetsid        cleanerSpawnMethod = "setsid"
	spawnFailed        cleanerSpawnMethod = "failed"
)

// scheduleUninstall 自我卸载（§5 uninstall）：调用方已先行回执，此处延迟自毁。
//
// install.sh 安装的 agent（可识别的 APP_ROOT/bin/lattix-agent）：
//   - 系统路径（/opt 或 $PREFIX/opt）：经独立 systemd unit 跑清理脚本
//     （systemd-run → /run oneshot → setsid 三级回退），避免 KillMode=control-group
//     在 agent 退出时杀掉 cleaner；脚本先 disable --now 再删文件，压制 Restart=always。
//   - 用户态（$HOME/.lattix-agent）：setsid 停 runner/crontab 后删文件。
//   - purgeXray=true 时连同 xray 与配置清除。
//
// 非 install.sh 安装（dev 手动运行）：不触碰宿主机真实安装；purge 时仅停并移除本进程
// 管理的 xray（-xray-bin/-xray-config），否则仅退出。
func scheduleUninstall(purgeXray bool, mgr *xray.Manager) {
	exe := resolveAgentExecutable()
	root, ok := managedInstallRoot(exe)
	if !ok {
		if purgeXray {
			log.Printf("uninstall: 非 install.sh 安装（%s），停止并移除管理的 xray 后退出", exe)
			mgr.PurgeXray()
		} else {
			log.Printf("uninstall: 非 install.sh 安装（%s），仅退出进程", exe)
		}
		go exitAfter(time.Second)
		return
	}

	paths := installPathsFor(root)
	useSystemd := paths.SystemStyle
	script := buildUninstallScript(paths, useSystemd, purgeXray)
	log.Printf("uninstall: 开始自卸载（root=%s systemd=%v purge_xray=%v）", root, useSystemd, purgeXray)
	method := spawnCleaner(script, useSystemd)
	log.Printf("uninstall: cleaner 派生方式=%s", method)
	go exitAfter(time.Second)
}

func resolveAgentExecutable() string {
	exe, err := os.Executable()
	if err != nil {
		return ""
	}
	exe = strings.TrimSuffix(exe, " (deleted)")
	if resolved, err := filepath.EvalSymlinks(exe); err == nil {
		return resolved
	}
	return exe
}

// managedInstallRoot 识别 install.sh 安装树：…/lattix-agent/bin/lattix-agent 或
// …/.lattix-agent/bin/lattix-agent。dev 随意路径返回 false，避免误删生产安装。
func managedInstallRoot(exe string) (string, bool) {
	if exe == "" {
		return "", false
	}
	exe = filepath.Clean(exe)
	if filepath.Base(exe) != "lattix-agent" {
		return "", false
	}
	binDir := filepath.Dir(exe)
	if filepath.Base(binDir) != "bin" {
		return "", false
	}
	root := filepath.Dir(binDir)
	switch filepath.Base(root) {
	case "lattix-agent", ".lattix-agent":
		return root, true
	default:
		return "", false
	}
}

// installPaths 是一次 install.sh 安装的关键路径（与 latx-ag / install-agent.sh 对齐）。
type installPaths struct {
	Root           string
	Prefix         string // LATX_PREFIX；标准 /opt 安装为空串
	SystemStyle    bool   // true = PREFIX/opt/lattix-agent 或 /opt/lattix-agent（走 systemd）
	AgentBin       string
	AgentBinBak    string
	RunScript      string
	LatxAgApp      string
	LatxAgLink     string // 系统安装的 /usr/local/bin/latx-ag；用户态为空
	EnvFile        string
	StateFile      string
	SettingsFile   string
	ConnectionFile string
	CommandQueue   string
	LockFile       string
	LockDir        string
	AgentLog       string
	XrayBin        string
	XrayBinBak     string
	XrayConfig     string
	XrayConfigBak  string
	UnitFile       string
	XrayUnitFile   string
	UnitName       string
	XrayUnitName   string
}

func installPathsFor(root string) installPaths {
	root = filepath.Clean(root)
	prefix := ""
	systemStyle := false
	suffix := string(filepath.Separator) + "opt" + string(filepath.Separator) + "lattix-agent"
	if rest, ok := strings.CutSuffix(root, suffix); ok {
		prefix = rest
		systemStyle = true
	} else if root == "/opt/lattix-agent" {
		systemStyle = true
	}

	bin := filepath.Join(root, "bin")
	data := filepath.Join(root, "data")
	config := filepath.Join(root, "config")
	logs := filepath.Join(root, "logs")
	p := installPaths{
		Root:           root,
		Prefix:         prefix,
		SystemStyle:    systemStyle,
		AgentBin:       filepath.Join(bin, "lattix-agent"),
		AgentBinBak:    filepath.Join(bin, "lattix-agent.bak"),
		RunScript:      filepath.Join(bin, "lattix-agent-run"),
		LatxAgApp:      filepath.Join(bin, "latx-ag"),
		EnvFile:        filepath.Join(config, "agent.env"),
		StateFile:      filepath.Join(data, "state.json"),
		SettingsFile:   filepath.Join(data, "settings.json"),
		ConnectionFile: filepath.Join(data, "connection.json"),
		CommandQueue:   filepath.Join(data, "command-queue.json"),
		LockFile:       filepath.Join(data, "lattix-agent.lock"),
		LockDir:        filepath.Join(data, "lattix-agent.lock.d"),
		AgentLog:       filepath.Join(logs, "agent.log"),
		XrayBin:        filepath.Join(bin, "xray"),
		XrayBinBak:     filepath.Join(bin, "xray.bak"),
		XrayConfig:     filepath.Join(config, "xray.json"),
		XrayConfigBak:  filepath.Join(config, "xray.json.rebind-backup"),
		UnitName:       "lattix-agent",
		XrayUnitName:   "xray",
	}
	if systemStyle {
		p.LatxAgLink = prefix + "/usr/local/bin/latx-ag"
		p.UnitFile = prefix + "/etc/systemd/system/lattix-agent.service"
		p.XrayUnitFile = prefix + "/etc/systemd/system/xray.service"
	}
	return p
}

func shellQuote(s string) string {
	if s == "" {
		return "''"
	}
	return "'" + strings.ReplaceAll(s, "'", `'"'"'`) + "'"
}

// buildUninstallScript 生成与 latx-ag uninstall 对齐的清理脚本。
// 先 stop/disable（或 pkill runner），再删文件——避免 Restart=always 在删文件前把 agent 拉回。
func buildUninstallScript(p installPaths, useSystemd, purgeXray bool) string {
	var b strings.Builder
	b.WriteString("set +e\n") // 清理步骤尽力而为，单步失败不中断
	if useSystemd {
		// 立即 disable --now，抢在 RestartSec=1 的自动拉起之前关闭服务（无前置 sleep）。
		b.WriteString(fmt.Sprintf("systemctl disable --now %s.service 2>/dev/null\n", p.UnitName))
		if purgeXray {
			b.WriteString(fmt.Sprintf("systemctl disable --now %s.service 2>/dev/null\n", p.XrayUnitName))
		}
		if p.UnitFile != "" {
			b.WriteString(fmt.Sprintf("rm -f %s\n", shellQuote(p.UnitFile)))
		}
		if purgeXray && p.XrayUnitFile != "" {
			b.WriteString(fmt.Sprintf("rm -f %s\n", shellQuote(p.XrayUnitFile)))
		}
		b.WriteString("systemctl daemon-reload 2>/dev/null\n")
	} else {
		// 用户态：必须先杀 runner，否则 agent 退出后 1s 会被重新拉起。
		b.WriteString(fmt.Sprintf("pkill -f %s 2>/dev/null\n", shellQuote(p.RunScript)))
		b.WriteString(fmt.Sprintf("pkill -f %s 2>/dev/null\n", shellQuote(p.AgentBin+" -panel")))
		b.WriteString(fmt.Sprintf(
			"if command -v crontab >/dev/null 2>&1 && crontab -l 2>/dev/null | grep -qF %s; then\n",
			shellQuote(p.RunScript)))
		b.WriteString(fmt.Sprintf(
			"  crontab -l 2>/dev/null | { grep -vF %s || true; } | crontab -\n",
			shellQuote(p.RunScript)))
		b.WriteString("fi\n")
		if purgeXray {
			b.WriteString(fmt.Sprintf("pkill -f %s 2>/dev/null\n", shellQuote(p.XrayBin+" run")))
		}
	}

	// 与 latx-ag uninstall 文件清单对齐（含 connection/queue/lock/log/bak/run）。
	b.WriteString(fmt.Sprintf(
		"rm -f %s %s %s %s %s %s %s %s %s %s\n",
		shellQuote(p.AgentBin), shellQuote(p.AgentBinBak), shellQuote(p.RunScript),
		shellQuote(p.EnvFile), shellQuote(p.StateFile), shellQuote(p.SettingsFile),
		shellQuote(p.ConnectionFile), shellQuote(p.CommandQueue),
		shellQuote(p.LockFile), shellQuote(p.AgentLog),
	))
	b.WriteString(fmt.Sprintf("rm -rf %s 2>/dev/null\n", shellQuote(p.LockDir)))
	b.WriteString(fmt.Sprintf("rm -f %s\n", shellQuote(p.LatxAgApp)))
	if p.LatxAgLink != "" {
		b.WriteString(fmt.Sprintf("rm -f %s\n", shellQuote(p.LatxAgLink)))
	}
	if purgeXray {
		b.WriteString(fmt.Sprintf(
			"rm -f %s %s %s %s\n",
			shellQuote(p.XrayBin), shellQuote(p.XrayBinBak),
			shellQuote(p.XrayConfig), shellQuote(p.XrayConfigBak),
		))
		b.WriteString(fmt.Sprintf("rm -rf %s 2>/dev/null\n", shellQuote(p.Root)))
	}
	return b.String()
}

// spawnCleaner 启动卸载清理脚本，返回实际派生方式。
//
// 系统安装三级回退（P3）：
//  1. systemd-run 独立 transient unit（首选）
//  2. 写入 /run/systemd/system oneshot + systemctl start --no-block
//     （systemd-run 不可用/失败时仍脱离 agent service cgroup）
//  3. setsid（最后手段；在 systemd KillMode=control-group 下可能被杀掉，打警告）
//
// 用户态：直接 setsid（agent 本就不在 lattix-agent.service cgroup 下）。
func spawnCleaner(script string, preferSystemdIsolation bool) cleanerSpawnMethod {
	if preferSystemdIsolation {
		if method, err := spawnViaSystemdRun(script); err == nil {
			return method
		} else {
			log.Printf("uninstall: systemd-run 不可用/失败: %v", err)
		}
		if method, err := spawnViaSystemdOneshot(script); err == nil {
			return method
		} else {
			log.Printf("uninstall: systemd oneshot unit 派生失败: %v", err)
		}
		log.Printf("uninstall: 警告：回退 setsid；在 systemd KillMode=control-group 下 cleaner 仍可能被随 service 杀掉")
	}
	pid, err := startSetsidFn(script)
	if err != nil {
		log.Printf("uninstall: spawn cleaner failed: %v", err)
		return spawnFailed
	}
	log.Printf("uninstall: 清理脚本已 setsid 派生 pid=%d", pid)
	return spawnSetsid
}

func spawnViaSystemdRun(script string) (cleanerSpawnMethod, error) {
	if _, err := lookPathFn("systemd-run"); err != nil {
		return "", fmt.Errorf("systemd-run not in PATH: %w", err)
	}
	unit := fmt.Sprintf("lattix-agent-uninstall-%d", os.Getpid())
	out, err := runCmdFn("systemd-run",
		"--no-block",
		"--collect",
		"--unit="+unit,
		"--description=Lattix Agent uninstall cleaner",
		"bash", "-c", script,
	)
	if err != nil {
		return "", fmt.Errorf("%w: %s", err, strings.TrimSpace(string(out)))
	}
	log.Printf("uninstall: 清理脚本已移交 systemd-run unit %s: %s",
		unit, strings.TrimSpace(string(out)))
	return spawnSystemdRun, nil
}

// spawnViaSystemdOneshot 在 /run/systemd/system 落盘 oneshot unit 并异步启动。
// 不依赖 systemd-run 二进制；unit 与 agent service 分属不同 cgroup，存活不受
// lattix-agent.service 停止影响。
func spawnViaSystemdOneshot(script string) (cleanerSpawnMethod, error) {
	if _, err := lookPathFn("systemctl"); err != nil {
		return "", fmt.Errorf("systemctl not in PATH: %w", err)
	}
	// /run/systemd/system 仅 root 可写；无权限时自然失败并回退。
	runDir := "/run/systemd/system"
	if _, err := statFn(runDir); err != nil {
		return "", fmt.Errorf("runtime unit dir: %w", err)
	}
	id := os.Getpid()
	scriptPath := fmt.Sprintf("/run/lattix-agent-uninstall-%d.sh", id)
	unitName := fmt.Sprintf("lattix-agent-uninstall-%d.service", id)
	unitPath := filepath.Join(runDir, unitName)

	// 脚本末尾自清理 transient 文件，避免 /run 残留。
	fullScript := script + fmt.Sprintf(
		"\nrm -f %s %s\n", shellQuote(scriptPath), shellQuote(unitPath),
	)
	if err := writeFileFn(scriptPath, []byte("#!/bin/bash\n"+fullScript), 0o700); err != nil {
		return "", fmt.Errorf("write cleaner script: %w", err)
	}
	unitBody := fmt.Sprintf(`[Unit]
Description=Lattix Agent uninstall cleaner
After=network.target

[Service]
Type=oneshot
ExecStart=/bin/bash %s
`, scriptPath)
	if err := writeFileFn(unitPath, []byte(unitBody), 0o644); err != nil {
		_ = removeFn(scriptPath)
		return "", fmt.Errorf("write oneshot unit: %w", err)
	}
	if out, err := runCmdFn("systemctl", "daemon-reload"); err != nil {
		_ = removeFn(scriptPath)
		_ = removeFn(unitPath)
		return "", fmt.Errorf("daemon-reload: %w: %s", err, strings.TrimSpace(string(out)))
	}
	if out, err := runCmdFn("systemctl", "start", "--no-block", unitName); err != nil {
		_ = removeFn(scriptPath)
		_ = removeFn(unitPath)
		_, _ = runCmdFn("systemctl", "daemon-reload")
		return "", fmt.Errorf("start oneshot: %w: %s", err, strings.TrimSpace(string(out)))
	}
	log.Printf("uninstall: 清理脚本已移交 systemd oneshot unit %s", unitName)
	return spawnSystemdOneshot, nil
}

func exitAfter(d time.Duration) {
	time.Sleep(d)
	os.Exit(0)
}
