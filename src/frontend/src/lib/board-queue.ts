import { useCallback, useEffect, useMemo, useRef, useState } from 'react'

import { api, errorMessage } from '@/lib/api'
import type { Chain, Server, SubUser } from '@/lib/types'
import { usePolling } from '@/lib/use-polling'

/**
 * 控制台待处理队列的数据派生层（方案 3.1 / 5.1.1）。
 *
 * 数据源：复用现有 api.servers、api.chains 与 api.users，
 * 由 requester 统一处理重试、取消与错误；不新增请求库。
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
  /** 影响面文案；由关联链路与用户列表客户端 join 派生。 */
  impact?: string
  /** 影响用户数（用于同严重度内排序）。 */
  impactUsers?: number
  /** 链路色槽位（0-8），仅 kind='chain' 时存在。 */
  lineIndex?: number
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
  usersTotal: number
  usersOnline: number
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
    return server.last_disconnected_at ?? server.last_seen_at ?? undefined
  }
  if (server.connection_state === 'never_connected') {
    return server.created_at || undefined
  }
  return server.agent_settings_reported_at ?? undefined
}

/** 链路异常的发生时间；契约无状态变更时间，退回 created_at 作近似。 */
function chainSince(chain: Chain): string | undefined {
  return chain.created_at || undefined
}

/** 计算单条链路影响的去重用户 ID 集合。 */
function usersForChain(chainId: number, users: SubUser[]): Set<number> {
  const matched = new Set<number>()
  for (const user of users) {
    const chainIds = user.effective_chain_ids ?? user.chain_ids ?? []
    if (chainIds.includes(chainId)) {
      matched.add(user.id)
    }
  }
  return matched
}

/** 服务器 → 队列项；不命中异常时返回 null。 */
function buildServerItem(
  server: Server,
  chains: Chain[],
  users: SubUser[] | null,
): BoardItem | null {
  const severity = serverSeverity(server)
  if (!severity) return null
  const since = serverSince(server)
  const href = `/network/servers?sel=${server.id}`
  const affectedChains = chains.filter((chain) =>
    chain.hops.some((hop) => hop.server_id === server.id),
  )
  let impact: string | undefined
  let impactUsers = 0
  if (users !== null) {
    const userSet = new Set<number>()
    for (const chain of affectedChains) {
      for (const uid of usersForChain(chain.id, users)) {
        userSet.add(uid)
      }
    }
    impactUsers = userSet.size
    impact =
      affectedChains.length > 0
        ? `${affectedChains.length} 条链路 · ${userSet.size} 位用户`
        : '0 条链路 · 0 位用户'
  }
  return {
    id: `server-${server.id}`,
    kind: 'server',
    severity,
    title: server.alias || `服务器 #${server.id}`,
    reason: serverReasons(server).join('；'),
    ...(impact ? { impact, impactUsers } : {}),
    ...(since ? { since } : {}),
    href,
    actions: [{ label: '查看服务器', href, tone: 'primary' }],
  }
}

/** 链路 → 队列项；处理 failed / active_failed / degraded / waiting_for_agent。 */
function buildChainItem(chain: Chain, users: SubUser[] | null): BoardItem | null {
  if (
    chain.status !== 'failed' &&
    chain.status !== 'active_failed' &&
    chain.status !== 'degraded' &&
    chain.status !== 'waiting_for_agent'
  ) {
    return null
  }
  const isCritical = chain.status === 'failed' || chain.status === 'active_failed'
  const severity: BoardSeverity = isCritical ? 'critical' : 'warning'
  const since = chainSince(chain)
  const href = `/network/chains/${chain.id}`
  const actions: BoardItem['actions'] = [{ label: '查看链路', href, tone: 'primary' }]
  const entryServerId = chain.hops[0]?.server_id
  if (entryServerId !== undefined) {
    actions.push({ label: '打开入口服务器', href: `/network/servers?sel=${entryServerId}` })
  }
  let impact: string | undefined
  let impactUsers = 0
  if (users !== null) {
    const userSet = usersForChain(chain.id, users)
    impactUsers = userSet.size
    impact = `影响 ${userSet.size} 位用户 · ${chain.hops.length} 跳`
  }
  const failedHop = chain.hops.find((h) => h.status === 'failed')
  const reason = isCritical
    ? `链路异常：${chain.error || (failedHop ? `跳点 ${failedHop.server_alias} 部署失败` : '发布失败，请检查失败跳')}`
    : chain.status === 'waiting_for_agent'
      ? `等待 Agent：${chain.error || '目标服务器离线，新配置已在队列中等待下发'}`
      : `链路降级：${chain.error || '部分跳点 Agent 离线，已发布 revision 仍保持生效'}`

  return {
    id: `chain-${chain.id}`,
    kind: 'chain',
    severity,
    title: chain.name || `链路 #${chain.id}`,
    reason,
    lineIndex: chain.id % 9,
    ...(impact ? { impact, impactUsers } : {}),
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

/** 先按 severity，再按影响人数降序，再按发生时间倒序，最后按名称保证稳定排序。 */
function compareItems(left: BoardItem, right: BoardItem): number {
  const bySeverity = SEVERITY_RANK[left.severity] - SEVERITY_RANK[right.severity]
  if (bySeverity !== 0) return bySeverity
  const byImpact = (right.impactUsers ?? -1) - (left.impactUsers ?? -1)
  if (byImpact !== 0) return byImpact
  const byTime = itemTimestamp(right) - itemTimestamp(left)
  if (byTime !== 0) return byTime
  return left.title.localeCompare(right.title, 'zh-CN')
}

/** 从列表派生全部队列项（已排序、未截断）。 */
function deriveBoardItems(
  servers: Server[],
  chains: Chain[],
  users: SubUser[] | null,
): BoardItem[] {
  const items: BoardItem[] = []
  for (const server of servers) {
    const item = buildServerItem(server, chains, users)
    if (item) items.push(item)
  }
  for (const chain of chains) {
    const item = buildChainItem(chain, users)
    if (item) items.push(item)
  }
  return items.sort(compareItems)
}

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
  const [users, setUsers] = useState<SubUser[] | null>(null)
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState<string | null>(null)
  const [updatedAt, setUpdatedAt] = useState<string | null>(null)
  const request = useRef(0)

  const load = useCallback(async (silent = false, signal?: AbortSignal, force = false) => {
    if (!force && typeof document !== 'undefined' && document.hidden) return
    const current = ++request.current
    const options = {
      signal,
      display: silent ? ('silent' as const) : ('foreground' as const),
    }
    const [serverResult, chainResult, userResult] = await Promise.allSettled([
      api.servers(options),
      api.chains(options),
      api.users({ signal, display: 'silent' }),
    ])
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
    if (userResult.status === 'fulfilled') {
      setUsers(userResult.value)
    }
    setError(failures.length > 0 ? failures.join('；') : null)
    if (serverResult.status === 'fulfilled' || chainResult.status === 'fulfilled') {
      setUpdatedAt(new Date().toISOString())
    }
    setLoading(false)
  }, [])

  usePolling(load, request, POLL_INTERVAL_MS)

  useEffect(() => {
    if (typeof document === 'undefined') return
    const handleVisibilityChange = () => {
      if (!document.hidden) void load(true, undefined, true)
    }
    document.addEventListener('visibilitychange', handleVisibilityChange)
    return () => document.removeEventListener('visibilitychange', handleVisibilityChange)
  }, [load])

  const allItems = useMemo(() => deriveBoardItems(servers, chains, users), [servers, chains, users])
  const counts = useMemo(() => {
    let critical = 0
    let warning = 0
    for (const item of allItems) {
      if (item.severity === 'critical') critical += 1
      else warning += 1
    }
    return { critical, warning }
  }, [allItems])

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
    const userList = users ?? []
    let usersOnline = 0
    for (const u of userList) {
      if (u.online_connections > 0) usersOnline += 1
    }
    return {
      serversTotal: servers.length,
      serversOnline,
      serversDrift,
      chainsTotal: chains.length,
      chainsHealthy,
      usersTotal: userList.length,
      usersOnline,
      updatedAt,
    }
  }, [servers, chains, users, updatedAt])

  const items = useMemo(() => allItems.slice(0, MAX_ITEMS), [allItems])

  const refresh = useCallback(() => {
    void load(false, undefined, true)
  }, [load])

  return { items, counts, summary, loading, error, refresh }
}
