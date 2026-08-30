import { useState } from 'react'

import { api } from '@/lib/api'
import { useAppDialog } from '@/lib/app-dialog'

/** 重启面板进程并轮询等待恢复；恢复后整页刷新（设置/运行态可能已变化）。 */
export function usePanelRestart({ onError }: { onError: (message: string) => void }) {
  const { confirm } = useAppDialog()
  const [restarting, setRestarting] = useState(false)

  const onRestart = async () => {
    if (
      !(await confirm({
        title: '重启面板',
        description: '确定重启面板进程？重启期间面板会短暂不可用（数秒）。',
        confirmLabel: '重启面板',
      }))
    ) {
      return
    }
    setRestarting(true)
    try {
      await api.restartPanel()
    } catch {
      // 进程退出导致连接中断属预期
    }
    const deadline = Date.now() + 30000
    while (Date.now() < deadline) {
      await new Promise((r) => setTimeout(r, 1500))
      try {
        await api.me()
        window.location.reload()
        return
      } catch {
        // 尚未恢复，继续等
      }
    }
    setRestarting(false)
    onError('等待重启完成超时。若切换了 HTTP/HTTPS 或端口，请改用新地址访问面板。')
  }

  return { restarting, onRestart }
}

export type PanelRestartController = ReturnType<typeof usePanelRestart>
