import { useEffect, useState } from 'react'

interface SubInfo {
  name: string
  expires_at?: number
  expired: boolean
  disabled: boolean
  used_up: number
  used_down: number
  traffic_limit: number
  nodes_count: number
  title: string
  announcement: string
  update_interval: string
}

interface ClientInfo {
  name: string
  platform: string
  deeplink: string
  format: string
}

function humanizeBytes(n: number): string {
  if (n < 1024) return `${n} B`
  const units = ['KB', 'MB', 'GB', 'TB']
  let v = n / 1024
  let i = 0
  while (v >= 1024 && i < units.length - 1) { v /= 1024; i++ }
  return v >= 100 ? `${v.toFixed(0)} ${units[i]}` : `${v.toFixed(1)} ${units[i]}`
}

const platformLabels: Record<string, string> = {
  ios: 'iOS',
  android: 'Android',
  windows: 'Windows',
  macos: 'macOS',
  universal: '通用',
}

export default function SubscriptionPage() {
  const token = window.location.pathname.split('/sub/')[1] || ''
  const [info, setInfo] = useState<SubInfo | null>(null)
  const [clients, setClients] = useState<ClientInfo[]>([])
  const [error, setError] = useState('')
  const [copied, setCopied] = useState('')

  const handleCopy = (text: string, label: string) => {
    navigator.clipboard?.writeText(text).catch(() => {})
    setCopied(label)
    setTimeout(() => setCopied(''), 1500)
  }

  useEffect(() => {
    if (!token) { setError('无效订阅链接'); return }
    fetch(`/api/sub/${token}/info`).then(r => {
      if (!r.ok) throw new Error(r.status === 404 ? '订阅不存在' : '加载失败')
      return r.json()
    }).then(setInfo).catch(e => setError(e.message))
    fetch(`/api/sub/${token}/clients`).then(r => r.ok ? r.json() : []).then(setClients).catch(() => {})
  }, [token])

  const subURL = `${window.location.origin}/sub/${token}`

  if (error) {
    return (
      <div className="flex min-h-[100dvh] items-center justify-center bg-[#0f1420] p-4">
        <div className="rounded-xl border border-[#2a3350] bg-[#1a2132] p-8 text-center text-[#f87171]">
          {error}
        </div>
      </div>
    )
  }

  if (!info) {
    return (
      <div className="flex min-h-[100dvh] items-center justify-center bg-[#0f1420]">
        <div className="animate-pulse text-sm text-[#9aa4c0]">加载中...</div>
      </div>
    )
  }

  const usedTotal = info.used_up + info.used_down
  const usagePercent = info.traffic_limit > 0 ? Math.min(100, (usedTotal / info.traffic_limit) * 100) : 0
  const overLimit = info.traffic_limit > 0 && usedTotal > info.traffic_limit

  // 按平台分组客户端
  const platforms = ['ios', 'android', 'windows', 'universal']
  const grouped = platforms.map(p => ({
    platform: p,
    label: platformLabels[p] || p,
    clients: clients.filter(c => c.platform === p),
  })).filter(g => g.clients.length > 0)

  return (
    <div className="flex min-h-[100dvh] flex-col items-center justify-center bg-[#0f1420] p-4 text-[#e6e9f0]">
      <div className="w-full max-w-[560px] rounded-[14px] border border-[#2a3350] bg-[#1a2132] p-6">
        {/* 头部 */}
        <div className="flex items-center justify-between">
          <h1 className="text-xl font-semibold">{info.title}</h1>
          <div className="flex gap-2">
            {info.disabled && <span className="rounded-full bg-[#4a1d1d] px-3 py-1 text-xs text-[#f87171]">已停用</span>}
            {info.expired && <span className="rounded-full bg-[#4a1d1d] px-3 py-1 text-xs text-[#f87171]">已到期</span>}
            {!info.disabled && !info.expired && <span className="rounded-full bg-[#123f2b] px-3 py-1 text-xs text-[#4ade80]">正常</span>}
          </div>
        </div>

        {/* 超限通知 */}
        {overLimit && (
          <div className="mt-3 rounded-lg bg-[#4a1d1d] px-3 py-2 text-sm text-[#fca5a5]">
            流量已超出配额，请联系管理员。
          </div>
        )}

        {/* 停用/到期通知 */}
        {info.disabled && (
          <div className="mt-3 rounded-lg bg-[#4a1d1d] px-3 py-2 text-sm text-[#fca5a5]">
            订阅已被管理员停用，节点不可用。
          </div>
        )}
        {info.expired && (
          <div className="mt-3 rounded-lg bg-[#4a1d1d] px-3 py-2 text-sm text-[#fca5a5]">
            订阅已到期，节点已停用。
          </div>
        )}

        {/* 统计 */}
        <div className="mt-4 grid grid-cols-3 gap-2.5">
          <div className="rounded-[10px] bg-[#131a2b] p-3">
            <span className="block text-xs text-[#9aa4c0]">已用流量</span>
            <span className="text-sm">↑ {humanizeBytes(info.used_up)} ↓ {humanizeBytes(info.used_down)}</span>
          </div>
          <div className="rounded-[10px] bg-[#131a2b] p-3">
            <span className="block text-xs text-[#9aa4c0]">有效期</span>
            <span className="text-sm">{info.expires_at ? new Date(info.expires_at * 1000).toLocaleDateString() : '长期'}</span>
          </div>
          <div className="rounded-[10px] bg-[#131a2b] p-3">
            <span className="block text-xs text-[#9aa4c0]">节点数量</span>
            <span className="text-sm">{info.nodes_count}</span>
          </div>
        </div>

        {/* 流量进度条 */}
        {info.traffic_limit > 0 && (
          <div className="mt-3">
            <div className="flex justify-between text-xs text-[#9aa4c0]">
              <span>{humanizeBytes(usedTotal)} / {humanizeBytes(info.traffic_limit)}</span>
              <span>{usagePercent.toFixed(1)}%</span>
            </div>
            <div className="mt-1 h-2 overflow-hidden rounded-full bg-[#131a2b]">
              <div
                className={`h-full rounded-full transition-all ${overLimit ? 'bg-[#f87171]' : 'bg-[#3b82f6]'}`}
                style={{ width: `${usagePercent}%` }}
              />
            </div>
          </div>
        )}

        {/* 订阅地址 */}
        <div className="mt-4">
          <span className="text-xs text-[#9aa4c0]">订阅地址（通用）</span>
          <div className="mt-1 flex items-center gap-2 rounded-[10px] bg-[#131a2b] px-3 py-2">
            <code className="flex-1 break-all text-xs text-[#cdd6ee]">{subURL}</code>
            <button
              onClick={() => handleCopy(subURL, 'url')}
              className="shrink-0 rounded-lg bg-[#3b82f6] px-3 py-1.5 text-xs text-white hover:bg-[#2563eb]"
            >
              {copied === 'url' ? '已复制' : '复制'}
            </button>
          </div>
        </div>

        {/* 客户端导入 */}
        <div className="mt-5">
          <span className="text-xs text-[#9aa4c0]">一键导入客户端</span>
          <div className="mt-2 space-y-3">
            {grouped.map(group => (
              <div key={group.platform}>
                <span className="text-[11px] font-medium uppercase tracking-wide text-[#6b7595]">{group.label}</span>
                <div className="mt-1 flex flex-wrap gap-2">
                  {group.clients.map(client => (
                    client.deeplink
                      ? (
                        <a
                          key={client.name}
                          href={client.deeplink}
                          className="rounded-lg bg-[#3b82f6] px-3 py-1.5 text-xs text-white no-underline hover:bg-[#2563eb]"
                        >
                          {client.name}
                        </a>
                      )
                      : (
                        <button
                          key={client.name}
                          onClick={() => handleCopy(`${subURL}?format=${client.format}`, client.name)}
                          className="rounded-lg bg-[#2a3350] px-3 py-1.5 text-xs text-[#cdd6ee] hover:bg-[#3a4560]"
                        >
                          {copied === client.name ? '已复制' : `${client.name}（复制链接）`}
                        </button>
                      )
                  ))}
                </div>
              </div>
            ))}
          </div>
        </div>

        {/* 公告 */}
        {info.announcement && (
          <div className="mt-5 rounded-[10px] bg-[#131a2b] p-4">
            <span className="text-xs text-[#9aa4c0]">公告</span>
            <div className="mt-2 whitespace-pre-wrap text-sm text-[#cdd6ee]">
              {info.announcement}
            </div>
          </div>
        )}
      </div>

      <p className="mt-4 text-xs text-[#6b7595]">
        Lattix · 订阅每 {info.update_interval} 小时自动更新
      </p>
    </div>
  )
}
