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
// lattix-agent.service，也绝不可触碰真实安装（/usr/local/bin、systemd 单元）。
const installAgentPath = "/usr/local/bin/lattix-agent"

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
	cmd := exec.Command("bash", "-c", script)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true} // 脱离进程组，agent 退出后仍可执行
	if err := cmd.Start(); err != nil {
		log.Printf("uninstall: spawn cleaner failed: %v", err)
	}
	go exitAfter(time.Second)
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
rm -f /usr/local/bin/lattix-agent /usr/local/bin/lattix-agent.bak /usr/local/bin/xray /usr/local/bin/xray.bak
rm -f /etc/lattix-agent.env /etc/lattix-agent.state.json
rm -rf /usr/local/etc/xray
systemctl daemon-reload
`

// uninstallAgentOnlyScript 仅移除 agent，保留 xray 及其配置（节点继续运行，§5）。
const uninstallAgentOnlyScript = `
sleep 2
systemctl disable --now lattix-agent.service
rm -f /etc/systemd/system/lattix-agent.service
rm -f /usr/local/bin/lattix-agent /usr/local/bin/lattix-agent.bak
rm -f /etc/lattix-agent.env /etc/lattix-agent.state.json
systemctl daemon-reload
`
