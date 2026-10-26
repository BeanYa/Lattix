// @vitest-environment happy-dom
import { act } from 'react'
import { createRoot, type Root } from 'react-dom/client'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { Router } from 'wouter'

import { AppShell } from './AppShell'
import { api } from '@/lib/api'
import { AuthContext } from '@/lib/auth-context'
import { ThemeProvider } from '@/lib/theme'
import type { PanelUpdateStatus } from '@/lib/types'

// Keep the shell, updater and requester real; only the remote panel is simulated.
const idle = (version: string): PanelUpdateStatus => ({
  running: false,
  stage: '',
  percent: 0,
  message: '',
  current_version: version,
  target_version: '',
})
const running: PanelUpdateStatus = {
  running: true,
  stage: 'download',
  percent: 42,
  message: '下载更新包',
  current_version: 'v0.1.13',
  target_version: 'v0.1.14',
}

let status: PanelUpdateStatus
let offline: boolean
let root: Root
let container: HTMLDivElement

beforeEach(async () => {
  vi.useFakeTimers()
  vi.stubGlobal('IS_REACT_ACT_ENVIRONMENT', true)
  status = idle('v0.1.13')
  offline = false
  vi.stubGlobal('fetch', async (input: string, init?: RequestInit) => {
    if (offline) throw new TypeError('panel restarting')
    const path = new URL(input, 'http://localhost').pathname
    let data: unknown
    switch (path) {
      case '/api/panel/start-update':
        if (init?.method !== 'POST') throw new Error('expected POST')
        status = { ...running }
        data = status
        break
      case '/api/panel/get-update-status':
        data = status
        break
      case '/api/auth/me':
        data = { username: 'admin', csrf_token: 'test-csrf' }
        break
      case '/api/panel/state':
        data = {
          panel_instance_id: 'test-panel',
          state: 'active',
          epoch: 'test',
          revision: 1,
          entered_at: '2026-10-26T00:00:00Z',
        }
        break
      case '/api/server/list':
      case '/api/chain/list':
      case '/api/user/list':
        data = []
        break
      default:
        throw new Error(`unexpected request: ${path}`)
    }
    return new Response(
      JSON.stringify({
        code: 'OK',
        message: '',
        data,
        request_id: 'a'.repeat(32),
        trace_id: 'b'.repeat(32),
      }),
      { headers: { 'Content-Type': 'application/json' } },
    )
  })
  container = document.createElement('div')
  document.body.append(container)
  root = createRoot(container)
  await act(async () => {
    root.render(
      <Router ssrPath="/settings/system">
        <AuthContext.Provider
          value={{
            username: 'admin',
            loading: false,
            login: async () => {},
            logout: async () => {},
          }}
        >
          <ThemeProvider>
            <AppShell>
              <p>设置页面</p>
            </AppShell>
          </ThemeProvider>
        </AuthContext.Provider>
      </Router>,
    )
  })
})

afterEach(async () => {
  await act(async () => root.unmount())
  container.remove()
  vi.restoreAllMocks()
  vi.unstubAllGlobals()
  vi.useRealTimers()
})

async function advance(ms: number) {
  await act(async () => {
    await vi.advanceTimersByTimeAsync(ms)
  })
}

async function start() {
  await act(async () => {
    await api.startPanelUpdate('v0.1.14')
  })
  await advance(300)
}

describe('panel updates in the active application shell', () => {
  it('shows actual update progress globally after the update starts', async () => {
    expect(container.textContent).not.toContain('面板更新中')
    await start()
    expect(container.textContent).toContain('面板更新中')
    expect(container.textContent).toContain('42%')
    expect(container.textContent).toContain('v0.1.13 → v0.1.14')
  })

  it('reloads the page with the new version even when the restart occurs between polls', async () => {
    const reload = vi.spyOn(window.location, 'reload').mockImplementation(() => {})
    await start()
    status = idle('v0.1.14')
    await advance(300)
    expect(container.textContent).toContain('更新完成')
    await advance(5000)
    expect(reload).toHaveBeenCalledOnce()
  })

  it('waits through the restart outage and reloads only after the target version returns', async () => {
    const reload = vi.spyOn(window.location, 'reload').mockImplementation(() => {})
    await start()
    status = { ...running, stage: 'restart', percent: 100 }
    await advance(300)
    offline = true
    await advance(2000)
    expect(reload).not.toHaveBeenCalled()
    status = idle('v0.1.14')
    offline = false
    await advance(500)
    expect(container.textContent).toContain('更新完成')
    await advance(5000)
    expect(reload).toHaveBeenCalledOnce()
  })

  it('tracks another update after the user closes a failed attempt', async () => {
    await start()
    status = { ...running, running: false, stage: 'failed', error: 'SHA256 校验失败' }
    await advance(300)
    expect(container.textContent).toContain('SHA256 校验失败')
    const close = [...container.querySelectorAll('button')].find((b) => b.textContent === '关闭')
    expect(close).toBeDefined()
    await act(async () => close!.click())
    await start()
    expect(container.textContent).toContain('面板更新中')
    expect(container.textContent).toContain('42%')
  })

  it('reports an interrupted update instead of reloading when the old version returns', async () => {
    const reload = vi.spyOn(window.location, 'reload').mockImplementation(() => {})
    await start()
    status = idle('v0.1.13')
    await advance(300)
    expect(container.textContent).toContain('面板更新失败')
    expect(container.textContent).toContain('当前版本 v0.1.13')
    await advance(5000)
    expect(reload).not.toHaveBeenCalled()
  })
})
