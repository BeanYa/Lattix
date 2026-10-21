import { useCallback, useEffect, useMemo, useRef, useState } from 'react'

import { api, errorMessage } from '@/lib/api'
import type { Chain, Server } from '@/lib/types'
import { usePolling } from '@/lib/use-polling'

/**
 * 控制台待处理队列的数据派生层（方案 3.1 / 5.1.1）。
 *
 * 数据源：仅复用现有 api.servers（GET /api/server/list）与 api.chains（GET /api/chain/list），
 * 由 requester 统一处理重试、取消与错误；不新增请求库，也不请求 dashboard / user 等其它接口。
 *
 * 派生规则：
 * - 服务器：connection_state 为 offline / auth_rejected / never_connected → critical；
 *   config_drift=true → warning。同一服务器即使同时命中多个条件也只产生一条（取更严重的级别，
 *   原因合并展示）。
 * - 链路：status=failed → critical；status=degraded → warning。
 * - 排序：先按 severity（critical 在前），同级再按 since（发生时间）倒序，最后按名称兜底。
 * - 展示：最多 8 条；counts 基于全部条目统计，不受截断影响。
 *
 * 假设：
 * - 服务器断开类异常用 last_disconnected_at 作为发生时间（缺失时退回 last_seen_at）；
 *   never_connected 用 created_at；配置漂移没有独立的开始时间字段，用最近一次
 *   agent_settings_reported_at 近似，仍缺失则不填 since（页面可显示「持续时长未知」）。
 * - Chain 契约没有状态变更时间或 updated_at，created_at 是唯一可用时间，仅作为排序与
 *   持续时长的近似值。
 * - 影响面（impact）需要按方案 5.1.1 对 /api/user/list 做客户端 join；本 hook 按约束
 *   不请求用户列表，因此不填 impact，由页面显示「影响面未知」而不是 0。
 * - 动作只提供跳转链接（查看对象 / 入口服务器），不触发任何写操作。
 */

/** 队列严重度：critical = 故障，warning = 降级 / 待处理。 */
export type BoardSeverity = 'critical' | 'warning'

/** 控制台单条待处理事项；同一对象（服务器或链路）最多一条。 */
export type BoardItem = {
  /** 稳定标识，形如 server-12 / chain-7，可直接作为列表 key。 */
  id: string
  kind: 'server' | 'chain'
  severity: BoardSeverity
  /** 对象名称：服务器 alias / 链路 name。 */
  title: string
  /** 触发原因；同时命中多个条件时以「；」拼接。 */
  reason: string
  /** 影响面文案；当前数据源无法计算，故通常为空。 */
  impact?: string
  /** 异常可确认的起始时间（ISO 字符串）；无可靠时间时不填。 */
  since?: string
  /** 对象详情页或带选中态的列表地址。 */
  href: string
  actions?: { label: string; href: string; tone?: 'primary' | 'default' }[]
}

/** 机群脉搏所需的汇总读数；与队列同一轮请求派生，避免页面重复拉取。 */
export type BoardSummary = {
  serversTotal: number
  serversOnline: number
  serversDrift: number
  chainsTotal: number
  chainsHealthy: number
  /** 最近一次成功刷新的时间（ISO）；全部请求失败时保留上一次值。 */
  updatedAt: string | null
}

/** 控制台轮询间隔：30 秒静默刷新一次。 */
const POLL_INTERVAL_MS = 30_000
/** 队列最多展示 8 条；计数仍基于全部条目。 */
const MAX_ITEMS = 8

/** critical 排在 warning 之前。 */
const SEVERITY_RANK: Record<BoardSeverity, number> = { critical: 0, warning: 1 }

/** 服务器是否命中异常；命中时返回该对象应得的最高严重度。 */
function serverSeverity(server: Server): BoardSeverity | null {
  if (
    server.connection_state === 'offline' ||
    server.connection_state === 'auth_rejected' ||
    server.connection_state === 'never_connected'
  ) {
    return 'critical'
  }
  return server.config_drift ? 'warning' : null
}

/** 汇总单个服务器的全部命中原因；同一对象只生成一条队列项时用于拼接。 */
function serverReasons(server: Server): string[] {
  const reasons: string[] = []
  if (server.connection_state === 'offline') {
    reasons.push(
      server.last_disconnect_reason
        ? `Agent 已离线：${server.last_disconnect_reason}`
        : 'Agent 已离线，面板无法下发配置或同步状态',
    )
  } else if (server.connection_state === 'auth_rejected') {
    reasons.push(
      server.last_disconnect_reason
        ? `Agent 认证被拒绝：${server.last_disconnect_reason}`
        : 'Agent 认证被拒绝：令牌可能已失效，请轮换令牌后重连',
    )
  } else if (server.connection_state === 'never_connected') {
    reasons.push('服务器从未成功连接 Agent：请检查安装命令与网络连通性')
  }
  if (server.config_drift) {
    reasons.push('配置漂移：Agent 上报值与期望配置不一致')
  }
  return reasons
}

/** 服务器异常的发生时间；仅使用后端已暴露的时间字段，避免凭空造时间。 */
function serverSince(server: Server): string | undefined {
  if (server.connection_state === 'offline' || server.connection_state === 'auth_rejected') {
    // 断开时间是最直接的故障起点；缺失时退回最近一次心跳，只能作为下界。
    return server.last_disconnected_at ?? server.last_seen_at ?? undefined
  }
  if (server.connection_state === 'never_connected') {
    // 从未连接成功：创建时间即「一直未接入」的起点。
    return server.created_at || undefined
  }
  // 仅配置漂移：没有独立的漂移开始时间，用最近一次配置上报时间近似。
  return server.agent_settings_reported_at ?? undefined
}

/** 链路异常的发生时间；契约无状态变更时间，只能退回 created_at 作近似。 */
function chainSince(chain: Chain): string | undefined {
  return chain.created_at || undefined
}

/** 服务器 → 队列项；不命中异常时返回 null。 */
function buildServerItem(server: Server): BoardItem | null {
  const severity = serverSeverity(server)
  if (!severity) return null
  const since = serverSince(server)
  const href = `/network/servers?sel=${server.id}`
  return {
    id: `server-${server.id}`,
    kind: 'server',
    severity,
    title: server.alias || `服务器 #${server.id}`,
    reason: serverReasons(server).join('；'),
    ...(since ? { since } : {}),
    href,
    actions: [{ label: '查看服务器', href, tone: 'primary' }],
  }
}

/** 链路 → 队列项；只处理 failed / degraded，其余状态（含 deleted）不进队列。 */
function buildChainItem(chain: Chain): BoardItem | null {
  if (chain.status !== 'failed' && chain.status !== 'degraded') return null
  const severity: BoardSeverity = chain.status === 'failed' ? 'critical' : 'warning'
  const since = chainSince(chain)
  const href = `/network/chains/${chain.id}`
  const actions: BoardItem['actions'] = [{ label: '查看链路', href, tone: 'primary' }]
  const entryServerId = chain.hops[0]?.server_id
  if (entryServerId !== undefined) {
    // 入口跳是订阅入口，排障时通常先看它。
    actions.push({ label: '打开入口服务器', href: `/network/servers?sel=${entryServerId}` })
  }
  return {
    id: `chain-${chain.id}`,
    kind: 'chain',
    severity,
    title: chain.name || `链路 #${chain.id}`,
    reason:
      chain.status === 'failed'
        ? `链路失败：${chain.error || '发布失败，请检查失败跳'}`
        : `链路降级：${chain.error || '部分跳不可用，已发布配置仍在生效'}`,
    ...(since ? { since } : {}),
    href,
    actions,
  }
}

/** 取排序用时间戳；缺失或非法时间视为最旧，排在同级末尾。 */
function itemTimestamp(item: BoardItem): number {
  if (!item.since) return 0
  const timestamp = Date.parse(item.since)
  return Number.isNaN(timestamp) ? 0 : timestamp
}

/** 先按 severity，再按发生时间倒序，最后按名称保证稳定排序。 */
function compareItems(left: BoardItem, right: BoardItem): number {
  const bySeverity = SEVERITY_RANK[left.severity] - SEVERITY_RANK[right.severity]
  if (bySeverity !== 0) return bySeverity
  const byTime = itemTimestamp(right) - itemTimestamp(left)
  if (byTime !== 0) return byTime
  return left.title.localeCompare(right.title, 'zh-CN')
}

/** 从两份列表派生全部队列项（已排序、未截断）。 */
function deriveBoardItems(servers: Server[], chains: Chain[]): BoardItem[] {
  const items: BoardItem[] = []
  for (const server of servers) {
    const item = buildServerItem(server)
    if (item) items.push(item)
  }
  for (const chain of chains) {
    const item = buildChainItem(chain)
    if (item) items.push(item)
  }
  return items.sort(compareItems)
}

/**
 * 控制台队列控制器：
 * - 挂载即加载，之后每 30 秒静默轮询；document.hidden 时跳过请求（暂停），
 *   回到前台立即补一次静默刷新；
 * - servers / chains 两路请求用 Promise.allSettled 并行，单路失败保留该路上一轮数据，
 *   另一路照常更新，失败原因汇总进 error；
 * - refresh 强制重取（跳过隐藏守卫，并让在途旧响应作废）。
 */
export function useBoardQueue(): {
  items: BoardItem[]
  counts: { critical: number; warning: number }
  summary: BoardSummary
  loading: boolean
  error: string | null
  refresh: () => void
} {
  const [servers, setServers] = useState<Server[]>([])
  const [chains, setChains] = useState<Chain[]>([])
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState<string | null>(null)
  const [updatedAt, setUpdatedAt] = useState<string | null>(null)
  const request = useRef(0)

  const load = useCallback(async (silent = false, signal?: AbortSignal, force = false) => {
    // usePolling 不能暂停计时器，因此在加载入口直接跳过：隐藏期间不发起网络请求。
    if (!force && typeof document !== 'undefined' && document.hidden) return
    const current = ++request.current
    const options = {
      signal,
      display: silent ? ('silent' as const) : ('foreground' as const),
    }
    // 两路互不阻塞：allSettled 保证任一失败都不会丢掉另一路的结果。
    const [serverResult, chainResult] = await Promise.allSettled([
      api.servers(options),
      api.chains(options),
    ])
    // 已卸载或被更新的一轮请求取代：丢弃整轮回包，避免旧数据覆盖新数据。
    if (signal?.aborted || current !== request.current) return
    const failures: string[] = []
    if (serverResult.status === 'fulfilled') {
      setServers(serverResult.value)
    } else {
      failures.push(`服务器列表加载失败：${errorMessage(serverResult.reason)}`)
    }
    if (chainResult.status === 'fulfilled') {
      setChains(chainResult.value)
    } else {
      failures.push(`链路列表加载失败：${errorMessage(chainResult.reason)}`)
    }
    setError(failures.length > 0 ? failures.join('；') : null)
    // 只要有一路成功，就把「更新于」推进到本轮；全部失败时保留上次成功时间。
    if (serverResult.status === 'fulfilled' || chainResult.status === 'fulfilled') {
      setUpdatedAt(new Date().toISOString())
    }
    setLoading(false)
  }, [])

  usePolling(load, request, POLL_INTERVAL_MS)

  // 回到前台立即补一次静默刷新；隐藏期间的定时轮询已在 load 入口跳过。
  useEffect(() => {
    if (typeof document === 'undefined') return
    const handleVisibilityChange = () => {
      if (!document.hidden) void load(true, undefined, true)
    }
    document.addEventListener('visibilitychange', handleVisibilityChange)
    return () => document.removeEventListener('visibilitychange', handleVisibilityChange)
  }, [load])

  // 先派生全部条目，再截断展示；counts 必须基于全量，否则会漏报。
  const allItems = useMemo(() => deriveBoardItems(servers, chains), [servers, chains])
  const counts = useMemo(() => {
    let critical = 0
    let warning = 0
    for (const item of allItems) {
      if (item.severity === 'critical') critical += 1
      else warning += 1
    }
    return { critical, warning }
  }, [allItems])
  // 机群脉搏：只做计数，不引入新的请求；链路「正常」= active / active_unconfirmed。
  const summary = useMemo<BoardSummary>(() => {
    let serversOnline = 0
    let serversDrift = 0
    for (const server of servers) {
      if (server.connection_state === 'online') serversOnline += 1
      if (server.config_drift) serversDrift += 1
    }
    let chainsHealthy = 0
    for (const chain of chains) {
      if (chain.status === 'active' || chain.status === 'active_unconfirmed') chainsHealthy += 1
    }
    return {
      serversTotal: servers.length,
      serversOnline,
      serversDrift,
      chainsTotal: chains.length,
      chainsHealthy,
      updatedAt,
    }
  }, [servers, chains, updatedAt])

  const items = useMemo(() => allItems.slice(0, MAX_ITEMS), [allItems])

  // 用户主动刷新：绕过隐藏守卫，并让在途旧响应失效。
  const refresh = useCallback(() => {
    void load(false, undefined, true)
  }, [load])

  return { items, counts, summary, loading, error, refresh }
}
