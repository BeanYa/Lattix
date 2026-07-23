package main

import (
	"log"
	"os"
	"os/exec"
	"syscall"
	"time"
)

// scheduleUninstall 自我卸载（§5 uninstall）：调用方已先行回执，此处延迟自毁。
// install.sh 安装的（systemd 托管）：detached 脚本清理服务、二进制与配置；
// 非 install.sh 安装的（dev 手动运行）：仅退出进程。
func scheduleUninstall() {
	if _, err := os.Stat("/etc/systemd/system/lattix-agent.service"); err != nil {
		log.Printf("uninstall: 非 install.sh 安装，仅退出进程")
		go exitAfter(time.Second)
		return
	}
	log.Printf("uninstall: 开始自卸载")
	cmd := exec.Command("bash", "-c", uninstallScript)
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

// uninstallScript 与 install.sh 的安装内容互逆（§11）。
const uninstallScript = `
sleep 2
systemctl disable --now lattix-agent.service
systemctl disable --now xray.service
rm -f /etc/systemd/system/lattix-agent.service /etc/systemd/system/xray.service
rm -f /usr/local/bin/lattix-agent /usr/local/bin/xray
rm -f /etc/lattix-agent.env /etc/lattix-agent.state.json
rm -rf /usr/local/etc/xray
systemctl daemon-reload
`
