// @vitest-environment happy-dom
import { expect, it } from 'vitest'

import { visibilityItemKind } from './visibility-items'

it('labels one-hop chains as direct links and multi-hop chains as transit links', () => {
  expect(visibilityItemKind({ id: 'chain:12', label: '直连链路', entry: '服务器' })).toBe(
    '直连链路',
  )
  expect(visibilityItemKind({ id: 'chain:13', label: '中转链路', entry: '入口' })).toBe('中转链路')
  expect(visibilityItemKind({ id: 'chain:14', label: '旧数据无 entry' })).toBe('中转链路')
  expect(visibilityItemKind({ id: 'node:7', label: '直连节点', entry: '服务器' })).toBe('直连节点')
  expect(visibilityItemKind({ id: 'external:3', label: '外部订阅' })).toBe('外部订阅')
})
