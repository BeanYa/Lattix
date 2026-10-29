// @vitest-environment happy-dom
import { act, useState } from 'react'
import { createRoot } from 'react-dom/client'
import { expect, it } from 'vitest'
import type { Server } from '@/lib/types'
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
