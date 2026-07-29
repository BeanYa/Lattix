import { useEffect, useState } from 'react'
import {
  RouteIcon,
  ServerIcon,
  UsersIcon,
  WifiIcon,
} from 'lucide-react'

import GlobeTopology from '@/components/GlobeTopology'
import { Notice } from '@/components/PagePrimitives'
import { Card, CardDescription, CardHeader, CardTitle } from '@/components/ui/card'
import { api, errorMessage } from '@/lib/api'
import type { Chain, DashboardStats, Server } from '@/lib/types'

export default function Dashboard() {
  const [stats, setStats] = useState<DashboardStats | null>(null)
  const [servers, setServers] = useState<Server[]>([])
  const [chains, setChains] = useState<Chain[]>([])
  const [error, setError] = useState('')

  useEffect(() => {
    const load = (silent = false) => {
      const options = silent ? { display: 'silent' as const } : undefined
      Promise.all([api.dashboard(options), api.servers(options), api.chains(options)])
        .then(([dashboardStats, serverList, chainList]) => {
          setStats(dashboardStats)
          setServers(serverList)
          setChains(chainList)
          setError('')
        })
        .catch((err) => setError(errorMessage(err)))
    }
    load()
    const timer = setInterval(() => load(true), 5000)
    return () => clearInterval(timer)
  }, [])

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
      tone: 'bg-[var(--pastel-blue)]',
    },
    {
      title: '在线节点',
      value: stats.servers_online,
      desc: `共 ${stats.servers} 台`,
      icon: WifiIcon,
      tone: 'bg-[var(--pastel-mint)]',
    },
    {
      title: '链路',
      value: stats.links,
      desc: `正常 ${stats.links_active} 条${stats.links_degraded ? ` / 降级 ${stats.links_degraded} 条` : ''}`,
      icon: RouteIcon,
      tone: 'bg-[var(--pastel-yellow)]',
    },
    {
      title: '订阅用户',
      value: stats.users,
      desc: '当前用户总数',
      icon: UsersIcon,
      tone: 'bg-[var(--pastel-coral)]',
    },
  ]

  const today = new Intl.DateTimeFormat('zh-CN', {
    month: 'long',
    day: 'numeric',
    weekday: 'short',
  }).format(new Date())

  return (
    <div className="space-y-5 font-pixel [font-synthesis:none]">
      <header className="flex flex-col gap-3 border-b pb-5 sm:flex-row sm:items-end sm:justify-between">
        <div>
          <p className="mb-1 text-sm font-medium text-muted-foreground">Lattix 控制中心</p>
          <h1 className="text-2xl font-extrabold">仪表盘</h1>
        </div>
        <div className="flex items-center gap-2 text-sm">
          <span className="rounded-full bg-secondary px-3 py-1.5 font-medium text-secondary-foreground">{today}</span>
          <span className="inline-flex items-center gap-2 rounded-full border bg-card px-3 py-1.5 font-medium">
            <span className="size-2 rounded-full bg-success" />
            已同步
          </span>
        </div>
      </header>

      <div className="grid gap-4 sm:grid-cols-2 xl:grid-cols-4">
        {cards.map((card) => (
          <Card key={card.title} className={`status-tile ${card.tone}`}>
            <CardHeader className="gap-3">
              <div className="flex items-center justify-between gap-3">
                <CardDescription className="font-semibold text-foreground">{card.title}</CardDescription>
                <span className="grid size-9 place-items-center rounded-lg border bg-card/70">
                  <card.icon className="size-5" strokeWidth={1.8} />
                </span>
              </div>
              <CardTitle className="text-4xl font-extrabold tabular-nums">
                {String(card.value).padStart(2, '0')}
              </CardTitle>
              <CardDescription className="text-foreground/60">{card.desc}</CardDescription>
            </CardHeader>
          </Card>
        ))}
      </div>

      <div className="grid gap-5 xl:grid-cols-[minmax(0,1fr)_280px]">
        <section className="game-panel overflow-hidden">
          <div className="flex items-center justify-between border-b bg-[var(--pastel-blue)] px-5 py-4">
            <div>
              <h2 className="font-extrabold">链路拓扑</h2>
              <p className="mt-1 text-xs text-muted-foreground">服务器地理位置与链路状态</p>
            </div>
            <span className="rounded-full border bg-card/80 px-3 py-1.5 text-xs font-semibold">5 秒刷新</span>
          </div>
          <GlobeTopology servers={servers} chains={chains} />
        </section>

        <div className="grid content-start gap-5">
          <section className="game-panel p-5">
            <h2 className="font-extrabold">运行概况</h2>
            <div className="mt-4 grid gap-3">
              {[
                { label: '活跃链路', value: stats.links_active, icon: RouteIcon, color: 'bg-[var(--pastel-yellow)]' },
                { label: '在线节点', value: stats.servers_online, icon: WifiIcon, color: 'bg-[var(--pastel-mint)]' },
                { label: '订阅用户', value: stats.users, icon: UsersIcon, color: 'bg-[var(--pastel-coral)]' },
              ].map((item) => (
                <div key={item.label} className="flex items-center gap-3 rounded-lg border bg-card p-3">
                  <span className={`grid size-10 place-items-center rounded-lg border ${item.color}`}>
                    <item.icon className="size-5" />
                  </span>
                  <span className="min-w-0 flex-1 font-medium">{item.label}</span>
                  <strong className="text-xl tabular-nums">{item.value}</strong>
                </div>
              ))}
            </div>
          </section>
        </div>
      </div>
    </div>
  )
}
