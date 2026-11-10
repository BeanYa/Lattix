// @vitest-environment happy-dom
import { act } from 'react'
import { createRoot, type Root } from 'react-dom/client'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { Router } from 'wouter'

import { AppShell } from './AppShell'
import { AuthContext } from '@/lib/auth-context'
import { ThemeProvider } from '@/lib/theme'

// 回归：顶栏头像菜单里的 DropdownMenuLabel 曾脱离 DropdownMenuGroup，
// 点开账户菜单即抛错并让 React 卸载整棵应用树（所有路由变空白直到刷新）。
// 打开菜单后断言菜单项与壳层同时存在，避免再次出现“点头像 → 整页空白”。

let root: Root
let container: HTMLDivElement

beforeEach(async () => {
  vi.stubGlobal('IS_REACT_ACT_ENVIRONMENT', true)
  vi.stubGlobal('fetch', async (input: string) => {
    const path = new URL(input, 'http://localhost').pathname
    let data: unknown
    switch (path) {
      case '/api/server/list':
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
          current_version: 'v0.1.37',
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
      <Router ssrPath="/board">
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
              <p>控制台页面</p>
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
  document.body.innerHTML = ''
  vi.restoreAllMocks()
  vi.unstubAllGlobals()
})

describe('账户头像菜单', () => {
  it('打开菜单后菜单项可见且应用壳层未被卸载', async () => {
    const trigger = container.querySelector<HTMLButtonElement>(
      'button[aria-label="账户菜单：admin"]',
    )
    expect(trigger, '账户菜单触发器应存在').not.toBeNull()

    await act(async () => {
      trigger!.click()
    })
    await act(async () => {})

    expect(document.body.textContent).toContain('登出')
    expect(container.querySelector('[data-slot="app-shell"]'), '应用壳层不应被卸载').not.toBeNull()
    expect(container.textContent).toContain('控制台页面')
  })
})
