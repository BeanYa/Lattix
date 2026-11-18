// @vitest-environment happy-dom
import { act } from 'react'
import { createRoot } from 'react-dom/client'
import { afterEach, expect, it, vi } from 'vitest'
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

it('前卡显示原服务器，active 标记编排已触发', async () => {
  const onEdit = vi.fn()
  await act(async () => {
    root.render(<HopStack orchestration={orchestration} hopAlias="A1" onEdit={onEdit} />)
  })
  const text = container.textContent ?? ''
  expect(text).toContain('A1')
  expect(text).toContain('原服务器')
  expect(text).toContain('编排已触发')
  expect(text).toContain('当前运行：A2')
  const front = container.querySelector('.cg-hop-stack-card.is-front') as HTMLElement
  await act(async () => front.click())
  expect(onEdit).toHaveBeenCalledTimes(1)
})

it('黄点切换展开态，无编排时仍显示入口', async () => {
  await act(async () => {
    root.render(<HopStack orchestration={orchestration} hopAlias="A1" />)
  })
  const dot = container.querySelector('.cg-hop-stack-dot') as HTMLElement
  expect(dot.getAttribute('aria-expanded')).toBe('false')
  await act(async () => dot.click())
  expect(container.querySelector('.cg-hop-stack')?.classList.contains('is-expanded')).toBe(true)

  await act(async () => {
    root.render(<HopStack hopAlias="A1" />)
  })
  expect(container.querySelector('.cg-hop-stack.is-bare')).toBeTruthy()
})
