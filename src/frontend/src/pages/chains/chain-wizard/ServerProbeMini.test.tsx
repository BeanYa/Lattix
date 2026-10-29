// @vitest-environment happy-dom
import { act } from 'react'
import { createRoot } from 'react-dom/client'
import { afterEach, expect, it } from 'vitest'

import { PROBE_ROUND_SLOTS } from '@/components/server-probe-strip'
import type { CatalogProbeRound, CatalogProbeRow, Server } from '@/lib/types'

import { ServerProbeMini } from './ServerProbeMini'

Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true })

const server = {
  id: 1,
  alias: 'hk-entry-01',
  country_code: 'HK',
  machine_type: 'direct',
  addresses: ['203.0.113.1'],
  allowed_ports: [],
} as unknown as Server

const container = document.createElement('div')
document.body.append(container)
const root = createRoot(container)
afterEach(() => {
  act(() => root.render(null))
})

function rounds(...values: Array<number | null>): CatalogProbeRound[] {
  return values.map((latency, index) => ({
    latency_ms: latency,
    measured_at: new Date(2026, 9, 30, 10, index).toISOString(),
  }))
}

function row(label: string, carrier: CatalogProbeRow['carrier'], values: Array<number | null>) {
  const entries = rounds(...values)
  return {
    label,
    carrier,
    latency_ms: entries.at(-1)?.latency_ms ?? null,
    loss_percent: null,
    samples: entries.length,
    rounds: entries,
    measured_at: entries.at(-1)!.measured_at,
  } as unknown as CatalogProbeRow
}

async function render(probeRows: CatalogProbeRow[]) {
  await act(async () =>
    root.render(<ServerProbeMini server={server} probeRows={probeRows} probeTitle="广东 · 深圳" />),
  )
}

it('shows the full three-network label on a single line instead of a truncated prefix', async () => {
  await render([
    row('广东电信', 'telecom', [88, 95, 91]),
    row('广东联通', 'unicom', [163.33, 159.591, 175.616]),
    row('广东移动', 'mobile', [null, null, null]),
  ])
  const labels = [...container.querySelectorAll('.cw-probe-label')].map(
    (element) => element.textContent,
  )
  expect(labels).toEqual(['广东电信', '广东联通', '广东移动'])
})

it('keeps the last n rounds as blocks with the latest round at the end', async () => {
  await render([row('广东电信', 'telecom', [500, 90, 160])])
  const blocks = [...container.querySelectorAll('.cw-probe-rounds i')]
  expect(blocks).toHaveLength(PROBE_ROUND_SLOTS)
  // 只有最近 3 轮有样本，前 9 个槽位为空槽；末位即最新轮（160ms → warn）。
  expect(
    blocks.slice(0, PROBE_ROUND_SLOTS - 3).every((block) => block.className === 'is-idle'),
  ).toBe(true)
  expect(blocks.at(-3)!.className).toBe('is-fail')
  expect(blocks.at(-2)!.className).toBe('is-ok')
  expect(blocks.at(-1)!.className).toBe('is-warn')
})

it('shows the latest latency and keeps full long labels intact', async () => {
  await render([row('广东深圳电信', 'telecom', [175.616])])
  expect(container.querySelector('.cw-probe-label')!.textContent).toBe('广东深圳电信')
  expect(container.querySelector('.cw-probe-latency')!.textContent).toBe('175.616')
})

it('falls back to the traffic row when the server has no probe samples', async () => {
  await render([])
  expect(container.querySelector('.cw-probe-row')).toBeNull()
  expect(container.querySelector('.cw-node-traffic')).toBeTruthy()
})
