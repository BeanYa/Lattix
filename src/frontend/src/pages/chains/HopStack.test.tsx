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

const front = <div className="cg-hop">Evoxt KL 原服务器卡片</div>

it('有备选：前卡仍是跳位卡片，备选叠在其后', async () => {
  await act(async () => {
    root.render(<HopStack orchestration={orchestration}>{front}</HopStack>)
  })
  // 前卡就是原卡片本身，不额外复制一份（备选卡也是 .cg-hop，但只在前卡位置有一张）。
  expect(container.querySelectorAll('.cg-hop-stack-front .cg-hop')).toHaveLength(1)
  expect(container.querySelectorAll('.cg-hop')).toHaveLength(2)
  // 备选也是同款服务器卡片（复用 .cg-hop 表面），卡头可读地露出。
  const backs = [...container.querySelectorAll('.cg-hop-stack-back')]
  expect(backs).toHaveLength(1)
  expect(backs[0].classList.contains('cg-hop')).toBe(true)
  expect(backs[0].textContent).toContain('备选')
  expect(backs[0].textContent).toContain('A2')
  // 备选正在运行：标注当前运行节点。
  expect(container.textContent).toContain('当前运行：A2')
})

it('未配置编排的跳保持原样：不包堆叠层', async () => {
  await act(async () => {
    root.render(<HopStack>{front}</HopStack>)
  })
  expect(container.querySelector('.cg-hop')).toBeTruthy()
  expect(container.querySelector('.cg-hop-stack')).toBeNull()
  expect(container.querySelector('.cg-hop-stack-back')).toBeNull()
})

it('图里只有根服务器：等同未配置，不包堆叠层', async () => {
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
      >
        {front}
      </HopStack>,
    )
  })
  expect(container.querySelector('.cg-hop-stack')).toBeNull()
})
