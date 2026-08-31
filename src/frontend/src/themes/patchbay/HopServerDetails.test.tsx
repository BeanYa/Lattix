import { renderToStaticMarkup } from 'react-dom/server'
import { describe, expect, it } from 'vitest'

import { DEMO_SERVERS } from '@/lib/dashboard-demo'
import { formatDateTime } from '@/lib/format'
import { TimezoneContext } from '@/lib/timezone-context'
import type { Server } from '@/lib/types'

import { HopServerDetails } from './HopServerDetails'

function render(server?: Server, fallbackAddress = '') {
  return renderToStaticMarkup(
    <TimezoneContext.Provider value={{ timezone: 'UTC', refresh: () => {} }}>
      <HopServerDetails server={server} fallbackAddress={fallbackAddress} />
    </TimezoneContext.Provider>,
  )
}

describe('hop server details', () => {
  it('labels disconnected telemetry as historical rather than live', () => {
    const updatedAt = '2026-08-20T12:34:56Z'
    const html = render({
      ...DEMO_SERVERS[0],
      connection_state: 'offline',
      metrics: { ...DEMO_SERVERS[0].metrics!, updated_at: updatedAt },
    })
    expect(html).toContain('Agent 离线')
    expect(html).toContain('历史数据 · 非实时')
    expect(html).not.toContain('最近遥测')
    expect(html).toContain(`>${formatDateTime(updatedAt, 'UTC')}</time>`)
  })

  it('shows the absence of telemetry without invented percentages', () => {
    const html = render({ ...DEMO_SERVERS[0], metrics: null })
    expect(html).toContain('等待遥测上报')
    expect(html).toContain('容量未上报')
    expect(html).not.toContain('0.0%')
    expect(html).not.toContain('NaN')
  })

  it('preserves a missing server’s hop address and explains the missing data', () => {
    const html = render(undefined, 'edge.example.com')
    expect(html).toContain('服务器未找到')
    expect(html).toContain('edge.example.com')
    expect(html).toContain('位置未设置')
  })

  it('renders additional addresses as an accessible native disclosure', () => {
    const html = render({
      ...DEMO_SERVERS[0],
      address: 'edge.example.com',
      addresses: ['edge.example.com', '2001:db8::8'],
    })
    expect(html).toContain('<details>')
    expect(html).toContain('<summary>另 1 个地址</summary>')
    expect(html).toContain('2001:db8::8')
  })
})
