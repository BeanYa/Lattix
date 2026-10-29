import { describe, expect, it } from 'vitest'

import { isServerBanned, isServerOnline } from './server-state'
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
})
