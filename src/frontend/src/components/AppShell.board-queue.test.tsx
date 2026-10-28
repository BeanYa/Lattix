// @vitest-environment happy-dom
import { act } from 'react'
import { createRoot, type Root } from 'react-dom/client'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { Router } from 'wouter'

import { AppShell } from './AppShell'
import { useBoardQueueContext } from '@/lib/board-queue-context'
import { AuthContext } from '@/lib/auth-context'
import { ThemeProvider } from '@/lib/theme'

// 角标回归：顶栏/侧栏的待处理数与控制台页共享同一份队列实例。
// 页面（这里的探测组件）触发 refresh 后，壳层角标必须立即跟随，
// 而不是等壳层私有副本的下一轮 30 秒轮询。

let offlineCount: number

function serverFixture() {
  return Array.from({ length: 14 }, (_, index) => ({
    id: index + 1,
    alias: `server-${index + 1}`,
    connection_state: index < offlineCount ? 'offline' : 'online',
  }))
}

function BoardQueueProbe() {
  const { refresh } = useBoardQueueContext()
  return (
    <button type="button" onClick={() => refresh()}>
      probe-refresh
    </button>
  )
}

let root: Root
let container: HTMLDivElement

beforeEach(async () => {
  offlineCount = 13
  vi.stubGlobal('IS_REACT_ACT_ENVIRONMENT', true)
  vi.stubGlobal('fetch', async (input: string) => {
    const path = new URL(input, 'http://localhost').pathname
    let data: unknown
    switch (path) {
      case '/api/server/list':
        data = serverFixture()
        break
      case '/api/chain/list':
      case '/api/user/list':
        data = []
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
      case '/api/panel/get-update-status':
        data = {
          running: false,
          stage: '',
          percent: 0,
          message: '',
          current_version: 'v0.1.24',
          target_version: '',
        }
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
      <Router ssrPath="/network/servers">
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
              <BoardQueueProbe />
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
})

function pendingChip(): HTMLAnchorElement {
  const chip = container.querySelector('a[aria-label*="项待处理"]')
  expect(chip, '顶栏待处理入口应存在').not.toBeNull()
  return chip as HTMLAnchorElement
}

describe('壳层角标与页面共享同一份控制台队列', () => {
  it('页面触发的 refresh 让角标立即跟随，不等下一轮轮询', async () => {
    expect(pendingChip().textContent).toContain('13')

    offlineCount = 1
    const button = [...container.querySelectorAll('button')].find(
      (candidate) => candidate.textContent === 'probe-refresh',
    )
    expect(button, '探测组件应渲染').toBeDefined()
    await act(async () => {
      button!.click()
    })
    await act(async () => {})

    expect(pendingChip().textContent).toContain('1')
    expect(pendingChip().textContent).not.toContain('13')
  })
})
