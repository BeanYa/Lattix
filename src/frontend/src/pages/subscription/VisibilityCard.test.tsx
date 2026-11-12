// @vitest-environment happy-dom
import { act } from 'react'
import { createRoot } from 'react-dom/client'
import { afterEach, expect, it, vi } from 'vitest'

import { requester } from '@/lib/requester'

import { VisibilityCard } from './VisibilityCard'
import type { PublicationJob } from './publication-jobs'

Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true })

const host = document.createElement('div')
document.body.append(host)
const root = createRoot(host)

afterEach(() => {
  act(() => root.render(null))
  vi.restoreAllMocks()
})

it('preserves opaque choices and newer edits when an earlier publication settles', async () => {
  const firstJob: PublicationJob = {
    id: 'first',
    kind: 'visibility',
    status: 'queued',
    queued_behind: false,
  }
  const secondJob: PublicationJob = {
    id: 'second',
    kind: 'visibility',
    status: 'queued',
    queued_behind: true,
  }
  const catalog = {
    links: [{ id: 'chain:1', label: 'Tokyo' }],
    external_subscriptions: [{ id: 'external:2', label: 'Partner', nodes: ['Singapore'] }],
    hidden_ids: ['node:99'],
  }
  vi.spyOn(requester, 'getJSON')
    .mockResolvedValueOnce(catalog)
    .mockResolvedValue({ ...catalog, hidden_ids: ['node:99', 'chain:1'] })
  const post = vi
    .spyOn(requester, 'postJSON')
    .mockResolvedValueOnce(firstJob)
    .mockResolvedValueOnce(secondJob)
  const submitted = vi.fn()
  const render = async (settledJobs: PublicationJob[]) => {
    await act(async () => {
      root.render(
        <VisibilityCard token="token" settledJobs={settledJobs} onSubmitted={submitted} />,
      )
    })
  }
  await render([])

  const [chain, external] = host.querySelectorAll<HTMLInputElement>('input[type="checkbox"]')
  const save = host.querySelector<HTMLButtonElement>('.is-primary')!
  const disclosure = host.querySelector<HTMLButtonElement>('[aria-expanded]')!
  expect(chain.checked).toBe(true)
  expect(external.checked).toBe(true)
  expect(save.disabled).toBe(true)
  act(() => disclosure.click())
  expect(disclosure.getAttribute('aria-expanded')).toBe('true')
  expect(host.querySelector('.sub-visibility-nodes')?.textContent).toBe('Singapore')

  act(() => chain.click())
  await act(async () => save.click())
  expect(post.mock.calls[0]?.[1]).toEqual({ hidden_ids: ['node:99', 'chain:1'] })
  expect(submitted).toHaveBeenCalledWith(firstJob)
  expect(save.disabled).toBe(true)

  act(() => external.click())
  expect(save.disabled).toBe(false)
  await render([{ ...firstJob, status: 'completed' }])
  expect(external.checked).toBe(false)
  expect(save.disabled).toBe(false)

  await act(async () => save.click())
  expect(post.mock.calls[1]?.[1]).toEqual({ hidden_ids: ['node:99', 'chain:1', 'external:2'] })
  await render([
    { ...firstJob, status: 'completed' },
    { ...secondJob, status: 'failed' },
  ])
  expect(external.checked).toBe(false)
  expect(save.disabled).toBe(false)
})
