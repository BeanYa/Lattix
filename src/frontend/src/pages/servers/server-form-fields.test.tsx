// @vitest-environment happy-dom
import { act } from 'react'
import { createRoot, type Root } from 'react-dom/client'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import type { ParseExternalLinkResponse } from '@/lib/types'

import { ExternalLinkField } from './server-form-fields'

const LINK = 'vless://uuid@example.com:443?security=reality#东京 01'

function preview(): ParseExternalLinkResponse {
  return {
    name: '东京 01',
    protocol: 'vless',
    address: 'example.com',
    port: 443,
    transport: 'tcp · reality',
    exit: { protocol: 'vless', address: 'example.com', port: 443, uuid: 'ab****cd' },
  }
}

describe('ExternalLinkField', () => {
  let container: HTMLDivElement
  let root: Root | undefined

  beforeEach(() => {
    container = document.createElement('div')
    document.body.appendChild(container)
  })

  afterEach(() => {
    act(() => root?.unmount())
    root = undefined
    container.remove()
  })

  function renderField(overrides: Partial<Parameters<typeof ExternalLinkField>[0]> = {}) {
    act(() => {
      root = createRoot(container)
      root.render(
        <ExternalLinkField
          link={LINK}
          preview={preview()}
          parseError=""
          parsing={false}
          onChange={() => {}}
          onParse={() => {}}
          {...overrides}
        />,
      )
    })
  }

  function parseButton() {
    return [...container.querySelectorAll('button')].find((button) =>
      button.textContent?.includes('解析'),
    )
  }

  it('renders the link and the parsed preview summary', () => {
    renderField()
    expect(container.querySelector<HTMLTextAreaElement>('#externalLink')?.value).toBe(LINK)
    const previewBlock = container.querySelector('[aria-label="解析预览"]')
    expect(previewBlock?.textContent).toContain('东京 01')
    expect(previewBlock?.textContent).toContain('vless')
    expect(previewBlock?.textContent).toContain('example.com')
    expect(previewBlock?.textContent).toContain('443')
    expect(previewBlock?.textContent).toContain('tcp · reality')
  })

  it('triggers parsing from the button', async () => {
    const onParse = vi.fn()
    renderField({ onParse })
    await act(async () => parseButton()?.click())
    expect(onParse).toHaveBeenCalledTimes(1)
  })

  it('disables parsing while empty or in flight', () => {
    renderField({ link: '   ', preview: null })
    expect(parseButton()?.disabled).toBe(true)
    container.remove()
    container = document.createElement('div')
    document.body.appendChild(container)
    renderField({ parsing: true })
    expect(parseButton()?.disabled).toBe(true)
    expect(parseButton()?.textContent).toContain('解析中')
  })

  it('shows parse failures instead of a stale preview', () => {
    renderField({ preview: null, parseError: '无法解析链接：需为分享链接' })
    expect(container.textContent).toContain('无法解析链接：需为分享链接')
    expect(container.querySelector('[aria-label="解析预览"]')).toBeNull()
  })
})
