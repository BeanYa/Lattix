import { useState, type CSSProperties } from 'react'
import { useWorldDrag } from './use-world-drag'
import { ArrowDownIcon, ArrowUpIcon, LayersIcon, PlusIcon, XIcon } from 'lucide-react'

import { CountryFlag } from '@/components/CountryFlag'
import { Input } from '@/components/ui/input'
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from '@/components/ui/select'
import { addressFamily } from '@/lib/address'
import { isServerBanned, isServerOnline } from '@/lib/server-state'
import { cn } from '@/lib/utils'
import type { CatalogProbeRow, Server } from '@/lib/types'

import {
  CARD_W,
  CARD_H,
  CARD_BODY_H,
  ADDRESS_ROW_H,
  ENTRY_PORT_OFFSET_Y,
  PORT_R,
  externalPortFor,
  inboundCapable,
  isExternalServer,
  isUsableExternalServer,
  natPortOptions,
  normalizePortInput,
  type CanvasNode,
  type CanvasNodeRole,
} from './chain-canvas'
import { externalExitSummary } from '../external-exit'
import { ServerProbeMini } from './ServerProbeMini'

const ROLE_LABEL: Record<CanvasNodeRole, string> = {
  entry: '入口',
  middle: '中转',
  exit: '出口',
  orphan: '未接入',
}

/** 画布节点与拖拽预览共享的完整卡片；上层服务器信息，下层地址与入口端口，支持跳位备选池管理。 */
export function ChainNodeCard({
  node,
  server,
  allServers = [],
  usedPrimaryIds,
  role,
  roleLabel,
  portEditable,
  portLabel,
  hasIssue,
  hasWarning = false,
  isConnecting,
  portEditing,
  outgoingConnected,
  incomingConnected = false,
  probeRows,
  probeTitle,
  toWorld,
  onExitPortClick,
  onEntryPortClick,
  onRemove,
  onPortChange,
  onPortEditToggle,
  onAddressChange,
  onAddCandidate,
  onRemoveCandidate,
  onMoveCandidate,
  onMove,
}: {
  node: CanvasNode
  server: Server | undefined
  allServers?: Server[]
  usedPrimaryIds?: Set<number>
  role: CanvasNodeRole
  /** 角色文案：分支/汇合下可同时是入口与中转，由画布传入（默认单选角色）。 */
  roleLabel?: string
  /** 端口语义：有流量入口入边 = 入口端口；有 Internet 出边 = 出口端口；中间跳不显示端口。 */
  portEditable: boolean
  portLabel?: string
  hasIssue: boolean
  /** 空悬警告（黄色边框）：只提示不会生成，不是硬错误。 */
  hasWarning?: boolean
  /** 橡皮筋连线进行中：入口端口高亮为可落点。 */
  isConnecting: boolean
  portEditing: boolean
  outgoingConnected: boolean
  incomingConnected?: boolean
  probeRows: CatalogProbeRow[]
  probeTitle: string | null
  toWorld: (clientX: number, clientY: number) => { x: number; y: number }
  onExitPortClick: () => void
  onEntryPortClick: () => void
  onRemove: () => void
  onPortChange: (port: string) => void
  onPortEditToggle: () => void
  onAddressChange: (address: string) => void
  onAddCandidate?: (serverId: number) => void
  onRemoveCandidate?: (serverId: number) => void
  onMoveCandidate?: (index: number, direction: -1 | 1) => void
  onMove: (x: number, y: number) => void
}) {
  const drag = useWorldDrag(node, toWorld, onMove)
  const [poolOpen, setPoolOpen] = useState(false)
  if (!server) return null

  const online = isServerOnline(server)
  const natPorts = natPortOptions(server)
  // 未托管外部直链节点：无 Agent、无入站能力，端口来自链接（只读展示）。
  const external = isExternalServer(server)
  const externalExit = external ? server.external_exit : undefined
  const candidates = node.candidates ?? []

  // 过滤该跳位可添加的兼容备选服务器：排除本跳主节点、本跳已有备选、画布其他跳位的主节点，并按角色能力过滤。
  const eligibleCandidates = allServers.filter((candidate) => {
    if (candidate.id === server.id) return false
    if (candidates.some((c) => c.serverId === candidate.id)) return false
    if (usedPrimaryIds?.has(candidate.id)) return false
    if (external) {
      return isExternalServer(candidate) && isUsableExternalServer(candidate)
    }
    if (isExternalServer(candidate)) return false
    if ((role === 'entry' || role === 'middle') && !inboundCapable(candidate)) return false
    return true
  })

  const portDisplay = normalizePortInput(node.port)
  const entryTitle = isConnecting ? '点击完成连线' : '先点击上游出口，再点击此入口连接'

  return (
    <div
      className={cn(
        'cw-node',
        `is-${role}`,
        hasIssue && 'is-issue',
        hasWarning && !hasIssue && 'is-warning',
        drag.dragging && 'is-dragging',
      )}
      style={
        {
          left: node.x,
          top: node.y,
          '--cw-card-w': `${CARD_W}px`,
          '--cw-card-h': `${CARD_H}px`,
          '--cw-body-h': `${CARD_BODY_H}px`,
          '--cw-address-h': `${ADDRESS_ROW_H}px`,
          '--cw-entry-y': `${ENTRY_PORT_OFFSET_Y}px`,
          '--cw-exit-y': `${CARD_BODY_H / 2}px`,
          '--cw-port-r': `${PORT_R}px`,
        } as CSSProperties
      }
      {...drag.handlers}
    >
      <div className="cw-node-card">
        <div className="cw-node-body">
          <div className="cw-node-head">
            <CountryFlag code={server.country_code} />
            <span className="cw-node-alias" title={server.alias}>
              {server.alias}
            </span>
            {onAddCandidate ? (
              <button
                type="button"
                className={cn('cw-node-pool-btn', candidates.length > 0 && 'has-candidates')}
                title="配置该跳位的替补候选服务器池（故障自动切换顺序）"
                aria-label={`配置节点 ${server.alias} 的备选服务器池`}
                onClick={(event) => {
                  event.stopPropagation()
                  setPoolOpen((v) => !v)
                }}
              >
                <LayersIcon />
                <span>{candidates.length > 0 ? `+${candidates.length} 备选` : '+ 备选'}</span>
              </button>
            ) : null}
            <span className={cn('cw-node-role', `is-${role}`)}>
              {roleLabel ?? ROLE_LABEL[role]}
            </span>
            <button
              type="button"
              className="cw-node-remove"
              aria-label={`移除节点 ${server.alias}`}
              onClick={(event) => {
                event.stopPropagation()
                onRemove()
              }}
            >
              <XIcon />
            </button>
          </div>
          <div className="cw-node-meta">
            <i className={cn('cw-node-state', online ? 'is-online' : 'is-offline')} />
            <span>{server.location || server.country_code || '未设置地区'}</span>
            {!external && !inboundCapable(server) ? (
              <em className="cw-node-exitonly">仅出口</em>
            ) : null}
          </div>
          {external ? (
            <div
              className="cw-node-external"
              title={
                externalExit
                  ? `外部直链 ${externalExit.address}:${externalExit.port} · 仅可作最后一跳`
                  : '外部直链配置缺失，请重新保存该服务器'
              }
            >
              <em className="cw-node-external-badge">外部直链</em>
              <span className="cw-node-external-proto">
                {externalExit ? externalExitSummary(externalExit) : '配置缺失'}
              </span>
            </div>
          ) : (
            <ServerProbeMini server={server} probeRows={probeRows} probeTitle={probeTitle} />
          )}
        </div>
        <div className="cw-node-config">
          <div className="cw-node-address-row">
            <NodeAddressSelect server={server} value={node.address} onChange={onAddressChange} />
          </div>
          {external || portEditable ? (
            <div className="cw-node-port-row">
              <span className="cw-node-port-label">
                {external ? '节点端口' : (portLabel ?? '入口端口')}
              </span>
              <NodePortControl
                server={server}
                node={node}
                portEditable={portEditable}
                portEditing={portEditing}
                portDisplay={portDisplay}
                natPorts={natPorts}
                onPortChange={onPortChange}
                onPortEditToggle={onPortEditToggle}
              />
            </div>
          ) : null}
        </div>
        {/* 入口圆点与右侧端口输入对齐；地址选择位于其上方。 */}
        <button
          type="button"
          className={cn(
            'cw-port cw-port-entry',
            incomingConnected && 'is-connected',
            isConnecting && 'is-target',
          )}
          aria-label={`入口端口：${server.alias}`}
          title={entryTitle}
          onClick={(event) => {
            event.stopPropagation()
            onEntryPortClick()
          }}
        />
      </div>

      {poolOpen && onAddCandidate ? (
        <div
          className="cw-node-pool-popover"
          role="dialog"
          aria-label={`${server.alias} 跳位备选池`}
          onClick={(event) => event.stopPropagation()}
          onPointerDown={(event) => event.stopPropagation()}
        >
          <div className="cw-node-pool-head">
            <strong>跳位备选池（{ROLE_LABEL[role]}）</strong>
            <button
              type="button"
              className="cw-node-remove"
              aria-label="关闭备选池弹窗"
              onClick={() => setPoolOpen(false)}
            >
              <XIcon />
            </button>
          </div>
          <p className="cw-node-pool-hint">
            主节点不可达、被封禁、离线或流量耗尽时，按 P1 → P2 顺序自动替换。
          </p>
          <div className="cw-node-pool-list">
            <div className="cw-node-pool-item is-primary">
              <span className="cw-node-pool-prio">P0</span>
              <CountryFlag code={server.country_code} />
              <span className="cw-node-pool-name">{server.alias}</span>
              <span className="cw-node-pool-tag">首选</span>
            </div>
            {candidates.map((cand, idx) => {
              const candSrv = allServers.find((s) => s.id === cand.serverId)
              const candOnline = candSrv ? isServerOnline(candSrv) : false
              const candBanned = candSrv ? isServerBanned(candSrv) : false
              return (
                <div key={cand.serverId} className="cw-node-pool-item">
                  <span className="cw-node-pool-prio">P{idx + 1}</span>
                  {candSrv ? <CountryFlag code={candSrv.country_code} /> : null}
                  <span className="cw-node-pool-name">{candSrv?.alias ?? `#${cand.serverId}`}</span>
                  {candBanned ? (
                    <span className="cw-node-pool-tag is-danger">Banned</span>
                  ) : (
                    <span className={cn('cw-node-pool-tag', candOnline ? 'is-ok' : 'is-muted')}>
                      {candSrv?.kind === 'external' ? '外部' : candOnline ? '在线' : '离线'}
                    </span>
                  )}
                  <div className="cw-node-pool-actions">
                    <button
                      type="button"
                      disabled={idx === 0}
                      title="上移优先级"
                      aria-label={`上移备选 ${candSrv?.alias ?? cand.serverId}`}
                      onClick={() => onMoveCandidate?.(idx, -1)}
                    >
                      <ArrowUpIcon />
                    </button>
                    <button
                      type="button"
                      disabled={idx === candidates.length - 1}
                      title="下移优先级"
                      aria-label={`下移备选 ${candSrv?.alias ?? cand.serverId}`}
                      onClick={() => onMoveCandidate?.(idx, 1)}
                    >
                      <ArrowDownIcon />
                    </button>
                    <button
                      type="button"
                      title="移除备选"
                      aria-label={`移除备选 ${candSrv?.alias ?? cand.serverId}`}
                      onClick={() => onRemoveCandidate?.(cand.serverId)}
                    >
                      <XIcon />
                    </button>
                  </div>
                </div>
              )
            })}
          </div>
          {eligibleCandidates.length > 0 ? (
            <div className="cw-node-pool-add">
              <Select
                value=""
                onValueChange={(val) => {
                  const id = Number(val)
                  if (id > 0) onAddCandidate(id)
                }}
                items={eligibleCandidates.map((s) => ({
                  value: String(s.id),
                  label: `${s.alias}${s.location ? ` (${s.location})` : ''}`,
                }))}
              >
                <SelectTrigger className="cw-node-pool-select" aria-label="添加备选服务器">
                  <PlusIcon />
                  <SelectValue placeholder="添加兼容备选服务器…" />
                </SelectTrigger>
                <SelectContent align="start" alignItemWithTrigger={false}>
                  {eligibleCandidates.map((s) => (
                    <SelectItem key={s.id} value={String(s.id)}>
                      {s.alias}
                      {s.location ? ` · ${s.location}` : ''}
                    </SelectItem>
                  ))}
                </SelectContent>
              </Select>
            </div>
          ) : (
            <p className="cw-node-pool-empty">暂无更多符合当前跳位角色的可用服务器</p>
          )}
        </div>
      ) : null}

      <button
        type="button"
        className={cn('cw-port cw-port-exit', outgoingConnected && 'is-connected')}
        aria-label={`出口端口：${server.alias}`}
        title={outgoingConnected ? '点击继续拉线（可多路分支）' : '点击拉线到下一节点'}
        onClick={(event) => {
          event.stopPropagation()
          onExitPortClick()
        }}
      />
    </div>
  )
}

/** 底部端口控件：NAT 服务器 = 可用端口下拉；直连 = 点击展开数字输入；中间跳 = 「自动」。 */
function NodePortControl({
  server,
  node,
  portEditable,
  portEditing,
  portDisplay,
  natPorts,
  onPortChange,
  onPortEditToggle,
}: {
  server: Server
  node: CanvasNode
  portEditable: boolean
  portEditing: boolean
  portDisplay: string
  natPorts: { options: number[]; total: number; truncated: boolean }
  onPortChange: (port: string) => void
  onPortEditToggle: () => void
}) {
  // 外部直链节点：端口由链接决定（external_exit.port），只读展示，不可编辑/自动分配。
  if (isExternalServer(server)) {
    const port = externalPortFor(server)
    return (
      <span
        className="cw-node-port-value is-locked"
        title={port ? '端口来自外部直链，不可修改' : '外部直链缺少端口，请重新保存该服务器'}
      >
        {port || '—'}
      </span>
    )
  }
  if (!portEditable) {
    return (
      <button
        type="button"
        className="cw-node-port-value is-auto"
        disabled
        title="中间跳端口由面板自动分配"
      >
        自动
      </button>
    )
  }
  // NAT 服务器：可用端口下拉（allowed_ports 段展开），另带「自动分配」。
  if (natPorts.options.length > 0) {
    const invalid =
      portDisplay !== '' &&
      !natPorts.options.includes(Number(portDisplay)) &&
      Number(portDisplay) !== 0
    const items = [
      { value: '', label: '自动分配' },
      ...natPorts.options.map((port) => ({ value: String(port), label: String(port) })),
      ...(invalid ? [{ value: portDisplay, label: `${portDisplay}（不在可用段内）` }] : []),
    ]
    return (
      <span className="cw-node-port-select">
        <Select
          value={portDisplay}
          onValueChange={(next) => onPortChange(String(next ?? ''))}
          items={items}
        >
          <SelectTrigger
            className="cw-node-port-trigger"
            aria-label={`选择入口端口：${server.alias}`}
            title="选择入口端口"
          >
            <SelectValue placeholder="自动分配" />
          </SelectTrigger>
          <SelectContent align="start" alignItemWithTrigger={false}>
            {items.map((item) => (
              <SelectItem
                key={item.value === '' ? '__auto__' : item.value}
                value={item.value}
                title={
                  item.value === ''
                    ? undefined
                    : item.label.includes('不在')
                      ? item.label
                      : `${server.alias} 可用端口 ${item.label}`
                }
              >
                {item.label}
              </SelectItem>
            ))}
            {natPorts.truncated ? (
              <div className="cw-node-port-truncated">
                可用段共 {natPorts.total} 个端口，仅列出前 {natPorts.options.length} 个
              </div>
            ) : null}
          </SelectContent>
        </Select>
      </span>
    )
  }
  // 直连：数字输入（点击圆点/chip 展开）。
  if (portEditing) {
    return (
      <Input
        className="cw-node-port-input"
        aria-label={`入口端口数值：${server.alias}`}
        type="number"
        min={1}
        max={65535}
        autoFocus
        value={node.port}
        placeholder="0=自动"
        onChange={(event) => onPortChange(event.target.value)}
        onClick={(event) => event.stopPropagation()}
      />
    )
  }
  return (
    <button
      type="button"
      className="cw-node-port-value"
      title="点击修改端口"
      onClick={(event) => {
        event.stopPropagation()
        onPortEditToggle()
      }}
    >
      {portDisplay || '0'}
    </button>
  )
}

/** 空值跟随默认地址；使用触发器边缘锚定，避免选中项对齐混用画布缩放坐标。 */
function NodeAddressSelect({
  server,
  value,
  onChange,
}: {
  server: Server
  value: string
  onChange: (address: string) => void
}) {
  const candidates =
    server.addresses.length > 0
      ? server.addresses
      : [...new Set([server.address, server.learned_addr].filter(Boolean))]
  if (candidates.length === 0) {
    return <span className="cw-node-address is-empty">默认公网地址</span>
  }
  const invalid = value !== '' && !candidates.includes(value)
  const label = (address: string) =>
    addressFamily(address) === 'ipv6' ? `${address}（IPv6）` : address
  const items = [
    { value: '', label: '跟随服务器默认地址' },
    ...candidates.map((address) => ({ value: address, label: label(address) })),
    ...(invalid ? [{ value, label: `${value}（已失效，将回退默认）` }] : []),
  ]
  return (
    <span className="cw-node-address">
      <Select value={value} onValueChange={(next) => onChange(String(next ?? ''))} items={items}>
        <SelectTrigger
          className="cw-node-address-trigger"
          aria-label={`服务器地址：${server.alias}`}
        >
          <SelectValue placeholder="跟随服务器默认地址" />
        </SelectTrigger>
        <SelectContent align="start" alignItemWithTrigger={false}>
          {items.map((item) => (
            <SelectItem key={item.value === '' ? '__default__' : item.value} value={item.value}>
              {item.label}
            </SelectItem>
          ))}
        </SelectContent>
      </Select>
    </span>
  )
}

// 预览直接复用真实节点，避免另写一套近似样式；外层 inert，控件不参与交互。
const previewActions = {
  toWorld: (x: number, y: number) => ({ x, y }),
  onExitPortClick: () => {},
  onEntryPortClick: () => {},
  onRemove: () => {},
  onPortChange: () => {},
  onPortEditToggle: () => {},
  onAddressChange: () => {},
  onMove: () => {},
}

export function ChainNodeDragPreview({
  server,
  probeRows,
  probeTitle,
  emptyCanvas,
}: {
  server: Server
  probeRows: CatalogProbeRow[]
  probeTitle: string | null
  emptyCanvas: boolean
}) {
  return (
    <ChainNodeCard
      node={{ id: `preview-${server.id}`, serverId: server.id, x: 0, y: 0, port: '', address: '' }}
      server={server}
      role={emptyCanvas ? 'entry' : 'orphan'}
      portEditable={emptyCanvas}
      hasIssue={false}
      isConnecting={false}
      portEditing={false}
      outgoingConnected={emptyCanvas}
      probeRows={probeRows}
      probeTitle={probeTitle}
      {...previewActions}
    />
  )
}
