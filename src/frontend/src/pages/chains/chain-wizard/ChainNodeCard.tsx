import { type CSSProperties } from 'react'
import { useWorldDrag } from './use-world-drag'
import { TriangleAlertIcon, XIcon } from 'lucide-react'

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
import { isServerOnline, isServerUpdating, serverConnectionLabel } from '@/lib/server-state'
import { cn } from '@/lib/utils'
import type { CatalogProbeRow, ChainOrchestration, Server } from '@/lib/types'

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
  role,
  roleLabel,
  portEditable,
  portLabel,
  portLayout = 'topology',
  removable = true,
  hasIssue,
  hasWarning = false,
  isConnecting,
  exitConnecting = false,
  isActive = false,
  portEditing,
  outgoingConnected,
  incomingConnected = false,
  probeRows,
  probeTitle,
  orchestration,
  onOrchestration,
  orchestrationLocked = false,
  replacementServers,
  onReplaceServer,
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
  /** 角色文案：分支/汇合下可同时是入口与中转，由画布传入（默认单选角色）。 */
  roleLabel?: string
  /** 端口语义：有流量入口入边 = 入口端口；有 Internet 出边 = 出口端口；中间跳不显示端口。 */
  portEditable: boolean
  portLabel?: string
  /** 端口锚点排布：topology=拓扑画布错位端口；symmetric=编排画布左右居中对齐端口。 */
  portLayout?: 'topology' | 'symmetric'
  /** 是否显示右上角移除按钮（编排图的根与当前运行节点不允许删除）。 */
  removable?: boolean
  hasIssue: boolean
  /** 空悬警告（黄色边框）：只提示不会生成，不是硬错误。 */
  hasWarning?: boolean
  /** 橡皮筋连线进行中：入口端口高亮为可落点。 */
  isConnecting: boolean
  /** 当前节点正作为连线起点，高亮出口端口。 */
  exitConnecting?: boolean
  /** 编排画布中标记当前正在使用的节点。 */
  isActive?: boolean
  portEditing: boolean
  outgoingConnected: boolean
  incomingConnected?: boolean
  probeRows: CatalogProbeRow[]
  probeTitle: string | null
  /** 该跳的自动编排图：有图时入口高亮并显示备选数量（编辑既有链路才有）。 */
  orchestration?: ChainOrchestration
  /** 提供时在卡片上方浮动「自动编排」入口。 */
  onOrchestration?: () => void
  /** 新建链路尚无跳位：入口保留但禁用，保存后才能配置。 */
  orchestrationLocked?: boolean
  /** 服务器已被删除时的可替换候选列表与回调。 */
  replacementServers?: Server[]
  onReplaceServer?: (serverId: number) => void
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
  const portDisplay = normalizePortInput(node.port)
  const entryTitle = isConnecting ? '点击完成连线' : '先点击上游出口，再点击此入口连接'
  const entryPortY = portLayout === 'symmetric' ? CARD_H / 2 : ENTRY_PORT_OFFSET_Y
  const exitPortY = portLayout === 'symmetric' ? CARD_H / 2 : CARD_BODY_H / 2
  const nodeStyle = {
    left: node.x,
    top: node.y,
    '--cw-card-w': `${CARD_W}px`,
    '--cw-card-h': `${CARD_H}px`,
    '--cw-body-h': `${CARD_BODY_H}px`,
    '--cw-address-h': `${ADDRESS_ROW_H}px`,
    '--cw-entry-y': `${entryPortY}px`,
    '--cw-exit-y': `${exitPortY}px`,
    '--cw-port-r': `${PORT_R}px`,
  } as CSSProperties

  if (!server) {
    const missingLabel = `已删除服务器 #${node.serverId}`
    const replaceItems = (replacementServers ?? []).map((candidate) => ({
      value: String(candidate.id),
      label: candidate.alias,
      server: candidate,
    }))
    return (
      <div
        className={cn(
          'cw-node',
          `is-${role}`,
          'is-issue',
          'is-missing',
          drag.dragging && 'is-dragging',
        )}
        style={nodeStyle}
        {...drag.handlers}
      >
        <div className="cw-node-card">
          <div className="cw-node-body">
            <div className="cw-node-head">
              <TriangleAlertIcon className="cw-node-missing-icon" aria-hidden />
              <span className="cw-node-alias" title={missingLabel}>
                {missingLabel}
              </span>
              <span className={cn('cw-node-role', `is-${role}`)}>
                {roleLabel ?? ROLE_LABEL[role]}
              </span>
              {removable ? (
                <button
                  type="button"
                  className="cw-node-remove"
                  aria-label={`移除节点 #${node.serverId}`}
                  onClick={(event) => {
                    event.stopPropagation()
                    onRemove()
                  }}
                >
                  <XIcon />
                </button>
              ) : null}
            </div>
            <p className="cw-node-missing-copy">该服务器已删除，可移除或原地替换</p>
            <div className="cw-node-missing-actions">
              {removable ? (
                <button
                  type="button"
                  className="cw-node-missing-remove"
                  onClick={(event) => {
                    event.stopPropagation()
                    onRemove()
                  }}
                >
                  移除此节点
                </button>
              ) : null}
              {portDisplay ? (
                <span className="cw-node-missing-port">原端口 {portDisplay}</span>
              ) : null}
            </div>
          </div>
          <div className="cw-node-config">
            {onReplaceServer && replaceItems.length > 0 ? (
              <div className="cw-node-replace-row">
                <Select
                  value=""
                  onValueChange={(next) => {
                    const id = Number(next)
                    if (id > 0) onReplaceServer(id)
                  }}
                  items={replaceItems.map((item) => ({ value: item.value, label: item.label }))}
                >
                  <SelectTrigger
                    className="cw-node-replace-trigger"
                    aria-label={`替换已删除服务器：#${node.serverId}`}
                  >
                    <SelectValue placeholder="替换为其他服务器…" />
                  </SelectTrigger>
                  <SelectContent
                    align="start"
                    alignItemWithTrigger={false}
                    className="cw-node-select-content"
                  >
                    {replaceItems.map((item) => (
                      <SelectItem key={item.value} value={item.value}>
                        <CountryFlag code={item.server.country_code} />
                        <span>{item.server.alias}</span>
                        {isExternalServer(item.server) && !isUsableExternalServer(item.server) ? (
                          <span>（未配置参数）</span>
                        ) : null}
                      </SelectItem>
                    ))}
                  </SelectContent>
                </Select>
              </div>
            ) : (
              <div className="cw-node-port-row">
                <span className="cw-node-address is-empty">
                  没有可替换的服务器，请直接移除此节点
                </span>
              </div>
            )}
          </div>
          <button
            type="button"
            className={cn(
              'cw-port cw-port-entry',
              incomingConnected && 'is-connected',
              isConnecting && 'is-target',
            )}
            aria-label={`入口端口：#${node.serverId}`}
            title={entryTitle}
            onClick={(event) => {
              event.stopPropagation()
              onEntryPortClick()
            }}
          />
        </div>
        <button
          type="button"
          className={cn(
            'cw-port cw-port-exit',
            outgoingConnected && 'is-connected',
            exitConnecting && 'is-connecting',
          )}
          aria-label={`出口端口：#${node.serverId}`}
          title={outgoingConnected ? '点击继续拉线（可多路分支）' : '点击拉线到下一节点'}
          onClick={(event) => {
            event.stopPropagation()
            onExitPortClick()
          }}
        />
      </div>
    )
  }

  const online = isServerOnline(server)
  const natPorts = natPortOptions(server)
  // 未托管外部直链节点：无 Agent、无入站能力，端口来自链接（只读展示）。
  const external = isExternalServer(server)
  const externalExit = external ? server.external_exit : undefined

  // 编排图里的服务器节点数减去根（当前跳主服务器）= 该跳备选数量。
  const backups = orchestration
    ? Math.max(
        0,
        orchestration.nodes.filter(
          (item) => item.kind === 'server' && item.server_id !== orchestration.root_server_id,
        ).length,
      )
    : 0

  return (
    <div
      className={cn(
        'cw-node',
        `is-${role}`,
        isActive && 'is-active',
        hasIssue && 'is-issue',
        hasWarning && !hasIssue && 'is-warning',
        drag.dragging && 'is-dragging',
      )}
      style={nodeStyle}
      {...drag.handlers}
    >
      {onOrchestration ? (
        <button
          type="button"
          className={cn(
            'cw-node-orch',
            orchestration && 'is-configured',
            orchestrationLocked && 'is-locked',
          )}
          aria-label={`自动编排：${server.alias}`}
          aria-disabled={orchestrationLocked || undefined}
          disabled={orchestrationLocked}
          title={
            orchestrationLocked
              ? '保存链路后可配置自动编排'
              : orchestration
                ? '编辑该跳自动编排'
                : '为该跳配置自动编排'
          }
          onPointerDown={(event) => event.stopPropagation()}
          onClick={(event) => {
            event.stopPropagation()
            onOrchestration()
          }}
        >
          <span className="cw-node-orch-dot" />
          <span>自动编排</span>
          {orchestration && backups > 0 ? <em>{backups} 备选</em> : null}
          {orchestration?.triggered ? <em>已触发</em> : null}
        </button>
      ) : null}
      <div className="cw-node-card">
        <div className="cw-node-body">
          <div className="cw-node-head">
            <CountryFlag code={server.country_code} />
            <span className="cw-node-alias" title={server.alias}>
              {server.alias}
            </span>
            <span className={cn('cw-node-role', `is-${role}`)}>
              {roleLabel ?? ROLE_LABEL[role]}
            </span>
            {removable ? (
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
            ) : null}
          </div>
          <div className="cw-node-meta">
            <i
              title={serverConnectionLabel(server.connection_state)}
              className={cn(
                'cw-node-state',
                isServerUpdating(server) ? 'is-updating' : online ? 'is-online' : 'is-offline',
              )}
            />
            <span>{server.location || server.country_code || '未设置地区'}</span>
            {isServerUpdating(server) ? <em>更新中</em> : null}
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

      <button
        type="button"
        className={cn(
          'cw-port cw-port-exit',
          outgoingConnected && 'is-connected',
          exitConnecting && 'is-connecting',
        )}
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
          <SelectContent
            align="start"
            alignItemWithTrigger={false}
            className="cw-node-select-content"
          >
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
        onBlur={() => onPortEditToggle()}
        onKeyDown={(event) => {
          if (event.key === 'Enter' || event.key === 'Escape') {
            event.stopPropagation()
            onPortEditToggle()
          }
        }}
        onClick={(event) => event.stopPropagation()}
      />
    )
  }
  return (
    <button
      type="button"
      className="cw-node-port-value"
      title="点击修改端口（0 或留空为自动分配）"
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
        <SelectContent
          align="start"
          alignItemWithTrigger={false}
          className="cw-node-select-content"
        >
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
