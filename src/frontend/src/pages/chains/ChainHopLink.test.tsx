// @vitest-environment happy-dom
import { act, type ReactNode } from 'react'
import { createRoot } from 'react-dom/client'
import { expect, it } from 'vitest'

import type { ChainSegmentStat } from '@/lib/types'

import { HopLink } from './ChainHopLink'

Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true })

function segment(kind: ChainSegmentStat['kind'], latency: number | null): ChainSegmentStat {
  return {
    hop_id: 1,
    kind,
    target_address: '203.0.113.10',
    target_port: kind === 'tcp' ? 1080 : 0,
    latency_ms: latency,
    sampled_at: '2026-11-17T08:00:00Z',
  }
}

async function render(node: ReactNode): Promise<HTMLElement> {
  const host = document.createElement('div')
  document.body.append(host)
  const root = createRoot(host)
  await act(async () => {
    root.render(node)
  })
  return host
}

it('外部出口段同时展示 Ping 与 TCP 延迟，超时保留字样', async () => {
  const host = await render(
    <HopLink
      segment={segment('tcp', null)}
      segments={[segment('icmp', 8.5), segment('tcp', null)]}
      flowing={false}
    />,
  )
  expect(host.textContent).toContain('Ping 8.5 ms')
  expect(host.textContent).toContain('TCP 超时')
  expect(host.querySelector('[aria-label="入口到外部节点延迟"]')).not.toBeNull()
  host.remove()
})

it('外部出口段无样本时两个来源显示待探测占位', async () => {
  const host = await render(<HopLink segment={null} segments={[]} flowing={false} />)
  expect(host.textContent).toContain('Ping —')
  expect(host.textContent).toContain('TCP —')
  host.remove()
})

it('托管段保持单一首选来源', async () => {
  const host = await render(<HopLink segment={segment('tcp', 8.5)} flowing={false} />)
  expect(host.textContent).toContain('8.5 ms')
  expect(host.textContent).not.toContain('Ping')
  host.remove()
})
