import { lazy, Suspense, useCallback, useEffect, useRef, useState } from 'react'
import {
  RouteIcon,
  ServerIcon,
  UsersIcon,
  WifiIcon,
} from 'lucide-react'

import { Notice, Page, PageHeader } from '@/components/PagePrimitives'
import { Card, CardDescription, CardHeader, CardTitle } from '@/components/ui/card'
import { api, errorMessage } from '@/lib/api'
import type { Chain, DashboardStats, Server } from '@/lib/types'

const loadGlobeTopology = () => import('@/components/GlobeTopology')
const GlobeTopology = lazy(loadGlobeTopology)

export default function Dashboard() {
  const [stats, setStats] = useState<DashboardStats | null>(null)
  const [servers, setServers] = useState<Server[]>([])
  const [chains, setChains] = useState<Chain[]>([])
  const [error, setError] = useState('')
  const loadRequest = useRef(0)

  const load = useCallback(async (silent = false, signal?: AbortSignal) => {
    const request = ++loadRequest.current
    const options = signal
      ? { signal, ...(silent ? { display: 'silent' as const } : {}) }
      : silent ? { display: 'silent' as const } : undefined
    try {
      const [dashboardStats, serverList, chainList] = await Promise.all([
        api.dashboard(options),
        api.servers(options),
        api.chains(options),
      ])
      if (signal?.aborted || request !== loadRequest.current) return
      setStats(dashboardStats)
      setServers(serverList)
      setChains(chainList)
      setError('')
    } catch (err) {
      if (signal?.aborted || request !== loadRequest.current) return
      setError(errorMessage(err))
    }
  }, [])

  useEffect(() => {
    void loadGlobeTopology().catch(() => {})
    const controller = new AbortController()
    let stopped = false
    let timer: number | undefined
    const poll = async (initial: boolean) => {
      await load(!initial, controller.signal)
      if (!stopped) timer = window.setTimeout(() => void poll(false), 5000)
    }
    void poll(true)
    return () => {
      stopped = true
      loadRequest.current += 1
      controller.abort()
      if (timer !== undefined) window.clearTimeout(timer)
    }
  }, [load])

  if (error) {
    return (
      <Notice tone="danger" title="控制面板连接失败" className="max-w-xl">
        {error}
      </Notice>
    )
  }
  if (!stats) {
    return (
      <div className="grid gap-4 sm:grid-cols-2 xl:grid-cols-4" aria-label="仪表盘加载中">
        {[0, 1, 2, 3].map((item) => (
          <div key={item} className="h-32 animate-pulse rounded-lg border bg-muted" />
        ))}
      </div>
    )
  }

  const cards = [
    {
      title: '服务器',
      value: stats.servers,
      desc: `在线 ${stats.servers_online} 台`,
      icon: ServerIcon,
    },
    {
      title: '在线节点',
      value: stats.servers_online,
      desc: `共 ${stats.servers} 台`,
      icon: WifiIcon,
    },
    {
      title: '链路',
      value: stats.links,
      desc: `正常 ${stats.links_active} 条${stats.links_degraded ? ` / 降级 ${stats.links_degraded} 条` : ''}`,
      icon: RouteIcon,
    },
    {
      title: '订阅用户',
      value: stats.users,
      desc: '当前用户总数',
      icon: UsersIcon,
    },
  ]

  const today = new Intl.DateTimeFormat('zh-CN', {
    month: 'long',
    day: 'numeric',
    weekday: 'short',
  }).format(new Date())

  return (
    <Page>
      <PageHeader
        title="仪表盘"
        description="服务器、链路与订阅用户的实时运行状态。"
        actions={(
          <div className="flex items-center gap-2 text-xs">
            <span className="rounded-sm border bg-card/70 px-2.5 py-1.5 text-muted-foreground">{today}</span>
            <span className="inline-flex items-center gap-2 rounded-sm border bg-card/70 px-2.5 py-1.5 font-medium">
              <span className="size-1.5 rounded-full bg-success shadow-[0_0_8px_var(--success)]" />
              已同步
            </span>
          </div>
        )}
      />

      <div className="grid gap-4 sm:grid-cols-2 xl:grid-cols-4">
        {cards.map((card) => (
          <Card key={card.title} className="status-tile relative gap-0 py-0">
            <span className="absolute inset-y-4 left-0 w-px bg-primary/70 shadow-[0_0_10px_var(--primary)]" />
            <CardHeader className="gap-3 px-4 py-4">
              <div className="flex items-center justify-between gap-3">
                <CardDescription className="text-xs font-medium">{card.title}</CardDescription>
                <span className="grid size-8 place-items-center rounded-sm border border-primary/15 bg-primary/5 text-primary">
                  <card.icon className="size-4" strokeWidth={1.7} />
                </span>
              </div>
              <CardTitle className="text-3xl font-semibold tabular-nums">
                {String(card.value).padStart(2, '0')}
              </CardTitle>
              <CardDescription className="text-xs">{card.desc}</CardDescription>
            </CardHeader>
          </Card>
        ))}
      </div>

      <div className="grid gap-4 xl:grid-cols-[minmax(0,1fr)_300px]">
        <section className="game-panel overflow-hidden">
          <div className="flex items-center justify-between border-b bg-card/60 px-4 py-3.5">
            <div>
              <h2 className="text-sm font-semibold">链路拓扑</h2>
              <p className="mt-1 text-[11px] text-muted-foreground">服务器地理位置与链路状态</p>
            </div>
            <span className="inline-flex items-center gap-2 rounded-sm border bg-muted/50 px-2.5 py-1.5 text-[10px] text-muted-foreground">
              <span className="size-1.5 rounded-full bg-primary" />
              5 秒刷新
            </span>
          </div>
          <Suspense
            fallback={(
              <div
                className="grid min-h-[430px] place-items-center bg-transparent sm:min-h-[520px]"
                role="status"
              >
                <span className="size-4 animate-pulse rounded-sm bg-primary" />
                <span className="sr-only">正在加载链路拓扑</span>
              </div>
            )}
          >
            <GlobeTopology servers={servers} chains={chains} />
          </Suspense>
        </section>

        <div className="grid content-start gap-4">
          <section className="game-panel overflow-hidden">
            <div className="border-b px-4 py-3.5">
              <h2 className="text-sm font-semibold">运行概况</h2>
              <p className="mt-1 text-[11px] text-muted-foreground">核心资源当前状态</p>
            </div>
            <div className="divide-y divide-border">
              {[
                { label: '活跃链路', value: stats.links_active, detail: `${stats.links} 条总计`, icon: RouteIcon },
                { label: '在线节点', value: stats.servers_online, detail: `${stats.servers} 台总计`, icon: WifiIcon },
                { label: '订阅用户', value: stats.users, detail: '已配置账户', icon: UsersIcon },
              ].map((item) => (
                <div key={item.label} className="grid grid-cols-[34px_minmax(0,1fr)_auto] items-center gap-3 px-4 py-4 transition-colors hover:bg-primary/[0.035]">
                  <span className="grid size-8 place-items-center rounded-sm border border-primary/15 bg-primary/5 text-primary">
                    <item.icon className="size-4" strokeWidth={1.7} />
                  </span>
                  <span className="min-w-0">
                    <strong className="block text-xs font-medium">{item.label}</strong>
                    <small className="mt-1 block text-[10px] text-muted-foreground">{item.detail}</small>
                  </span>
                  <strong className="text-lg font-semibold tabular-nums">{item.value}</strong>
                </div>
              ))}
            </div>
          </section>

          {stats.links_degraded > 0 ? (
            <Notice tone="warning" title="链路降级">
              {stats.links_degraded} 条链路需要检查
            </Notice>
          ) : (
            <Notice tone="success" title="网络状态">
              当前没有降级链路
            </Notice>
          )}
        </div>
      </div>
    </Page>
  )
}
