import { renderToStaticMarkup } from 'react-dom/server'
import { describe, expect, it } from 'vitest'

import { UserSubscriptionStatus } from './UserSubscriptionStatus'

describe('UserSubscriptionStatus', () => {
  it('renders the actual failure in an expandable reason panel', () => {
    const html = renderToStaticMarkup(
      <UserSubscriptionStatus
        snapshot={{ status: 'error', revision: 3, error: 'no such column: disable_region_groups' }}
      />,
    )
    expect(html).toContain('订阅异常')
    expect(html).toContain('查看原因')
    expect(html).toContain('<details')
    expect(html).toContain('no such column: disable_region_groups')
    expect(html).toContain('刷新订阅快照')
  })

  it('provides a recovery hint when the server has no error details', () => {
    const html = renderToStaticMarkup(
      <UserSubscriptionStatus snapshot={{ status: 'error', revision: 0 }} />,
    )
    expect(html).toContain('服务端未返回具体原因')
    expect(html).toContain('刷新订阅快照')
  })

  it.each([
    ['missing', '尚未生成'],
    ['pending', '订阅生成中'],
  ] as const)('does not report %s as a generation failure', (status, label) => {
    const html = renderToStaticMarkup(<UserSubscriptionStatus snapshot={{ status, revision: 0 }} />)
    expect(html).toContain(label)
    expect(html).not.toContain('订阅异常')
  })

  it('renders a ready revision without stale error text', () => {
    const html = renderToStaticMarkup(
      <UserSubscriptionStatus snapshot={{ status: 'ready', revision: 4, error: 'old failure' }} />,
    )
    expect(html).toContain('订阅 r4')
    expect(html).not.toContain('old failure')
  })
})
