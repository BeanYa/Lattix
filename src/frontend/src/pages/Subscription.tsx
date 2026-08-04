import {
  useEffect,
  useMemo,
  useRef,
  useState,
  type CSSProperties,
} from 'react'
import {
  ArrowDownToLine,
  ArrowUpRight,
  CheckIcon,
  CircleAlertIcon,
  CopyIcon,
  DownloadIcon,
  GaugeIcon,
  RadioIcon,
  RefreshCwIcon,
  ShieldCheckIcon,
  WifiIcon,
  XIcon,
} from 'lucide-react'

import LattixMark from '@/components/LattixMark'
import MagicRings from '@/components/MagicRings'
import AnimatedContent from '@/components/react-bits/AnimatedContent'
import DotGrid from '@/components/react-bits/DotGrid'
import ElectricBorder from '@/components/react-bits/ElectricBorder'
import { RequestError, requester } from '@/lib/requester'

import './subscription.css'

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
  update_interval: string
}

interface ClientInfo {
  name: string
  platform: string
  deeplink: string
  format: string
  app_store_url?: string
  download_variants?: Array<{ id: string; label: string }>
}

interface DownloadTask {
  task_id: string
  status: string
  progress: number
  size: number
  filename?: string
  error?: string
}

interface LatencySample {
  latency_ms: number | null
  updated_at: string
}

interface LinkStatus {
  label: string
  hops: Array<{
    label: string
    samples: LatencySample[]
  }>
}

interface LinkStatusResponse {
  links: LinkStatus[]
}

const previewInfo: SubInfo = {
  name: 'bean',
  expires_at: Math.floor(Date.now() / 1000) + 86400 * 126,
  expired: false,
  disabled: false,
  used_up: 18_640_000_000,
  used_down: 46_920_000_000,
  traffic_limit: 0,
  nodes_count: 18,
  title: 'Lattix Private Network',
  update_interval: '6',
}

const previewClients: ClientInfo[] = [
  { name: 'Stash', platform: 'ios', deeplink: '#preview-stash', format: 'stash' },
  { name: 'Shadowrocket', platform: 'ios', deeplink: '#preview-shadowrocket', format: 'base64' },
  {
    name: 'Clash Meta',
    platform: 'android',
    deeplink: '#preview-clash-meta',
    format: 'mihomo',
    download_variants: [
      { id: 'clash-meta-android-arm64', label: 'ARM64' },
      { id: 'clash-meta-android-x64', label: 'x64' },
    ],
  },
  {
    name: 'v2rayNG',
    platform: 'android',
    deeplink: '',
    format: 'base64',
    download_variants: [
      { id: 'v2rayng-android-arm64', label: 'ARM64' },
      { id: 'v2rayng-android-x64', label: 'x64' },
    ],
  },
  {
    name: 'Mihomo Party',
    platform: 'windows',
    deeplink: '#preview-mihomo-party',
    format: 'mihomo',
    download_variants: [
      { id: 'mihomo-party-windows-x64', label: 'x64' },
      { id: 'mihomo-party-windows-arm64', label: 'ARM64' },
    ],
  },
  {
    name: 'Clash Verge',
    platform: 'macos',
    deeplink: '#preview-clash-verge',
    format: 'mihomo',
    download_variants: [
      { id: 'clash-verge-macos-arm64', label: 'ARM64' },
      { id: 'clash-verge-macos-x64', label: 'x64' },
    ],
  },
  { name: '通用订阅', platform: 'universal', deeplink: '', format: 'base64' },
]

const previewLinkStatus: LinkStatus[] = [
  {
    label: '🇯🇵东京-Out',
    hops: [
      { label: '入口', samples: [42, 38, 45, 39, 41, 44, 40, 43, 46, 48, 44, 39, 42, 41, 43, 46, 40, 38, 41, 44, 45, 43, 47, 42, 41, 39, 44, 40, 42, 45].map(previewLatencySample) },
      { label: '中转 1', samples: [82, 88, 91, 85, 92, 103, 97, 89, 84, 90, 94, 87, 98, 101, 93, 88, 95, 99, 86, 91, 88, 97, 102, 90, 89, 94, 98, 92, 87, 91].map(previewLatencySample) },
      { label: '出口', samples: [128, 142, 158, 135, 129, 144, 151, 137, 132, 148, 139, 130, 145, 152, 136, 142, 155, 149, 134, 141, 138, 147, 159, 143, 132, 146, 151, 139, 145, 148].map(previewLatencySample) },
    ],
  },
  {
    label: '🇸🇬新加坡-Direct',
    hops: [
      { label: '服务器', samples: [61, 58, 63, 59, 67, 64, 62, 70, 66, 61, 57, 64, 69, 60, 63, 65, 59, 68, 71, 62, 60, 66, 64, 58, 63, 67, 61, 59, 65, 62].map(previewLatencySample) },
    ],
  },
]

const platformLabels: Record<string, string> = {
  all: '全部',
  ios: 'iOS',
  android: 'Android',
  windows: 'Windows',
  macos: 'macOS',
  universal: '通用',
}

const platformOrder = ['ios', 'android', 'windows', 'macos', 'universal']

function humanizeBytes(n: number): string {
  if (n < 1024) return `${n} B`
  const units = ['KB', 'MB', 'GB', 'TB']
  let value = n / 1024
  let index = 0
  while (value >= 1024 && index < units.length - 1) {
    value /= 1024
    index++
  }
  return value >= 100 ? `${value.toFixed(0)} ${units[index]}` : `${value.toFixed(1)} ${units[index]}`
}

function formatExpiry(timestamp?: number): string {
  if (!timestamp) return '长期有效'
  return new Intl.DateTimeFormat('zh-CN', {
    year: 'numeric',
    month: 'short',
    day: 'numeric',
  }).format(new Date(timestamp * 1000))
}

function previewLatencySample(latency_ms: number): LatencySample {
  return { latency_ms, updated_at: new Date().toISOString() }
}

function latencyTone(latency: number | null): 'healthy' | 'warning' | 'critical' | 'timeout' {
  if (latency === null) return 'timeout'
  if (latency >= 300) return 'critical'
  if (latency >= 100) return 'warning'
  return 'healthy'
}

function formatSampleTime(value: string): string {
  if (!value) return '采样时间未知'
  const date = new Date(value)
  if (Number.isNaN(date.getTime())) return value
  return new Intl.DateTimeFormat('zh-CN', {
    month: 'numeric',
    day: 'numeric',
    hour: '2-digit',
    minute: '2-digit',
  }).format(date)
}

function LatencyHistory({ label, samples }: { label: string; samples: LatencySample[] }) {
  const [activeIndex, setActiveIndex] = useState<number | null>(null)
  const values = samples.slice(-30)
  const cells: Array<LatencySample | null> = [
    ...values,
    ...Array.from<null>({ length: Math.max(0, 30 - values.length) }).fill(null),
  ]

  return (
    <div className="link-hop">
      <span className="link-hop-label">{label}</span>
      <div
        className="latency-history"
        role="group"
        tabIndex={0}
        aria-label={`${label} 最近 30 次延迟状态，使用左右方向键查看采样数据`}
        onMouseLeave={(event) => {
          if (document.activeElement !== event.currentTarget) setActiveIndex(null)
        }}
        onFocus={() => setActiveIndex((current) => current ?? cells.length - 1)}
        onBlur={() => setActiveIndex(null)}
        onKeyDown={(event) => {
          if (event.key !== 'ArrowLeft' && event.key !== 'ArrowRight') return
          event.preventDefault()
          const direction = event.key === 'ArrowLeft' ? -1 : 1
          setActiveIndex((current) => Math.min(cells.length - 1, Math.max(0, (current ?? cells.length - 1) + direction)))
        }}
      >
        {cells.map((sample, index) => {
          const tone = sample ? latencyTone(sample.latency_ms) : 'missing'
          const labelText = sample === null ? '无数据' : sample.latency_ms === null ? '探测超时' : `${Math.round(sample.latency_ms)} ms`
          return (
            <span
              key={sample ? `${sample.updated_at}-${index}` : `empty-${index}`}
              className={`latency-cell is-${tone}`}
              onMouseEnter={() => setActiveIndex(index)}
            >
              {activeIndex === index ? (
                <span className="latency-tooltip" role="tooltip">
                  <strong>{labelText}</strong>
                  <small>{sample ? formatSampleTime(sample.updated_at) : '尚无采样记录'}</small>
                </span>
              ) : null}
            </span>
          )
        })}
      </div>
    </div>
  )
}

async function copyText(text: string): Promise<void> {
  try {
    await navigator.clipboard.writeText(text)
    return
  } catch {
    const input = document.createElement('textarea')
    input.value = text
    input.setAttribute('readonly', '')
    input.style.position = 'fixed'
    input.style.opacity = '0'
    document.body.append(input)
    input.select()
    const copied = document.execCommand('copy')
    input.remove()
    if (!copied) throw new Error('copy failed')
  }
}

function LoadingState() {
  return (
    <main className="subscription-page subscription-loading" aria-busy="true" aria-label="正在加载订阅">
      <div className="subscription-noise" />
      <div className="subscription-skeleton">
        <div className="skeleton-line skeleton-brand" />
        <div className="skeleton-grid">
          <div>
            <div className="skeleton-line skeleton-title" />
            <div className="skeleton-line skeleton-copy" />
            <div className="skeleton-line skeleton-copy short" />
          </div>
          <div className="skeleton-orbit" />
        </div>
        <div className="skeleton-line skeleton-panel" />
      </div>
    </main>
  )
}

function ErrorState({ message }: { message: string }) {
  return (
    <main className="subscription-page subscription-error-page">
      <div className="subscription-noise" />
      <section className="subscription-error" role="alert">
        <div className="error-icon"><CircleAlertIcon /></div>
        <span className="section-kicker">CONNECTION FAILED</span>
        <h1>订阅暂时无法打开</h1>
        <p>{message}</p>
        <button type="button" className="primary-action" onClick={() => window.location.reload()}>
          <RefreshCwIcon />
          重新连接
        </button>
      </section>
    </main>
  )
}

export default function SubscriptionPage() {
  const token = window.location.pathname.split('/sub/')[1]?.replace(/\/$/, '') || ''
  const isPreview = import.meta.env.DEV && token === 'preview'
  const [info, setInfo] = useState<SubInfo | null>(null)
  const [clients, setClients] = useState<ClientInfo[]>([])
  const [linkStatus, setLinkStatus] = useState<LinkStatus[] | null>(null)
  const [error, setError] = useState('')
  const [copied, setCopied] = useState('')
  const [activePlatform, setActivePlatform] = useState('all')
  const [downloadClient, setDownloadClient] = useState<ClientInfo | null>(null)
  const [downloadVariant, setDownloadVariant] = useState('')
  const [downloadTask, setDownloadTask] = useState<DownloadTask | null>(null)
  const [downloadError, setDownloadError] = useState('')
  const copyTimer = useRef<number | undefined>(undefined)
  const downloadTriggered = useRef('')

  useEffect(() => {
    if (!token) {
      setError('链接中缺少有效的订阅凭证。')
      return
    }
    if (isPreview) {
      setInfo(previewInfo)
      setClients(previewClients)
      setLinkStatus(previewLinkStatus)
      return
    }

    const controller = new AbortController()
    Promise.all([
      requester.getJSON<SubInfo>(`/api/sub/${token}/info`, { signal: controller.signal }),
      requester
        .getJSON<ClientInfo[]>(`/api/sub/${token}/clients`, {
          signal: controller.signal,
          display: 'silent',
        })
        .catch(() => []),
      requester
        .getJSON<LinkStatusResponse>(`/api/sub/${token}/status`, {
          signal: controller.signal,
          display: 'silent',
        })
        .catch(() => ({ links: [] })),
    ])
      .then(([nextInfo, nextClients, nextLinkStatus]) => {
        setInfo(nextInfo)
        setClients(nextClients)
        setLinkStatus(nextLinkStatus.links)
      })
      .catch((reason: unknown) => {
        if (reason instanceof RequestError && reason.code === 'REQUEST_CANCELLED') return
        setError(
          reason instanceof RequestError && reason.httpStatus === 404
            ? '这条订阅不存在，或链接已经失效。'
            : '无法读取订阅信息，请检查网络后重试。',
        )
      })
    return () => controller.abort()
  }, [isPreview, token])

  useEffect(() => () => window.clearTimeout(copyTimer.current), [])

  useEffect(() => {
    if (!downloadTask || !downloadTask.task_id || !['queued', 'downloading'].includes(downloadTask.status)) return
    let cancelled = false
    const timer = window.setTimeout(() => {
      requester
        .getJSON<DownloadTask>(`/api/sub/${token}/client-download/status?task=${encodeURIComponent(downloadTask.task_id)}`, { display: 'silent' })
        .then((nextTask) => {
          if (!cancelled) setDownloadTask(nextTask)
        })
        .catch(() => {
          if (!cancelled) setDownloadError('读取下载进度失败，请稍后重试。')
        })
    }, 800)
    return () => {
      cancelled = true
      window.clearTimeout(timer)
    }
  }, [downloadTask, token])

  useEffect(() => {
    if (!downloadTask || downloadTask.status !== 'done' || downloadTriggered.current === downloadTask.task_id) return
    downloadTriggered.current = downloadTask.task_id
    requester
      .download(`/api/sub/${token}/client-download/file?task=${encodeURIComponent(downloadTask.task_id)}`, {
        timeoutMs: 10 * 60 * 1000,
        display: 'silent',
      })
      .then(() => {
        setDownloadTask((current) => current?.task_id === downloadTask.task_id ? { ...current, status: 'downloaded' } : current)
      })
      .catch(() => {
        setDownloadError('浏览器下载客户端失败，请重新点击下载。')
        setDownloadTask((current) => current?.task_id === downloadTask.task_id ? { ...current, status: 'failed' } : current)
      })
  }, [downloadTask, token])

  const subURL = isPreview
    ? `${window.location.origin}/sub/9b5c-preview-token`
    : `${window.location.origin}/sub/${token}`

  const handleCopy = async (text: string, label: string) => {
    try {
      await copyText(text)
      setCopied(label)
      window.clearTimeout(copyTimer.current)
      copyTimer.current = window.setTimeout(() => setCopied(''), 1800)
    } catch {
      setCopied('')
    }
  }

  const openDownload = (client: ClientInfo) => {
    const firstVariant = client.download_variants?.[0]?.id ?? ''
    setDownloadClient(client)
    setDownloadVariant(firstVariant)
    setDownloadTask(null)
    setDownloadError('')
    downloadTriggered.current = ''
  }

  const closeDownload = () => {
    setDownloadClient(null)
    setDownloadTask(null)
    setDownloadError('')
    downloadTriggered.current = ''
  }

  const startDownload = async () => {
    if (!downloadClient || !downloadVariant) return
    setDownloadError('')
    setDownloadTask({ task_id: '', status: 'queued', progress: 0, size: 0 })
    try {
      const task = await requester.getJSON<DownloadTask>(
        `/api/sub/${token}/client-download/start?variant=${encodeURIComponent(downloadVariant)}`,
        { display: 'silent' },
      )
      setDownloadTask(task)
    } catch {
      setDownloadTask(null)
      setDownloadError('无法开始下载客户端，请稍后重试。')
    }
  }

  const availablePlatforms = useMemo(
    () => platformOrder.filter((platform) => clients.some((client) => client.platform === platform)),
    [clients],
  )
  const visibleClients = activePlatform === 'all'
    ? clients
    : clients.filter((client) => client.platform === activePlatform)

  if (error) return <ErrorState message={error} />
  if (!info) return <LoadingState />

  const usedTotal = info.used_up + info.used_down
  const remaining = Math.max(0, info.traffic_limit - usedTotal)
  const usagePercent = info.traffic_limit > 0
    ? Math.min(100, (usedTotal / info.traffic_limit) * 100)
    : 0
  const overLimit = info.traffic_limit > 0 && usedTotal > info.traffic_limit
  const unavailable = info.disabled || info.expired || overLimit
  const statusLabel = info.disabled ? '已停用' : info.expired ? '已到期' : overLimit ? '已超额' : '网络就绪'
  const statusDetail = unavailable ? '节点当前不可用' : '订阅与节点均可用'
  const primaryClient = clients.find((client) => client.deeplink)
  const ringStyle = { '--usage-angle': `${usagePercent * 3.6}deg` } as CSSProperties

  return (
    <main className="subscription-page" data-status={unavailable ? 'warning' : 'ready'}>
      <div className="subscription-noise" />
      <div className="react-bits-background" aria-hidden="true">
        <DotGrid
          dotSize={2}
          gap={24}
          baseColor="#263129"
          activeColor="#bdf33b"
          proximity={130}
          shockRadius={220}
          shockStrength={3.5}
          returnDuration={1.2}
        />
      </div>

      <div className="subscription-frame">
        <header className="subscription-nav">
          <a className="brand-lockup" href={subURL} aria-label="Lattix 订阅主页">
            <LattixMark className="brand-mark" />
            <span>LATTIX</span>
          </a>
          <div className="nav-status" aria-label={`订阅状态：${statusLabel}`}>
            <span className="status-pulse" />
            {statusLabel}
          </div>
        </header>

        <section className="subscription-stage">
          <div className="stage-copy">
            <h1>{info.title}</h1>
            <div className="stage-meta" aria-label="订阅基本信息">
              <span>{info.name}</span>
              <span>{info.nodes_count} 个节点</span>
              <span>{formatExpiry(info.expires_at)}</span>
            </div>
            <div className="stage-actions">
              {primaryClient ? (
                <a className="primary-action magnetic-action" href={primaryClient.deeplink}>
                  <ArrowDownToLine />
                  导入到 {primaryClient.name}
                  <ArrowUpRight className="action-tail" />
                </a>
              ) : null}
              <button
                type="button"
                className="secondary-action"
                onClick={() => handleCopy(subURL, 'hero-url')}
              >
                {copied === 'hero-url' ? <CheckIcon /> : <CopyIcon />}
                {copied === 'hero-url' ? '链接已复制' : '复制订阅地址'}
              </button>
            </div>
          </div>

          {info.traffic_limit > 0 ? (
            <ElectricBorder
              className="traffic-electric"
              color={unavailable ? '#ff7059' : '#bdf33b'}
              speed={0.8}
              chaos={0.08}
              borderRadius={8}
            >
              <div className="traffic-orbit" style={ringStyle} aria-label={`流量已使用 ${usagePercent.toFixed(1)}%`}>
                <div className="orbit-track">
                  <div className="orbit-core">
                    <span>剩余流量</span>
                    <strong>{humanizeBytes(remaining)}</strong>
                    <small>{usagePercent.toFixed(1)}% 已使用</small>
                  </div>
                </div>
                <span className="orbit-node node-a" />
                <span className="orbit-node node-b" />
                <span className="orbit-node node-c" />
              </div>
            </ElectricBorder>
          ) : (
            <div className="traffic-orbit traffic-orbit-unlimited" aria-label="无限流量，无配额限制">
              <div className="unlimited-rings" aria-hidden="true">
                <MagicRings
                  color="#bdf33b"
                  colorTwo="#f0f4ee"
                  speed={0.72}
                  ringCount={7}
                  attenuation={9}
                  lineThickness={1.8}
                  baseRadius={0.18}
                  radiusStep={0.075}
                  scaleRate={0.46}
                  opacity={0.72}
                  noiseAmount={0.025}
                  ringGap={1.35}
                  fadeIn={0.58}
                  fadeOut={2.5}
                  followMouse
                  mouseInfluence={0.08}
                  hoverScale={1.06}
                  parallax={0.018}
                  clickBurst
                />
              </div>
              <div className="unlimited-core">
                <span className="unlimited-symbol" aria-hidden="true">∞</span>
                <strong>无限流量</strong>
                <small>本期已用 {humanizeBytes(usedTotal)}</small>
              </div>
            </div>
          )}
        </section>

        {unavailable ? (
          <div className="status-alert" role="alert">
            <CircleAlertIcon />
            <div>
              <strong>{statusLabel}</strong>
              <span>{info.disabled ? '订阅已被管理员停用。' : info.expired ? '订阅有效期已结束。' : '流量已超出当前配额。'}</span>
            </div>
          </div>
        ) : null}

        <AnimatedContent distance={28} duration={0.65} threshold={0.08}>
          <section className="signal-strip" aria-label="订阅摘要">
            <div className="signal-item">
              <GaugeIcon />
              <span>本期流量</span>
              <strong>{humanizeBytes(usedTotal)}</strong>
              <small>上行 {humanizeBytes(info.used_up)} · 下行 {humanizeBytes(info.used_down)}</small>
            </div>
            <div className="signal-item">
              <ShieldCheckIcon />
              <span>有效期</span>
              <strong>{formatExpiry(info.expires_at)}</strong>
              <small>{info.expired ? '续期后恢复服务' : '凭证状态正常'}</small>
            </div>
            <div className="signal-item">
              <RadioIcon />
              <span>节点</span>
              <strong>{info.nodes_count} 个</strong>
              <small>每 {info.update_interval} 小时自动同步</small>
            </div>
          </section>
        </AnimatedContent>

        <AnimatedContent distance={36} duration={0.7} threshold={0.12}>
          <section className="link-monitor" aria-label="链路状态">
            <div className="section-heading">
              <h2>链路状态</h2>
              <span>最近 30 次延迟</span>
            </div>
            {linkStatus && linkStatus.length > 0 ? (
              <div className="link-monitor-list">
                {linkStatus.map((link) => (
                  <article key={link.label} className="link-monitor-row">
                    <span className="link-monitor-name">{link.label}</span>
                    <div className="link-hop-list">
                      {link.hops.map((hop) => (
                        <LatencyHistory key={hop.label} label={hop.label} samples={hop.samples} />
                      ))}
                    </div>
                  </article>
                ))}
              </div>
            ) : linkStatus ? (
              <div className="link-monitor-empty">暂无已采集的链路状态</div>
            ) : null}
          </section>
        </AnimatedContent>

        <AnimatedContent distance={36} duration={0.7} threshold={0.12}>
          <section className="client-section">
          <div className="section-heading">
            <h2>客户端</h2>
          </div>

          {clients.length > 0 ? (
            <>
              <div className="platform-tabs" role="tablist" aria-label="客户端平台">
                {['all', ...availablePlatforms].map((platform) => (
                  <button
                    key={platform}
                    type="button"
                    role="tab"
                    aria-selected={activePlatform === platform}
                    onClick={() => setActivePlatform(platform)}
                  >
                    {platformLabels[platform] ?? platform}
                  </button>
                ))}
              </div>

              <div className="client-grid">
                {visibleClients.map((client, index) => {
                  const label = `${client.platform}-${client.name}`
                  const isAppStore = Boolean(client.app_store_url)
                  const isDownloadable = !isAppStore && Boolean(client.download_variants?.length)
                  const content = (
                    <>
                      <span className="client-index">{String(index + 1).padStart(2, '0')}</span>
                      <span className="client-name">
                        <strong>{client.name}</strong>
                        <small>{platformLabels[client.platform] ?? client.platform} · {client.format}</small>
                      </span>
                      <span className="client-action-icon">
                        {copied === label ? <CheckIcon /> : isAppStore ? <ArrowUpRight /> : client.deeplink ? <ArrowUpRight /> : isDownloadable ? <DownloadIcon /> : <CopyIcon />}
                      </span>
                    </>
                  )
                  if (isAppStore) {
                    return (
                      <a
                        key={label}
                        className="client-row"
                        href={client.app_store_url}
                        target="_blank"
                        rel="noreferrer"
                        aria-label={`在 App Store 打开 ${client.name}`}
                      >
                        {content}
                      </a>
                    )
                  }
                  if (isDownloadable) {
                    if (client.deeplink) {
                      return (
                        <div key={label} className="client-row client-row-dual">
                          <a
                            className="client-row-main"
                            href={client.deeplink}
                            aria-label={`导入到 ${client.name}`}
                          >
                            {content}
                          </a>
                          <button
                            type="button"
                            className="client-row-download"
                            onClick={() => openDownload(client)}
                            aria-label={`下载 ${client.name}`}
                          >
                            <DownloadIcon />
                          </button>
                        </div>
                      )
                    }
                    return (
                      <button
                        key={label}
                        type="button"
                        className="client-row"
                        onClick={() => openDownload(client)}
                        aria-label={`下载 ${client.name}`}
                      >
                        {content}
                      </button>
                    )
                  }
                  return client.deeplink ? (
                    <a key={label} className="client-row" href={client.deeplink}>{content}</a>
                  ) : (
                    <button
                      key={label}
                      type="button"
                      className="client-row"
                      onClick={() => handleCopy(`${subURL}?format=${client.format}`, label)}
                    >
                      {content}
                    </button>
                  )
                })}
              </div>
            </>
          ) : (
            <div className="client-empty">
              <WifiIcon />
              <strong>暂无一键导入客户端</strong>
              <span>仍可复制通用订阅地址，在客户端中手动添加。</span>
            </div>
          )}
          </section>
        </AnimatedContent>

        <AnimatedContent distance={28} duration={0.65} threshold={0.16}>
          <section className="subscription-endpoint">
          <div className="endpoint-copy"><h2>订阅地址</h2></div>
          <div className="endpoint-value">
            <code>{subURL}</code>
            <button
              type="button"
              aria-label="复制通用订阅地址"
              title="复制通用订阅地址"
              onClick={() => handleCopy(subURL, 'endpoint-url')}
            >
              {copied === 'endpoint-url' ? <CheckIcon /> : <CopyIcon />}
            </button>
          </div>
          </section>
        </AnimatedContent>

        <footer className="subscription-footer">
          <span>Lattix</span>
          <span>每 {info.update_interval} 小时自动更新 · {statusDetail}</span>
        </footer>

        {downloadClient ? (
          <div
            className="download-modal-backdrop"
            role="presentation"
            onMouseDown={() => {
              if (!downloadTask || !['queued', 'downloading'].includes(downloadTask.status)) closeDownload()
            }}
          >
            <section
              className="download-modal"
              role="dialog"
              aria-modal="true"
              aria-labelledby="download-modal-title"
              onMouseDown={(event) => event.stopPropagation()}
            >
              <div className="download-modal-header">
                <div>
                  <span className="section-kicker">CLIENT PACKAGE</span>
                  <h2 id="download-modal-title">下载 {downloadClient.name}</h2>
                </div>
                <button
                  type="button"
                  className="download-modal-close"
                  aria-label="关闭下载窗口"
                  title="关闭"
                  disabled={Boolean(downloadTask && ['queued', 'downloading'].includes(downloadTask.status))}
                  onClick={closeDownload}
                >
                  <XIcon />
                </button>
              </div>

              <label className="download-variant-label" htmlFor="download-variant">安装包架构</label>
              <select
                id="download-variant"
                className="download-variant-select"
                value={downloadVariant}
                disabled={Boolean(downloadTask && ['queued', 'downloading'].includes(downloadTask.status))}
                onChange={(event) => setDownloadVariant(event.target.value)}
              >
                {downloadClient.download_variants?.map((variant) => (
                  <option key={variant.id} value={variant.id}>{variant.label}</option>
                ))}
              </select>

              {downloadTask ? (
                <div className="download-progress" aria-live="polite">
                  <div className="download-progress-label">
                    <span>
                      {downloadTask.status === 'downloaded'
                        ? '已开始浏览器下载'
                        : downloadTask.status === 'done'
                          ? '准备浏览器下载'
                          : downloadTask.status === 'failed'
                            ? '下载失败'
                            : downloadTask.status === 'queued'
                              ? '等待下载'
                              : '正在下载'}
                    </span>
                    <strong>{Math.round(Math.max(0, Math.min(1, downloadTask.progress)) * 100)}%</strong>
                  </div>
                  <div className="download-progress-track">
                    <span style={{ width: `${Math.round(Math.max(0, Math.min(1, downloadTask.progress)) * 100)}%` }} />
                  </div>
                  {downloadTask.filename ? <small>{downloadTask.filename}</small> : null}
                </div>
              ) : null}

              {downloadError || downloadTask?.error ? <p className="download-modal-error" role="alert">{downloadError || downloadTask?.error}</p> : null}

              <div className="download-modal-actions">
                <button
                  type="button"
                  className="primary-action"
                  disabled={!downloadVariant || Boolean(downloadTask && ['queued', 'downloading', 'done', 'downloaded'].includes(downloadTask.status))}
                  onClick={() => void startDownload()}
                >
                  {downloadTask && ['queued', 'downloading'].includes(downloadTask.status) ? <RefreshCwIcon className="download-spin" /> : <DownloadIcon />}
                  {downloadTask && ['queued', 'downloading'].includes(downloadTask.status) ? '下载中' : '开始下载'}
                </button>
              </div>
            </section>
          </div>
        ) : null}
      </div>
    </main>
  )
}
