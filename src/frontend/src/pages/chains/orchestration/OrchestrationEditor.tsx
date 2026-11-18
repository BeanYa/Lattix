import { useEffect, useMemo, useState } from 'react'
import type { Chain, ChainHop, Server } from '@/lib/types'
import { api } from '@/lib/api'
import { cn } from '@/lib/utils'
import {
  TRIGGERS,
  addServer,
  addTrigger,
  connect,
  createEditorState,
  removeNode,
  setPriority,
  setServerField,
  toSavePayload,
  triggerLabel,
  validateGraph,
  type EditorNode,
  type EditorState,
  type TriggerAction,
  type TriggerSubject,
} from './orchestration-graph'
import './orchestration.css'

const NODE_W = 186
const TRIGGER_W = 168
const NODE_H = 92

function nodeCenter(node: EditorNode) {
  return {
    x: node.x + (node.kind === 'server' ? NODE_W / 2 : TRIGGER_W / 2),
    y: node.y + NODE_H / 2,
  }
}

export function OrchestrationEditor({
  chain,
  hop,
  onClose,
  onSaved,
}: {
  chain: Chain
  hop: ChainHop
  onClose: () => void
  onSaved: () => void
}) {
  const [state, setState] = useState<EditorState>(() =>
    createEditorState({
      chainId: chain.id,
      seq: hop.seq,
      activeServerId: hop.server_id,
      orchestration: hop.orchestration,
    }),
  )
  const [servers, setServers] = useState<Server[]>([])
  const [tab, setTab] = useState<'trigger' | 'server'>('trigger')
  const [selected, setSelected] = useState<string | null>(null)
  const [error, setError] = useState('')
  const [saving, setSaving] = useState(false)
  const errors = useMemo(() => validateGraph(state), [state])
  const serverMap = useMemo(() => new Map(servers.map((server) => [server.id, server])), [servers])

  useEffect(() => {
    let cancelled = false
    api
      .servers()
      .then((list) => {
        if (!cancelled) setServers(list)
      })
      .catch(() => undefined)
    return () => {
      cancelled = true
    }
  }, [])

  useEffect(() => {
    const onKeyDown = (event: KeyboardEvent) => {
      if (event.key === 'Escape') onClose()
    }
    window.addEventListener('keydown', onKeyDown)
    return () => window.removeEventListener('keydown', onKeyDown)
  }, [onClose])

  const lastSeq = chain.hops.length - 1
  const hasRecovery = state.nodes.some((node) => node.subject === 'root')
  const availableServers = servers.filter(
    (server) => !state.nodes.some((node) => node.kind === 'server' && node.serverId === server.id),
  )

  const addServerNode = (serverId: number) => {
    const next = addServer(state, serverId)
    if (next === state) {
      setError('无法添加该服务器（重复或已达上限）')
      return
    }
    setError('')
    setState(next)
  }

  const addTriggerNode = (subject: TriggerSubject, action: TriggerAction) => {
    if (subject === 'prev' && hop.seq === 0) {
      setError('入口跳没有上一跳超时触发器')
      return
    }
    if (subject === 'next' && hop.seq === lastSeq) {
      setError('出口跳没有下一跳超时触发器')
      return
    }
    if (subject === 'root' && hasRecovery) {
      setError('原节点恢复触发器最多一个')
      return
    }
    const next = addTrigger(state, { subject, action })
    if (next === state) {
      setError('无法添加触发器（已达上限）')
      return
    }
    setError('')
    setState(next)
  }

  const onNodeClick = (key: string) => {
    if (!selected) {
      setSelected(key)
      return
    }
    if (selected === key) {
      setSelected(null)
      return
    }
    const from = state.nodes.find((node) => node.key === selected)
    const to = state.nodes.find((node) => node.key === key)
    if (!from || !to) return
    if (from.kind === to.kind) {
      setSelected(key)
      return
    }
    const next =
      from.kind === 'server' ? connect(state, selected, key) : connect(state, key, selected)
    if (next.edges.length === state.edges.length) {
      setError('无法连接：检查触发器的来源/目标限制')
    } else {
      setError('')
      setState(next)
    }
    setSelected(null)
  }

  const save = async () => {
    if (errors.length > 0) return
    setSaving(true)
    setError('')
    try {
      await api.saveHopOrchestration(toSavePayload(state))
      onSaved()
    } catch (err) {
      setError(err instanceof Error ? err.message : '保存失败')
    } finally {
      setSaving(false)
    }
  }

  const renderNode = (node: EditorNode) => {
    if (node.kind === 'server') {
      const server = node.serverId ? serverMap.get(node.serverId) : undefined
      const removable = !node.isRoot && node.serverId !== state.activeServerId
      return (
        <div
          key={node.key}
          className={cn(
            'orch-node is-server',
            selected === node.key && 'is-selected',
            node.isRoot && 'is-root',
          )}
          style={{ left: node.x, top: node.y, width: NODE_W }}
          onClick={() => onNodeClick(node.key)}
        >
          <div className="orch-node-head">
            <strong>{server?.alias ?? `#${node.serverId ?? '?'}`}</strong>
            {node.isRoot ? <em>根 · 原服务器</em> : null}
            {node.serverId === state.activeServerId ? <em>当前使用</em> : null}
          </div>
          <div className="orch-node-fields" onClick={(event) => event.stopPropagation()}>
            <input
              value={node.address}
              placeholder="跟随默认地址"
              onChange={(event) =>
                setState(setServerField(state, node.key, 'address', event.target.value))
              }
            />
            <input
              value={node.listenPort}
              placeholder="自动端口"
              inputMode="numeric"
              onChange={(event) =>
                setState(setServerField(state, node.key, 'listenPort', event.target.value))
              }
            />
          </div>
          {removable ? (
            <button
              type="button"
              className="orch-node-remove"
              aria-label="移除服务器"
              onClick={(event) => {
                event.stopPropagation()
                setState(removeNode(state, node.key))
              }}
            >
              ×
            </button>
          ) : null}
        </div>
      )
    }
    return (
      <div
        key={node.key}
        className={cn('orch-node is-trigger', selected === node.key && 'is-selected')}
        style={{ left: node.x, top: node.y, width: TRIGGER_W }}
        onClick={() => onNodeClick(node.key)}
      >
        <div className="orch-node-head">
          <strong>{triggerLabel(node)}</strong>
          <button
            type="button"
            className="orch-node-remove"
            aria-label="移除触发器"
            onClick={(event) => {
              event.stopPropagation()
              setState(removeNode(state, node.key))
            }}
          >
            ×
          </button>
        </div>
        <label className="orch-priority" onClick={(event) => event.stopPropagation()}>
          优先级
          <input
            value={node.priority}
            inputMode="numeric"
            onChange={(event) => setState(setPriority(state, node.key, event.target.value))}
          />
        </label>
      </div>
    )
  }

  return (
    <div className="orch-overlay" role="presentation" onMouseDown={onClose}>
      <div
        className="orch-panel"
        role="dialog"
        aria-modal="true"
        aria-label="自动编排编辑器"
        onMouseDown={(event) => event.stopPropagation()}
      >
        <header className="orch-head">
          <div>
            <h2>自动编排 · 第 {hop.seq + 1} 跳</h2>
            <p>
              {chain.name} · 原服务器 {hop.server_alias} · 当前使用{' '}
              {hop.orchestration?.nodes.find((node) => node.active)?.alias ?? hop.server_alias}
            </p>
          </div>
          <div className="orch-head-actions">
            <button type="button" className="cg-chain-type" onClick={onClose}>
              取消
            </button>
            <button
              type="button"
              className="cg-chain-type is-selected"
              disabled={saving || errors.length > 0}
              onClick={() => void save()}
            >
              {saving ? '保存中…' : '保存编排'}
            </button>
          </div>
        </header>
        <div className="orch-body">
          <aside className="orch-side">
            <div className="orch-tabs">
              <button
                type="button"
                className={cn(tab === 'trigger' && 'is-active')}
                onClick={() => setTab('trigger')}
              >
                触发动作
              </button>
              <button
                type="button"
                className={cn(tab === 'server' && 'is-active')}
                onClick={() => setTab('server')}
              >
                服务器
              </button>
            </div>
            {tab === 'trigger' ? (
              <div className="orch-list">
                {TRIGGERS.map((trigger) => {
                  const disabled =
                    (trigger.subject === 'prev' && hop.seq === 0) ||
                    (trigger.subject === 'next' && hop.seq === lastSeq) ||
                    (trigger.subject === 'root' && hasRecovery)
                  return (
                    <button
                      key={`${trigger.subject}/${trigger.action}`}
                      type="button"
                      className="orch-list-item"
                      disabled={disabled}
                      onClick={() => addTriggerNode(trigger.subject, trigger.action)}
                    >
                      <strong>{trigger.label}</strong>
                      <span>
                        {trigger.subject === 'root'
                          ? '当前不在根且根恢复健康时切回（默认最高优先级）'
                          : trigger.subject === 'self'
                            ? '本机三网测速封禁'
                            : '相邻跳连续探测超时'}
                      </span>
                    </button>
                  )
                })}
              </div>
            ) : (
              <div className="orch-list">
                {availableServers.length === 0 ? (
                  <p className="orch-empty">没有可添加的服务器</p>
                ) : null}
                {availableServers.map((server) => (
                  <button
                    key={server.id}
                    type="button"
                    className="orch-list-item"
                    onClick={() => addServerNode(server.id)}
                  >
                    <strong>{server.alias}</strong>
                    <span>
                      {server.location || '未填写地区'} ·{' '}
                      {server.kind === 'external' ? '外部直链' : '托管'}
                    </span>
                  </button>
                ))}
              </div>
            )}
          </aside>
          <div className="orch-canvas-wrap">
            <div className="orch-canvas">
              <svg className="orch-edges" aria-hidden="true">
                {state.edges.map((edge) => {
                  const from = state.nodes.find((node) => node.key === edge.from)
                  const to = state.nodes.find((node) => node.key === edge.to)
                  if (!from || !to) return null
                  const a = nodeCenter(from)
                  const b = nodeCenter(to)
                  return (
                    <line
                      key={`${edge.from}->${edge.to}`}
                      x1={from.x + (from.kind === 'server' ? NODE_W : TRIGGER_W)}
                      y1={a.y}
                      x2={to.x}
                      y2={b.y}
                    />
                  )
                })}
              </svg>
              {state.nodes.map(renderNode)}
            </div>
          </div>
        </div>
        <footer className="orch-foot">
          <div className="orch-errors" role="status">
            {error ? <p className="is-error">{error}</p> : null}
            {errors.length > 0 ? (
              <ul>
                {errors.slice(0, 4).map((item) => (
                  <li key={item}>{item}</li>
                ))}
              </ul>
            ) : (
              <p className="is-ok">校验通过</p>
            )}
          </div>
          <span className="orch-hint">
            点击两个节点连线（服务器→触发器→服务器）；黄点/堆叠来自详情页。恢复触发器添加即连根。
          </span>
        </footer>
      </div>
    </div>
  )
}
