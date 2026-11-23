// @vitest-environment happy-dom
import { act } from 'react'
import { createRoot } from 'react-dom/client'
import { expect, it } from 'vitest'
import { ThemeProvider } from '@/lib/theme'
import { AppDialogProvider } from './AppDialogProvider'
import { ServerMonitorGrid } from './ServerMonitor'
import type { Server } from '@/lib/types'

Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true })

it('更新中服务器有明确状态，不计入离线异常筛选', async () => {
  const updating = {
    id: 1,
    alias: '升级中的香港节点',
    kind: 'managed',
    connection_state: 'updating',
    addresses: [],
    tags: [],
    country_code: 'HK',
    location: '香港',
    metrics: null,
    billing: { enabled: false, provider: null },
    traffic_plan: { quota_bytes: null, used_bytes: 0, complete: true },
  } as unknown as Server
  const offline = { ...updating, id: 2, alias: '断连节点', connection_state: 'offline' } as Server
  const host = document.createElement('div')
  document.body.append(host)
  const root = createRoot(host)
  const noop = () => {}
  try {
    await act(async () => {
      root.render(
        <ThemeProvider>
          <AppDialogProvider>
            <ServerMonitorGrid
              servers={[updating, offline]}
              samples={[]}
              probes={null}
              loading={false}
              onEdit={noop}
              onRepair={noop}
              onCleanupXray={noop}
              onRebuildXray={noop}
              onRotateToken={noop}
              onUpgrade={noop}
              onRenew={noop}
              onDelete={noop}
            />
          </AppDialogProvider>
        </ThemeProvider>,
      )
    })
    const card = host
      .querySelector('button[aria-label="选择 升级中的香港节点，更新中"]')
      ?.closest('article')
    expect(card).not.toBeNull()
    expect(card?.classList.contains('is-offline')).toBe(false)
    expect(card?.textContent).toContain('更新中')
    const pills = Array.from(host.querySelectorAll<HTMLButtonElement>('.cg-filter-pill'))
    const offlineFilter = pills.find((button) => button.textContent?.includes('离线/异常'))!
    expect(offlineFilter.textContent).toContain('1')
    await act(async () => {
      offlineFilter.click()
    })
    expect(host.textContent).not.toContain('升级中的香港节点')
    expect(host.textContent).toContain('断连节点')
    const updatingFilter = pills.find((button) => button.textContent?.includes('更新中'))!
    await act(async () => {
      updatingFilter.click()
    })
    expect(host.textContent).toContain('升级中的香港节点')
    expect(host.textContent).not.toContain('断连节点')
  } finally {
    await act(async () => {
      root.unmount()
    })
    host.remove()
  }
})
