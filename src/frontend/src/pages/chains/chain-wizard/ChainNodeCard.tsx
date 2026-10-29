import { type CSSProperties } from 'react'
import { useWorldDrag } from './use-world-drag'
import { XIcon } from 'lucide-react'

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
import { isServerOnline } from '@/lib/server-state'
import { cn } from '@/lib/utils'
import type { CatalogProbeRow, Server } from '@/lib/types'

import {
  CARD_W,
  CARD_H,
  CARD_BODY_H,
  ADDRESS_ROW_H,
  ENTRY_PORT_OFFSET_Y,
  PORT_R,
  inboundCapable,
  natPortOptions,
  normalizePortInput,
  type CanvasNode,
  type CanvasNodeRole,
} from './chain-canvas'
import { ServerProbeMini } from './ServerProbeMini'

const ROLE_LABEL: Record<CanvasNodeRole, string> = {
  entry: '入口',
  middle: '中转',
  exit: '出口',
  orphan: '未接入',
}

/** 画布节点与拖拽预览共享的完整卡片；上层服务器信息，下层地址与入口端口。 */
export function ChainNodeCard({
  node,
  server,
  role,
  portEditable,
  hasIssue,
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
  onMove,
}: {
  node: CanvasNode
  server: Server | undefined
  role: CanvasNodeRole
  /** 端口语义：仅入口（entry_port）与出口（出口节点端口）可指定，中间跳自动分配。 */
  portEditable: boolean
  hasIssue: boolean
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
  onMove: (x: number, y: number) => void
}) {
  const drag = useWorldDrag(node, toWorld, onMove)
  if (!server) return null

  const online = isServerOnline(server)
  const natPorts = natPortOptions(server)

  const portDisplay = normalizePortInput(node.port)
  const entryTitle = isConnecting ? '点击完成连线' : '先点击上游出口，再点击此入口连接'

  return (
    <div
      className={cn(
        'cw-node',
        `is-${role}`,
        hasIssue && 'is-issue',
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
            <span className={cn('cw-node-role', `is-${role}`)}>{ROLE_LABEL[role]}</span>
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
            {!inboundCapable(server) ? <em className="cw-node-exitonly">仅出口</em> : null}
          </div>
          <ServerProbeMini server={server} probeRows={probeRows} probeTitle={probeTitle} />
        </div>
        <div className="cw-node-config">
          <div className="cw-node-address-row">
            <NodeAddressSelect server={server} value={node.address} onChange={onAddressChange} />
          </div>
          <div className="cw-node-port-row">
            <span className="cw-node-port-label">入口端口</span>
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

      <button
        type="button"
        className={cn('cw-port cw-port-exit', outgoingConnected && 'is-connected')}
        aria-label={`出口端口：${server.alias}`}
        title={outgoingConnected ? '点击重新连接（完成前保留原线）' : '点击拉线到下一节点'}
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
