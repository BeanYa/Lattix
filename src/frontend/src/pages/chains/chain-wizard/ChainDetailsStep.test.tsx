// @vitest-environment happy-dom
import { act } from 'react'
import { createRoot } from 'react-dom/client'
import { expect, it } from 'vitest'

import type { Server } from '@/lib/types'

import type { ChainFormController } from '../use-chain-form'
import { ChainDetailsStep } from './ChainDetailsStep'

Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true })

const entryServer = {
  id: 1,
  alias: 'HK-ENTRY',
  kind: 'managed',
  country_code: 'HK',
  location: '香港',
  machine_type: 'direct',
  addresses: ['203.0.113.10'],
  allowed_ports: [],
  tags: [],
} as unknown as Server

const externalServer = {
  id: 2,
  alias: 'SOCKS-US',
  kind: 'external',
  country_code: 'US',
  location: '美国',
  connection_state: 'external',
  machine_type: 'nat',
  addresses: [],
  allowed_ports: [],
  tags: [],
  external_exit: { protocol: 'socks', address: '198.51.100.9', port: 1080 },
} as unknown as Server

function controller(): ChainFormController {
  return {
    editingChainId: null,
    creating: false,
    createError: '',
    form: {
      name: '',
      protocol: 'vless',
      shortId: '',
      destPreset: 'dl.google.com:443',
      dest: 'dl.google.com:443',
      serverNames: 'dl.google.com',
      fingerprint: 'chrome',
      network: 'tcp',
      path: '/',
      mode: 'auto',
      host: '',
      flow: 'xtls-rprx-vision',
      encryption: 'none',
      security: 'reality',
      certMode: 'selfsign',
      tlsDomain: '',
      serviceName: 'grpc',
      method: '2022-blake3-aes-128-gcm',
      cipher: 'auto',
      obfsPassword: '',
      upMbps: '50',
      downMbps: '100',
      portHop: '',
      entryProtocolEnabled: false,
      entryShortId: '',
      entryDestPreset: 'dl.google.com:443',
      entryDest: 'dl.google.com:443',
      entryServerNames: 'dl.google.com',
      entryFingerprint: 'chrome',
      targetAddress: '',
      targetPort: '',
      trafficMultiplier: '1.000',
    },
    patch: () => {},
    isReality: true,
    isHy2: false,
    effectiveProtocol: 'vless',
    topologyServers: [entryServer, externalServer],
    hopIndexes: [],
    entryPortHint: '',
    entryBlockLocked: false,
    chainCount: 1,
    chainPaths: [
      {
        key: 'n1>n2',
        servers: [entryServer, externalServer],
        hops: [
          {
            server: entryServer,
            role: 'entry',
            external: false,
            externalExit: null,
            port: 1443,
          },
          {
            server: externalServer,
            role: 'exit',
            external: true,
            externalExit: { protocol: 'socks', address: '198.51.100.9', port: 1080 },
            port: 1080,
          },
        ],
        externalExit: { protocol: 'socks', address: '198.51.100.9', port: 1080 },
      },
    ],
    chainPreviewNames: ['自动生成'],
    entrySharedPreview: true,
    exitIsExternal: true,
    exitConfigMissing: false,
    externalExit: { protocol: 'socks', address: '198.51.100.9', port: 1080 },
    externalExits: [{ protocol: 'socks', address: '198.51.100.9', port: 1080 }],
    strictNameResult: {
      tokens: [],
      references: [],
      error: '',
    } as unknown as ChainFormController['strictNameResult'],
    beginCreate: () => {},
    beginEdit: () => {},
    onNetworkChange: () => {},
    onSecurityChange: () => {},
    onProtocolChange: () => {},
    onSubmit: async () => {},
  } as unknown as ChainFormController
}

it('外部直链出口：隐藏出口协议表单，展示只读摘要并强制开启入口协议区块', async () => {
  const host = document.createElement('div')
  document.body.append(host)
  const root = createRoot(host)
  try {
    await act(async () =>
      root.render(
        <ChainDetailsStep
          controller={controller()}
          panelShort="hk"
          chainType="relay"
          entryPort="1443"
          onBack={() => {}}
          warnings={[]}
          serverById={
            new Map([
              [entryServer.id, entryServer],
              [externalServer.id, externalServer],
            ])
          }
        />,
      ),
    )
    const text = host.textContent ?? ''
    // 结果预览：按链路详情列表样式展示将创建的链路（名称/跳/端口/外部出口）。
    expect(text).toContain('结果预览')
    expect(text).toContain('将创建 1 条链路')
    expect(text).toContain('自动生成')
    expect(text).toContain('待创建')
    expect(text).toContain('HK-ENTRY')
    expect(text).toContain('SOCKS-US')
    expect(text).toContain('端口 1080')
    expect(text).toContain('共享入口')
    expect(text).toContain('外部出口')
    expect(text).toContain('外部直链出口')
    expect(text).toContain('SOCKS5')
    expect(text).toContain('198.51.100.9:1080')
    expect(text).toContain('TCP（无传输层）')
    expect(text).toContain('客户端入口为入口服务器的 VLESS 端点，出口由外部节点承载')
    // 入口服务器 A 与普通链路一样可配置完整 VLESS 协议细节。
    expect(text).toContain('入口协议')
    expect(text).toContain('入口服务器 A 的客户端接入端点')
    expect(text).toContain('传输（network）')
    expect(text).toContain('安全层（security）')
    expect(text).toContain('VLESS Encryption')
    expect(text).toContain('uTLS 指纹（客户端）')
    // 出口协议表单（选择器/协议名）不出现；入口协议固定 VLESS。
    expect(text).not.toContain('Hysteria2')
    // 旧的入口协议区块开关不再出现（入口协议由主表单承载）。
    expect(host.querySelector('input[type="checkbox"]')).toBeNull()
    expect(text).not.toContain('外部直链出口固定启用')
    expect(text).not.toContain('入口 short_id（可空）')
  } finally {
    await act(async () => root.unmount())
    host.remove()
  }
})
