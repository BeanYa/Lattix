// @vitest-environment happy-dom
import { act, useState } from 'react'
import { createRoot } from 'react-dom/client'
import { expect, it } from 'vitest'
import type { ChainOrchestration, Server } from '@/lib/types'
import { ChainNodeCard } from './ChainNodeCard'

Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true })

it('地址弹层里的指针事件不会启动卡片拖动', async () => {
  const host = document.createElement('div')
  document.body.append(host)
  const root = createRoot(host)
  const server = {
    id: 1,
    alias: 'HK',
    country_code: 'HK',
    machine_type: 'direct',
    addresses: ['203.0.113.10'],
    allowed_ports: [],
  } as unknown as Server
  function Harness() {
    const [position, setPosition] = useState('unchanged')
    return (
      <>
        <ChainNodeCard
          node={{ id: 'n1', serverId: 1, x: 0, y: 0, port: '', address: '' }}
          server={server}
          role="entry"
          portEditable
          hasIssue={false}
          isConnecting={false}
          portEditing={false}
          outgoingConnected
          probeRows={[]}
          probeTitle={null}
          toWorld={(x, y) => ({ x, y })}
          onMove={(x, y) => setPosition(`${x},${y}`)}
          onExitPortClick={() => {}}
          onEntryPortClick={() => {}}
          onRemove={() => {}}
          onPortChange={() => {}}
          onPortEditToggle={() => {}}
          onAddressChange={() => {}}
        />
        <output>{position}</output>
      </>
    )
  }
  try {
    await act(async () => root.render(<Harness />))
    const card = host.querySelector('.cw-node') as HTMLElement
    card.setPointerCapture = () => {}
    await act(async () =>
      host
        .querySelector('[role="combobox"]')!
        .dispatchEvent(new MouseEvent('click', { bubbles: true })),
    )
    const option = [...document.querySelectorAll('[role="option"]')].find(
      (e) => e.textContent === '203.0.113.10',
    )!
    expect(option).toBeTruthy()
    await act(async () => {
      option.dispatchEvent(
        new PointerEvent('pointerdown', { bubbles: true, button: 0, clientX: 20, clientY: 20 }),
      )
      card.dispatchEvent(
        new PointerEvent('pointermove', { bubbles: true, clientX: 80, clientY: 90 }),
      )
    })
    expect(host.querySelector('output')!.textContent).toBe('unchanged')
  } finally {
    await act(async () => root.unmount())
    host.remove()
  }
})

it('外部直链节点：外部直链徽标 + 协议摘要 + 只读端口', async () => {
  const host = document.createElement('div')
  document.body.append(host)
  const root = createRoot(host)
  const server = {
    id: 2,
    alias: 'SOCKS-US',
    country_code: 'US',
    kind: 'external',
    connection_state: 'external',
    machine_type: 'nat',
    addresses: [],
    allowed_ports: [],
    external_exit: { protocol: 'socks', address: '198.51.100.9', port: 1080 },
  } as unknown as Server
  try {
    await act(async () =>
      root.render(
        <ChainNodeCard
          node={{ id: 'e1', serverId: 2, x: 0, y: 0, port: '', address: '' }}
          server={server}
          role="exit"
          portEditable
          hasIssue={false}
          isConnecting={false}
          portEditing={false}
          outgoingConnected
          probeRows={[]}
          probeTitle={null}
          toWorld={(x, y) => ({ x, y })}
          onMove={() => {}}
          onExitPortClick={() => {}}
          onEntryPortClick={() => {}}
          onRemove={() => {}}
          onPortChange={() => {}}
          onPortEditToggle={() => {}}
          onAddressChange={() => {}}
        />,
      ),
    )
    expect(host.querySelector('.cw-node-external-badge')?.textContent).toBe('外部直链')
    expect(host.querySelector('.cw-node-external-proto')?.textContent).toBe('socks · TCP')
    // 端口来自链接：只读展示 external_exit.port，不提供编辑控件。
    expect(host.textContent).toContain('节点端口')
    const port = host.querySelector('.cw-node-port-value') as HTMLElement
    expect(port.tagName).toBe('SPAN')
    expect(port.textContent).toBe('1080')
    expect(host.querySelector('.cw-node-port-input')).toBeNull()
    expect(host.querySelector('.cw-node-exitonly')).toBeNull()
  } finally {
    await act(async () => root.unmount())
    host.remove()
  }
})

it('卡片上方的自动编排入口：有图时显示备选数，点击打开编排器', async () => {
  const host = document.createElement('div')
  document.body.append(host)
  const root = createRoot(host)
  const server = {
    id: 3,
    alias: 'KL',
    country_code: 'MY',
    machine_type: 'direct',
    addresses: ['203.0.113.30'],
    allowed_ports: [],
  } as unknown as Server
  const orchestration = {
    root_server_id: 3,
    active_server_id: 3,
    triggered: false,
    nodes: [
      {
        id: 1,
        kind: 'server',
        server_id: 3,
        priority: 0,
        is_root: true,
        online: true,
        banned: false,
      },
      { id: 2, kind: 'server', server_id: 4, priority: 1, online: true, banned: false },
      { id: 3, kind: 'server', server_id: 5, priority: 2, online: true, banned: false },
    ],
    edges: [],
  } as unknown as ChainOrchestration
  let opened = 0
  const card = (extra: { orchestration?: ChainOrchestration; onOrchestration?: () => void }) => (
    <ChainNodeCard
      node={{ id: 'o1', serverId: 3, x: 0, y: 0, port: '', address: '' }}
      server={server}
      role="entry"
      portEditable
      hasIssue={false}
      isConnecting={false}
      portEditing={false}
      outgoingConnected
      probeRows={[]}
      probeTitle={null}
      toWorld={(x, y) => ({ x, y })}
      onMove={() => {}}
      onExitPortClick={() => {}}
      onEntryPortClick={() => {}}
      onRemove={() => {}}
      onPortChange={() => {}}
      onPortEditToggle={() => {}}
      onAddressChange={() => {}}
      {...extra}
    />
  )
  try {
    // 没有编排回调（例如编排画布内部复用卡片）时不渲染入口。
    await act(async () => root.render(card({})))
    expect(host.querySelector('.cw-node-orch')).toBeNull()

    // 新建链路还没有跳位：入口保留但禁用，并提示保存后可配置。
    await act(async () =>
      root.render(
        <ChainNodeCard
          node={{ id: 'o1', serverId: 3, x: 0, y: 0, port: '', address: '' }}
          server={server}
          role="entry"
          portEditable
          hasIssue={false}
          isConnecting={false}
          portEditing={false}
          outgoingConnected
          probeRows={[]}
          probeTitle={null}
          toWorld={(x, y) => ({ x, y })}
          onMove={() => {}}
          onExitPortClick={() => {}}
          onEntryPortClick={() => {}}
          onRemove={() => {}}
          onPortChange={() => {}}
          onPortEditToggle={() => {}}
          onAddressChange={() => {}}
          onOrchestration={() => (opened += 1)}
          orchestrationLocked
        />,
      ),
    )
    const locked = host.querySelector('.cw-node-orch') as HTMLButtonElement
    expect(locked).toBeTruthy()
    expect(locked.disabled).toBe(true)
    expect(locked.getAttribute('title')).toBe('保存链路后可配置自动编排')
    opened = 0

    await act(async () =>
      root.render(card({ orchestration, onOrchestration: () => (opened += 1) })),
    )
    const entry = host.querySelector('.cw-node-orch') as HTMLElement
    expect(entry).toBeTruthy()
    expect(entry.classList.contains('is-configured')).toBe(true)
    expect(entry.textContent).toContain('自动编排')
    expect(entry.textContent).toContain('2 备选')
    await act(async () => entry.click())
    expect(opened).toBe(1)
  } finally {
    await act(async () => root.unmount())
    host.remove()
  }
})
