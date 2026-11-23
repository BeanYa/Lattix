// @vitest-environment happy-dom
import { act, useState } from 'react'
import { createRoot, type Root } from 'react-dom/client'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import type { Server } from '@/lib/types'

import { ExternalExitFields } from './ExternalExitFields'
import { defaultExternalExitForm } from './external-exit-form'
import { ServerEditDialog } from './ServerEditDialog'
import {
  defaultBilling,
  defaultTraffic,
  initialExternalLink,
  serverTypeOf,
  type ServerEditFormState,
  type ServerTypeChoice,
} from './server-form-utils'
import type { ServerEditFormController } from './use-server-edit-form'

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

function mockServer(overrides: Partial<Server> = {}): Server {
  return {
    id: 1,
    alias: 'tokyo-01',
    address: '1.2.3.4',
    addresses: ['1.2.3.4'],
    learned_addr: '',
    nic_addresses: ['1.2.3.4'],
    config_drift: false,
    machine_type: 'direct',
    allowed_ports: [],
    tags: [],
    country_code: 'JP',
    location: 'Tokyo',
    non_direct: false,
    kind: 'managed',
    external_link: '',
    panel_latency_ms: null,
    panel_probed_at: null,
    last_seen_at: null,
    xray_version: '',
    agent_version: '',
    connection_state: 'never_connected',
    session_kind: 'initial',
    last_connected_at: null,
    last_disconnected_at: null,
    last_reconnected_at: null,
    reconnect_count: 0,
    last_disconnect_reason: '',
    agent_settings_status: 'synced',
    agent_settings_revision: 0,
    agent_settings_desired_revision: 0,
    agent_settings_error: '',
    agent_settings_reported_at: null,
    effective_xray_version: null,
    metrics: null,
    billing: {
      enabled: false,
      provider: null,
      amount_minor: 0,
      currency: 'CNY',
      service_started_on: '2026-01-01',
      interval_count: 1,
      interval_unit: 'month',
      next_renewal_on: '2026-02-01',
      status: 'disabled',
      assumed_valid_through: '',
      status_changed_at: '',
    },
    traffic_plan: {
      quota_bytes: null,
      accounting_mode: 'outbound',
      reset_anchor_on: '2026-01-01',
      reset_count: 1,
      reset_unit: 'month',
      period_started_on: '',
      next_reset_on: '',
      tx_bytes: 0,
      rx_bytes: 0,
      used_bytes: 0,
      complete: false,
    },
    custom_settings: {},
    created_at: '2026-01-01T00:00:00Z',
    ...overrides,
  }
}

describe('ServerEditDialog', () => {
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

  function Harness({ target }: { target: Server }) {
    const [form, setForm] = useState<ServerEditFormState>({
      serverType: serverTypeOf(target),
      alias: target.alias,
      addresses: target.kind === 'external' ? [] : target.addresses,
      defaultAddr: target.kind === 'external' ? '' : target.address,
      addrInput: '',
      portRows: [''],
      tags: [],
      countryCode: target.country_code,
      location: target.location,
      nonDirect: target.non_direct,
      billing: defaultBilling(),
      traffic: defaultTraffic(),
      xrayOverride: '',
      xrayVersions: ['latest'],
      external: initialExternalLink(),
    })
    const controller: ServerEditFormController = {
      target,
      saving: false,
      error: '',
      linkConflict: false,
      form,
      patch: (p) => setForm((cur) => ({ ...cur, ...p })),
      countryOptions: [{ code: 'JP', label: '日本 (JP)', name: 'Japan' }],
      citySuggestions: [],
      addCandidates: [],
      addAddress: () => {},
      setServerType: (serverType: ServerTypeChoice) =>
        setForm((cur) => ({ ...cur, serverType })),
      setCountry: (countryCode: string) => setForm((cur) => ({ ...cur, countryCode })),
      setExternalLink: () => {},
      setExternalExit: () => {},
      parseExternalLink: async () => {},
      openEdit: () => {},
      closeEdit: () => {},
      onSubmit: async (e) => {
        e.preventDefault()
      },
    }
    return <ServerEditDialog controller={controller} providers={[]} onManageProviders={() => {}} />
  }

  function clickType(value: ServerTypeChoice) {
    const radio = document.body.querySelector<HTMLInputElement>(
      `input[name="edit-server-type"][value="${value}"]`,
    )
    if (!radio) throw new Error(`radio ${value} not found`)
    act(() => {
      radio.click()
    })
  }

  it('switches managed direct server to nat and external with appropriate notices and fields', () => {
    act(() => {
      root = createRoot(container)
      root.render(<Harness target={mockServer()} />)
    })

    const radios = document.body.querySelectorAll('input[name="edit-server-type"]')
    expect(radios).toHaveLength(3)
    expect(document.body.textContent).not.toContain('全部留空 = 仅出口档')

    clickType('nat')
    expect(document.body.textContent).toContain('全部留空 = 仅出口档')
    expect(document.body.textContent).toContain('TryExchange')

    clickType('external')
    expect(document.body.textContent).toContain('断开面板控制并吊销 Agent 凭证')
    expect(document.body.querySelector('#editExternal-link')).toBeTruthy()
    expect(document.body.textContent).not.toContain('全部留空 = 仅出口档')
  })

  it('switches an external server back to managed direct and shows install command notice', () => {
    act(() => {
      root = createRoot(container)
      root.render(
        <Harness
          target={mockServer({ kind: 'external', machine_type: 'nat', address: 'ext.test' })}
        />,
      )
    })

    expect(document.body.querySelector('#editExternal-link')).toBeTruthy()
    clickType('direct')
    expect(document.body.textContent).toContain('将生成新的 Agent 安装命令')
    expect(document.body.querySelector('#editExternal-link')).toBeFalsy()
  })
})
