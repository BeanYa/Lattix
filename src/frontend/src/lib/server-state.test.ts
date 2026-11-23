import { describe, expect, it } from 'vitest'

import {
  isServerBanned,
  isServerOnline,
  serverConnectionLabel,
  supportsAgentActions,
} from './server-state'
import type { Server, ServerBanWatch } from './types'

function banWatch(state: ServerBanWatch['state'], lossStreak = 12): ServerBanWatch {
  return { state, loss_streak: lossStreak }
}

function serverWith(overrides: Partial<Server>): Server {
  return {
    id: 1,
    non_direct: false,
    ban_watch: null,
    connection_state: 'online',
    kind: 'managed',
    ...overrides,
  } as Server
}

describe('isServerBanned', () => {
  it('is true only for a banned, direct-connectable server', () => {
    expect(isServerBanned(serverWith({ ban_watch: banWatch('banned') }))).toBe(true)
  })

  it('is false for non-direct servers even when marked banned', () => {
    expect(isServerBanned(serverWith({ non_direct: true, ban_watch: banWatch('banned') }))).toBe(
      false,
    )
  })

  it('is false for observation states that are not banned', () => {
    for (const state of ['normal', 'suspected', 'recovering'] as const) {
      expect(isServerBanned(serverWith({ ban_watch: banWatch(state) }))).toBe(false)
    }
  })

  it('is false without a ban watch snapshot or a server', () => {
    expect(isServerBanned(serverWith({ ban_watch: null }))).toBe(false)
    expect(isServerBanned(serverWith({}))).toBe(false)
    expect(isServerBanned(null)).toBe(false)
    expect(isServerBanned(undefined)).toBe(false)
  })
})

describe('isServerOnline', () => {
  it('is true only for the online connection state', () => {
    expect(isServerOnline(serverWith({ connection_state: 'online' }))).toBe(true)
    expect(isServerOnline(serverWith({ connection_state: 'offline' }))).toBe(false)
    expect(isServerOnline(null)).toBe(false)
  })

  it('never treats an external node as online', () => {
    expect(isServerOnline(serverWith({ kind: 'external', connection_state: 'external' }))).toBe(
      false,
    )
  })
})

describe('serverConnectionLabel', () => {
  it('maps every connection state to its Chinese label', () => {
    expect(serverConnectionLabel('never_connected')).toBe('未连接')
    expect(serverConnectionLabel('connecting')).toBe('连接中')
    expect(serverConnectionLabel('reconnecting')).toBe('重连中')
    expect(serverConnectionLabel('online')).toBe('在线')
    expect(serverConnectionLabel('updating')).toBe('更新中')
    expect(serverConnectionLabel('offline')).toBe('离线')
    expect(serverConnectionLabel('auth_rejected')).toBe('凭据被拒绝')
  })

  it('labels external nodes as 外部节点 instead of 未连接', () => {
    expect(serverConnectionLabel('external')).toBe('外部节点')
  })
})

describe('supportsAgentActions', () => {
  it('is true for managed servers', () => {
    expect(supportsAgentActions({ kind: 'managed' })).toBe(true)
  })

  it('is false for unmanaged external links without an agent', () => {
    expect(supportsAgentActions({ kind: 'external' })).toBe(false)
  })
})

it('identifies updating without treating maintenance as transport online', async () => {
  const { isServerUpdating } = await import('./server-state')
  const updating = serverWith({ connection_state: 'updating' })
  expect(isServerUpdating(updating)).toBe(true)
  expect(isServerOnline(updating)).toBe(false)
  expect(isServerUpdating(serverWith({ connection_state: 'offline' }))).toBe(false)
  expect(isServerUpdating(undefined)).toBe(false)
})
