package main

import (
	"log"
	"os"
	"os/exec"
	"syscall"
	"time"
)

// scheduleUninstall 自我卸载（§5 uninstall）：调用方已先行回执，此处延迟自毁。
// purgeXray=true 时连同 install.sh 安装的 xray 与配置一并清除；
// false 时仅移除 agent（xray 及节点继续运行）。
// 非 install.sh 安装的（dev 手动运行）：仅退出进程。
func scheduleUninstall(purgeXray bool) {
	if _, err := os.Stat("/etc/systemd/system/lattix-agent.service"); err != nil {
		log.Printf("uninstall: 非 install.sh 安装，仅退出进程")
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
rm -f /usr/local/bin/lattix-agent /usr/local/bin/xray
rm -f /etc/lattix-agent.env /etc/lattix-agent.state.json
rm -rf /usr/local/etc/xray
systemctl daemon-reload
`

// uninstallAgentOnlyScript 仅移除 agent，保留 xray 及其配置（节点继续运行，§5）。
const uninstallAgentOnlyScript = `
sleep 2
systemctl disable --now lattix-agent.service
rm -f /etc/systemd/system/lattix-agent.service
rm -f /usr/local/bin/lattix-agent
rm -f /etc/lattix-agent.env /etc/lattix-agent.state.json
systemctl daemon-reload
`
