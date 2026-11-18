// @vitest-environment happy-dom
import { act } from 'react'
import { createRoot } from 'react-dom/client'
import { afterEach, expect, it } from 'vitest'
import type { ChainOrchestration } from '@/lib/types'
import { HopStack } from './HopStack'

Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true })
const container = document.createElement('div')
document.body.append(container)
const root = createRoot(container)
afterEach(() => {
  act(() => root.render(null))
})

const orchestration: ChainOrchestration = {
  root_server_id: 1,
  active_server_id: 2,
  triggered: true,
  nodes: [
    {
      id: 10,
      kind: 'server',
      server_id: 1,
      alias: 'A1',
      priority: 0,
      is_root: true,
      online: true,
      banned: false,
    },
    {
      id: 11,
      kind: 'trigger',
      subject: 'self',
      action: 'ban_banned',
      priority: 0,
      online: false,
      banned: false,
    },
    { id: 12, kind: 'server', server_id: 2, alias: 'A2', priority: 0, online: true, banned: false },
  ],
  edges: [
    { from: 10, to: 11 },
    { from: 11, to: 12 },
  ],
}

it('启用了编排的跳：堆叠展示原服务器与备选，点击展开收起', async () => {
  await act(async () => {
    root.render(<HopStack orchestration={orchestration} hopAlias="A1" />)
  })
  const text = container.textContent ?? ''
  expect(text).toContain('A1')
  expect(text).toContain('原服务器')
  expect(text).toContain('编排已触发')
  expect(text).toContain('当前运行：A2')
  // 只读展示：详情页不提供编排编辑入口。
  expect(container.querySelector('[role="dialog"]')).toBeNull()
  const stack = container.querySelector('.cg-hop-stack') as HTMLElement
  expect(stack.getAttribute('aria-expanded')).toBe('false')
  await act(async () => stack.click())
  expect(stack.classList.contains('is-expanded')).toBe(true)
})

it('普通链路（未配置编排）保持原样：不渲染堆叠', async () => {
  await act(async () => {
    root.render(<HopStack hopAlias="A1" />)
  })
  expect(container.querySelector('.cg-hop-stack')).toBeNull()
})

it('图里只有根服务器：等同未配置，不渲染', async () => {
  await act(async () => {
    root.render(
      <HopStack
        orchestration={{
          root_server_id: 1,
          active_server_id: 1,
          triggered: false,
          nodes: [orchestration.nodes[0]],
          edges: [],
        }}
        hopAlias="A1"
      />,
    )
  })
  expect(container.querySelector('.cg-hop-stack')).toBeNull()
})
