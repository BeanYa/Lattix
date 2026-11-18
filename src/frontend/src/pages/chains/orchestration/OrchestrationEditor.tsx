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
  moveNode,
  removeEdge,
  removeNode,
  setPriority,
  setServerAddress,
  setServerListenPort,
  toSavePayload,
  validateGraph,
  type EditorState,
  type TriggerAction,
  type TriggerSubject,
} from './orchestration-graph'
import { OrchestrationCanvas } from './OrchestrationCanvas'
// 与链路编辑器同源：卡片、端口圆点、世界层与连线样式全部复用链路画布样式表。
import '../chain-wizard/chain-wizard.css'
import './orchestration.css'

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
  const [connecting, setConnecting] = useState<string | null>(null)
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

  const startConnect = (key: string) => {
    setSelected(null)
    setConnecting((current) => (current === key ? null : key))
  }

  const completeConnect = (key: string) => {
    if (!connecting || connecting === key) return
    const from = state.nodes.find((node) => node.key === connecting)
    const to = state.nodes.find((node) => node.key === key)
    if (!from || !to) return
    const next =
      from.kind === 'server' && to.kind === 'trigger'
        ? connect(state, connecting, key)
        : from.kind === 'trigger' && to.kind === 'server'
          ? connect(state, connecting, key)
          : state
    // 触发器换目标时边数不变（替换出边），因此按「目标边是否存在」判断成功与否。
    const linked = next.edges.some((edge) => edge.from === connecting && edge.to === key)
    if (!linked) {
      setError('无法连接：服务器只能连到触发器入口，触发器只能连到服务器入口')
    } else {
      setError('')
      setState(next)
    }
    setConnecting(null)
  }

  const remove = (key: string) => {
    if (key.includes('->')) {
      const [from, to] = key.split('->')
      setState(removeEdge(state, from, to))
      return
    }
    const next = removeNode(state, key)
    if (next === state) {
      setError('根服务器与当前使用的服务器不能删除')
      return
    }
    setError('')
    setState(next)
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
          <OrchestrationCanvas
            state={state}
            hopRole={hop.role}
            serverMap={serverMap}
            selected={selected}
            connecting={connecting}
            onStartConnect={startConnect}
            onCompleteConnect={completeConnect}
            onRemove={remove}
            onMove={(key, x, y) => setState(moveNode(state, key, x, y))}
            onPriority={(key, value) => setState(setPriority(state, key, value))}
            onPort={(key, value) => setState(setServerListenPort(state, key, value))}
            onAddress={(key, value) => setState(setServerAddress(state, key, value))}
          />
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
            点服务器出口圆点 → 触发器入口圆点连线；恢复触发器添加即连根；拖动卡片可调整布局。
          </span>
        </footer>
      </div>
    </div>
  )
}
