import { useEffect, useState } from 'react'

import { Card, CardDescription, CardHeader, CardTitle } from '@/components/ui/card'
import { api, errorMessage } from '@/lib/api'
import type { DashboardStats } from '@/lib/types'

export default function Dashboard() {
  const [stats, setStats] = useState<DashboardStats | null>(null)
  const [error, setError] = useState('')

  useEffect(() => {
    const load = () => {
      api
        .dashboard()
        .then(setStats)
        .catch((err) => setError(errorMessage(err)))
    }
    load()
    const timer = setInterval(load, 5000)
    return () => clearInterval(timer)
  }, [])

  if (error) {
    return <p className="text-sm text-destructive">加载失败：{error}</p>
  }
  if (!stats) {
    return <p className="text-sm text-muted-foreground">加载中…</p>
  }

  const cards = [
    { title: '服务器总数', value: stats.servers, desc: `在线 ${stats.servers_online} 台` },
    { title: '在线服务器', value: stats.servers_online, desc: `共 ${stats.servers} 台` },
    {
      title: '链路',
      value: stats.links,
      desc: `正常 ${stats.links_active} 条${stats.links_degraded ? ` · 降级 ${stats.links_degraded} 条` : ''}`,
    },
    { title: '用户', value: stats.users, desc: '订阅用户总数' },
  ]

  return (
    <div className="space-y-6">
      <h1 className="text-xl font-semibold">仪表盘</h1>
      <div className="grid gap-4 sm:grid-cols-2 xl:grid-cols-4">
        {cards.map((card) => (
          <Card key={card.title}>
            <CardHeader>
              <CardDescription>{card.title}</CardDescription>
              <CardTitle className="text-3xl tabular-nums">{card.value}</CardTitle>
              <CardDescription>{card.desc}</CardDescription>
            </CardHeader>
          </Card>
        ))}
      </div>
    </div>
  )
}
