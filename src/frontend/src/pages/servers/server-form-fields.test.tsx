// @vitest-environment happy-dom
import { act } from 'react'
import { createRoot, type Root } from 'react-dom/client'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import { ExternalExitFields } from './ExternalExitFields'
import { defaultExternalExitForm } from './external-exit-form'

const LINK = 'vless://uuid@example.com:443?security=reality#东京 01'

describe('ExternalExitFields', () => {
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

  function renderField(overrides: Partial<Parameters<typeof ExternalExitFields>[0]> = {}) {
    act(() => {
      root = createRoot(container)
      root.render(
        <ExternalExitFields
          link={LINK}
          parsing={false}
          parseError=""
          exit={{ ...defaultExternalExitForm(), address: 'example.com', port: '443' }}
          onLinkChange={() => {}}
          onParse={() => {}}
          onExitChange={() => {}}
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

  it('renders the optional share link and the editable protocol form', () => {
    renderField()
    expect(container.querySelector<HTMLTextAreaElement>('#external-link')?.value).toBe(LINK)
    expect(container.textContent).toContain('分享链接（可选）')
    expect(container.textContent).toContain('协议')
    expect(container.querySelector<HTMLInputElement>('#external-address')?.value).toBe(
      'example.com',
    )
    expect(container.querySelector<HTMLInputElement>('#external-port')?.value).toBe('443')
    // 默认协议 http：展示 Basic 认证用户名/密码。
    expect(container.querySelector('#external-username')).toBeTruthy()
    expect(container.querySelector('#external-password')).toBeTruthy()
  })

  it('shows protocol-specific fields', () => {
    renderField({
      exit: { ...defaultExternalExitForm(), protocol: 'vless', security: 'reality' },
    })
    expect(container.querySelector('#external-uuid')).toBeTruthy()
    expect(container.textContent).toContain('Reality 公钥（pbk）')
    container.remove()
    container = document.createElement('div')
    document.body.appendChild(container)
    renderField({ exit: { ...defaultExternalExitForm(), protocol: 'shadowsocks' } })
    expect(container.textContent).toContain('加密方式')
    expect(container.querySelector('#external-ss-password')).toBeTruthy()
  })

  it('triggers parsing from the button', async () => {
    const onParse = vi.fn()
    renderField({ onParse })
    await act(async () => parseButton()?.click())
    expect(onParse).toHaveBeenCalledTimes(1)
  })

  it('disables parsing while empty or in flight', () => {
    renderField({ link: '   ' })
    expect(parseButton()?.disabled).toBe(true)
    container.remove()
    container = document.createElement('div')
    document.body.appendChild(container)
    renderField({ parsing: true })
    expect(parseButton()?.disabled).toBe(true)
    expect(parseButton()?.textContent).toContain('解析中')
  })

  it('shows parse failures', () => {
    renderField({ parseError: '无法解析链接：需为分享链接' })
    expect(container.textContent).toContain('无法解析链接：需为分享链接')
  })
})
