import { describe, expect, it } from 'vitest'

import type { Observation } from './operation-progress'
import {
  INITIAL_PROGRESS_STATE,
  progressTransition,
  type ProgressEvent,
  type ProgressState,
} from './operation-progress-state'

function observation(overrides: Partial<Observation> = {}): Observation {
  return {
    id: 'obs-1',
    kind: 'user_group.update',
    title: '更新用户分组',
    stages: [
      { key: 'db', label: '校验并写入数据库' },
      { key: 'sync', label: '同步共享端点' },
      { key: 'regenerate', label: '重新生成订阅文件' },
    ],
    stage: 'db',
    percent: 10,
    message: '正在写入数据库',
    status: 'running',
    ...overrides,
  }
}

function run(initial: ProgressState, events: ProgressEvent[]): ProgressState {
  return events.reduce((state, event) => progressTransition(state, event), initial)
}

const running = observation()
const doneClean = observation({
  status: 'done',
  stage: 'regenerate',
  percent: 100,
  message: '完成',
})
const doneWithWarnings = observation({
  status: 'done',
  stage: 'regenerate',
  percent: 100,
  warnings: ['用户 3 发布失败'],
})
const failed = observation({ status: 'failed', stage: 'sync', error: '同步超时' })

describe('progressTransition', () => {
  it('starts polling on show from idle', () => {
    const next = progressTransition(INITIAL_PROGRESS_STATE, { type: 'show', observeId: 'abc' })

    expect(next).toEqual({ phase: 'running', observeId: 'abc', observation: null })
  })

  it('replaces an active dialog on a second show', () => {
    const active = run(INITIAL_PROGRESS_STATE, [
      { type: 'show', observeId: 'abc' },
      { type: 'observe', observation: running },
    ])

    const next = progressTransition(active, { type: 'show', observeId: 'def' })

    expect(next).toEqual({ phase: 'running', observeId: 'def', observation: null })
  })

  it('keeps polling while the observation stays running', () => {
    const next = run(INITIAL_PROGRESS_STATE, [
      { type: 'show', observeId: 'abc' },
      { type: 'observe', observation: running },
      { type: 'observe', observation: { ...running, stage: 'sync', percent: 60 } },
    ])

    expect(next).toMatchObject({ phase: 'running', observeId: 'abc' })
    expect(next).toMatchObject({
      observation: { stage: 'sync', percent: 60, message: '正在写入数据库' },
    })
  })

  it('marks done-without-warnings for auto close', () => {
    const next = run(INITIAL_PROGRESS_STATE, [
      { type: 'show', observeId: 'abc' },
      { type: 'observe', observation: doneClean },
    ])

    expect(next).toMatchObject({ phase: 'done', observeId: 'abc' })
    expect(next).toMatchObject({ observation: { status: 'done' } })
    if (next.phase === 'done') expect(next.autoClose).toBe(true)
  })

  it('keeps done-with-warnings open for manual close', () => {
    const next = run(INITIAL_PROGRESS_STATE, [
      { type: 'show', observeId: 'abc' },
      { type: 'observe', observation: doneWithWarnings },
    ])

    if (next.phase !== 'done') throw new Error('expected done phase')
    expect(next.autoClose).toBe(false)
    expect(next.observation.warnings).toEqual(['用户 3 发布失败'])
  })

  it('enters failed phase on a failed observation', () => {
    const next = run(INITIAL_PROGRESS_STATE, [
      { type: 'show', observeId: 'abc' },
      { type: 'observe', observation: failed },
    ])

    expect(next).toMatchObject({ phase: 'failed', observeId: 'abc' })
    if (next.phase === 'failed') {
      expect(next.observation.error).toBe('同步超时')
    }
  })

  it('enters lost phase when the observation is gone (404)', () => {
    const next = run(INITIAL_PROGRESS_STATE, [
      { type: 'show', observeId: 'abc' },
      { type: 'observe', observation: running },
      { type: 'lost' },
    ])

    expect(next).toEqual({ phase: 'lost', observeId: 'abc' })
  })

  it('closes an active dialog on close', () => {
    const active = run(INITIAL_PROGRESS_STATE, [
      { type: 'show', observeId: 'abc' },
      { type: 'observe', observation: failed },
    ])

    expect(progressTransition(active, { type: 'close' })).toEqual({ phase: 'idle' })
  })

  it('auto-closes a done-without-warnings dialog', () => {
    const active = run(INITIAL_PROGRESS_STATE, [
      { type: 'show', observeId: 'abc' },
      { type: 'observe', observation: doneClean },
    ])

    expect(progressTransition(active, { type: 'autoClose' })).toEqual({ phase: 'idle' })
  })

  it('ignores autoClose while a done-with-warnings dialog is open', () => {
    const active = run(INITIAL_PROGRESS_STATE, [
      { type: 'show', observeId: 'abc' },
      { type: 'observe', observation: doneWithWarnings },
    ])

    expect(progressTransition(active, { type: 'autoClose' })).toBe(active)
  })

  it('ignores all events except show once idle', () => {
    const idle = progressTransition(INITIAL_PROGRESS_STATE, { type: 'close' })

    expect(idle).toEqual({ phase: 'idle' })
    expect(progressTransition(idle, { type: 'observe', observation: running })).toEqual({
      phase: 'idle',
    })
    expect(progressTransition(idle, { type: 'lost' })).toEqual({ phase: 'idle' })
    expect(progressTransition(idle, { type: 'autoClose' })).toEqual({ phase: 'idle' })
  })
})
