// @vitest-environment happy-dom
import { act } from 'react'
import { createRoot } from 'react-dom/client'
import { expect, it, vi } from 'vitest'

import type { Server } from '@/lib/types'

import { ExternalServerCard } from './ServerMonitor'

Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true })

function externalServer(overrides: Partial<Server> = {}): Server {
  return {
    id: 1,
    alias: '香港家宽出口',
    external_link: 'http://user:pass@203.0.113.10:8080#HK',
    external_exit: { protocol: 'http', address: '203.0.113.10', port: 8080 },
    country_code: 'HK',
    tags: [],
    panel_latency_ms: 12,
    panel_probed_at: '2026-11-17T08:00:00Z',
    ...overrides,
  } as unknown as Server
}

async function renderCard(server: Server): Promise<HTMLElement> {
  const host = document.createElement('div')
  document.body.append(host)
  const root = createRoot(host)
  await act(async () => {
    root.render(
      <ExternalServerCard
        server={server}
        onEdit={() => {}}
        onDelete={() => {}}
        onOpenTrafficStats={() => {}}
      />,
    )
  })
  return host
}

it('复制按钮复制外部直链原文并给出已复制反馈', async () => {
  const writeText = vi.fn().mockResolvedValue(undefined)
  Object.defineProperty(navigator, 'clipboard', {
    value: { writeText },
    configurable: true,
  })
  const link = 'vless://11111111-2222-3333-4444-555555555555@example.com:443?type=tcp#节点'
  const host = await renderCard(externalServer({ external_link: link }))
  const button = host.querySelector(
    'button[aria-label="复制 香港家宽出口 直链"]',
  ) as HTMLButtonElement
  expect(button.disabled).toBe(false)
  await act(async () => {
    button.click()
  })
  expect(writeText).toHaveBeenCalledWith(link)
  expect(button.title).toBe('已复制')
  host.remove()
})

it('手工协议参数的节点没有原始直链时复制按钮禁用并提示', async () => {
  const writeText = vi.fn().mockResolvedValue(undefined)
  Object.defineProperty(navigator, 'clipboard', {
    value: { writeText },
    configurable: true,
  })
  const host = await renderCard(externalServer({ external_link: '' }))
  const button = host.querySelector(
    'button[aria-label="复制 香港家宽出口 直链"]',
  ) as HTMLButtonElement
  expect(button.disabled).toBe(true)
  expect(button.title).toContain('手工协议参数')
  await act(async () => {
    button.click()
  })
  expect(writeText).not.toHaveBeenCalled()
  host.remove()
})
