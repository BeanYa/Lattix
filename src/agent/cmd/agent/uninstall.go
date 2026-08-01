package main

import (
	"log"
	"os"
	"os/exec"
	"strings"
	"syscall"
	"time"

	"lattix/agent/internal/xray"
)

// installAgentPath 是 install.sh 的 agent 安装路径（§11）；只有从该路径运行的
// agent 才允许执行 systemd 卸载脚本——dev/测试运行的 agent 即使宿主机装有
// lattix-agent.service，也绝不可触碰真实安装（/opt/lattix-agent、systemd 单元）。
const installAgentPath = "/opt/lattix-agent/bin/lattix-agent"

// scheduleUninstall 自我卸载（§5 uninstall）：调用方已先行回执，此处延迟自毁。
// install.sh 安装的（运行于 installAgentPath）：purgeXray=true 时连同 xray 与配置
// 一并清除，false 时仅移除 agent（xray 及节点继续运行）。
// 非 install.sh 安装的（dev 手动运行）：purge_xray=true 时停止并移除其管理的 xray
// （-xray-bin/-xray-config 指定，生产同路径）后退出；false 时仅退出进程。
func scheduleUninstall(purgeXray bool, mgr *xray.Manager) {
	exe, err := os.Executable()
	if err != nil {
		exe = ""
	}
	exe = strings.TrimSuffix(exe, " (deleted)") // 运行期间二进制被自升级替换时带此后缀
	if exe != installAgentPath {
		if purgeXray {
			log.Printf("uninstall: 非 install.sh 安装（%s），停止并移除管理的 xray 后退出", exe)
			mgr.PurgeXray()
		} else {
			log.Printf("uninstall: 非 install.sh 安装（%s），仅退出进程", exe)
		}
		go exitAfter(time.Second)
		return
	}
	script := uninstallAgentOnlyScript
	if purgeXray {
		script = uninstallPurgeScript
	}
	log.Printf("uninstall: 开始自卸载（purge_xray=%v）", purgeXray)
	spawnCleaner(script)
	go exitAfter(time.Second)
}

// spawnCleaner 启动卸载清理脚本。systemd 安装场景下必须经 systemd-run 将脚本
// 放进独立的 transient unit：Setsid 只能脱离会话/进程组，无法脱离所在服务的
// cgroup——agent 退出后 systemd 按 KillMode=control-group 清理残留进程，仍在
// sleep 的脚本会被一并杀掉，导致卸载脚本从未执行（服务仍 enabled、
// /opt/lattix-agent 原样保留）。systemd-run 不可用或失败时回退到 setsid 直接
// 派生（非 systemd 托管的守护脚本/手动运行场景仍然有效）。
func spawnCleaner(script string) {
	if _, err := exec.LookPath("systemd-run"); err == nil {
		out, err := exec.Command("systemd-run", "bash", "-c", script).CombinedOutput()
		if err == nil {
			log.Printf("uninstall: 清理脚本已移交 systemd transient unit: %s", strings.TrimSpace(string(out)))
			return
		}
		log.Printf("uninstall: systemd-run 派生失败（%v: %s），回退 setsid", err, strings.TrimSpace(string(out)))
	}
	cmd := exec.Command("bash", "-c", script)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true} // 脱离进程组，agent 退出后仍可执行
	if err := cmd.Start(); err != nil {
		log.Printf("uninstall: spawn cleaner failed: %v", err)
	}
}

func exitAfter(d time.Duration) {
	time.Sleep(d)
	os.Exit(0)
}

// uninstallPurgeScript 与 install.sh 的安装内容互逆（§11）。
const uninstallPurgeScript = `
sleep 2
systemctl disable --now lattix-agent.service
systemctl disable --now xray.service
rm -f /etc/systemd/system/lattix-agent.service /etc/systemd/system/xray.service
rm -f /usr/local/bin/latx-ag
rm -rf /opt/lattix-agent
systemctl daemon-reload
`

// uninstallAgentOnlyScript 仅移除 agent，保留 xray 及其配置（节点继续运行，§5）。
const uninstallAgentOnlyScript = `
sleep 2
systemctl disable --now lattix-agent.service
rm -f /etc/systemd/system/lattix-agent.service
rm -f /usr/local/bin/latx-ag
rm -f /opt/lattix-agent/bin/lattix-agent /opt/lattix-agent/bin/lattix-agent.bak /opt/lattix-agent/bin/latx-ag
rm -f /opt/lattix-agent/config/agent.env
rm -f /opt/lattix-agent/data/state.json /opt/lattix-agent/data/settings.json
systemctl daemon-reload
`
