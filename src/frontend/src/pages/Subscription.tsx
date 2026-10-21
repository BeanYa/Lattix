import { useEffect, useMemo, useRef, useState } from 'react'
import {
  ArrowDownToLine,
  ArrowUpRight,
  CheckIcon,
  CircleAlertIcon,
  CopyIcon,
  DownloadIcon,
  QrCodeIcon,
  RefreshCwIcon,
  WifiIcon,
  XIcon,
} from 'lucide-react'

import LattixMark from '@/components/LattixMark'
import { Notice } from '@/components/PagePrimitives'
import { QRDialog } from '@/components/QRDialog'
import ThemeToggle from '@/components/ThemeToggle'
import { humanizeBytes } from '@/lib/format'
import { RequestError, requester } from '@/lib/requester'

import './subscription.css'

interface SubInfo {
  name: string
  expires_at?: number
  expired: boolean
  disabled: boolean
  quota_exhausted?: boolean
  effective_at?: number
  not_effective?: boolean
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
  source_url?: string
  sha256?: string
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
      {
        label: '入口',
        samples: [
          42, 38, 45, 39, 41, 44, 40, 43, 46, 48, 44, 39, 42, 41, 43, 46, 40, 38, 41, 44, 45, 43,
          47, 42, 41, 39, 44, 40, 42, 45,
        ].map(previewLatencySample),
      },
      {
        label: '中转 1',
        samples: [
          82, 88, 91, 85, 92, 103, 97, 89, 84, 90, 94, 87, 98, 101, 93, 88, 95, 99, 86, 91, 88, 97,
          102, 90, 89, 94, 98, 92, 87, 91,
        ].map(previewLatencySample),
      },
      {
        label: '出口',
        samples: [
          128, 142, 158, 135, 129, 144, 151, 137, 132, 148, 139, 130, 145, 152, 136, 142, 155, 149,
          134, 141, 138, 147, 159, 143, 132, 146, 151, 139, 145, 148,
        ].map(previewLatencySample),
      },
    ],
  },
  {
    label: '🇸🇬新加坡-Direct',
    hops: [
      {
        label: '服务器',
        samples: [
          61, 58, 63, 59, 67, 64, 62, 70, 66, 61, 57, 64, 69, 60, 63, 65, 59, 68, 71, 62, 60, 66,
          64, 58, 63, 67, 61, 59, 65, 62,
        ].map(previewLatencySample),
      },
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

const formatOrder = ['clash', 'singbox', 'links', 'quanx']

const formatLabels: Record<string, string> = {
  clash: 'Clash / Mihomo',
  singbox: 'Sing-box',
  links: 'V2Ray / Links',
  quanx: 'Quantumult X',
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

/** 客户端矩阵按断点换列，最后一行的单元格去掉底边，让模块外框充当收口线。 */
function lastRowClass(index: number, total: number): string {
  const lastRowOfThree = total - (total % 3 || 3)
  const lastRowOfTwo = total - (total % 2 || 2)
  return [
    index >= lastRowOfThree ? 'is-last-3' : '',
    index >= lastRowOfTwo ? 'is-last-2' : '',
    index === total - 1 ? 'is-last-1' : '',
  ]
    .filter(Boolean)
    .join(' ')
}

function LatencyHistory({ label, samples }: { label: string; samples: LatencySample[] }) {
  const [activeIndex, setActiveIndex] = useState<number | null>(null)
  const values = samples.slice(-30)
  const cells: Array<LatencySample | null> = [
    ...values,
    ...Array.from<null>({ length: Math.max(0, 30 - values.length) }).fill(null),
  ]

  return (
    <div className="sub-hop">
      <span className="sub-hop-label">{label}</span>
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
          setActiveIndex((current) =>
            Math.min(cells.length - 1, Math.max(0, (current ?? cells.length - 1) + direction)),
          )
        }}
      >
        {cells.map((sample, index) => {
          const tone = sample ? latencyTone(sample.latency_ms) : 'missing'
          const labelText =
            sample === null
              ? '无数据'
              : sample.latency_ms === null
                ? '探测超时'
                : `${Math.round(sample.latency_ms)} ms`
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

function BrandLockup() {
  return (
    <span className="sub-brand">
      <span className="sub-mark">
        <LattixMark className="size-4 shrink-0" />
      </span>
      <span className="sub-brand-name">LATTIX</span>
      <span className="sub-brand-sub">订阅 / SUBSCRIPTION</span>
    </span>
  )
}

function LoadingState() {
  return (
    <main className="sub-page" aria-busy="true" aria-label="正在加载订阅">
      <header className="sub-bar">
        <BrandLockup />
        <span className="sub-bar-spacer" />
        <span className="sub-bar-meta">正在读取订阅凭证</span>
      </header>
      <div className="sub-frame sub-frame-state">
        <section className="sub-mod" aria-label="正在加载订阅">
          <header className="sub-mod-head">
            <span className="sub-tab">LOADING</span>
            <h1 className="sub-mod-title">正在加载订阅</h1>
          </header>
          <div className="sub-mod-body">
            <div className="loading-state sub-loading">正在读取订阅信息…</div>
          </div>
        </section>
      </div>
    </main>
  )
}

function ErrorState({ message }: { message: string }) {
  return (
    <main className="sub-page">
      <header className="sub-bar">
        <BrandLockup />
        <span className="sub-bar-spacer" />
        <span className="sub-bar-meta">公开订阅页</span>
      </header>
      <div className="sub-frame sub-frame-state">
        <section className="sub-mod sub-error" role="alert">
          <header className="sub-mod-head">
            <span className="sub-tab">ERROR</span>
            <span className="sub-mod-title">订阅访问失败</span>
          </header>
          <div className="sub-mod-body sub-error-body">
            <div className="sub-error-icon">
              <CircleAlertIcon size={16} aria-hidden="true" />
            </div>
            <h1 className="sub-error-title">订阅暂时无法打开</h1>
            <p className="sub-error-message">{message}</p>
            <button
              type="button"
              className="cg-button is-primary"
              onClick={() => window.location.reload()}
            >
              <RefreshCwIcon aria-hidden="true" />
              重新连接
            </button>
          </div>
        </section>
      </div>
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
  const [activeFormat, setActiveFormat] = useState('')
  const [qrOpen, setQrOpen] = useState(false)
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
    if (
      !downloadTask ||
      !downloadTask.task_id ||
      !['queued', 'downloading'].includes(downloadTask.status)
    )
      return
    let cancelled = false
    const timer = window.setTimeout(() => {
      requester
        .getJSON<DownloadTask>(
          `/api/sub/${token}/client-download/status?task=${encodeURIComponent(downloadTask.task_id)}`,
          { display: 'silent' },
        )
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
    if (
      !downloadTask ||
      downloadTask.status !== 'done' ||
      downloadTriggered.current === downloadTask.task_id
    )
      return
    downloadTriggered.current = downloadTask.task_id
    const taskParam = encodeURIComponent(downloadTask.task_id)
    // 换取短期下载票据，再用普通 HTTP 链接交给浏览器原生下载（支持暂停/断点续传）。
    requester
      .getJSON<{ ticket: string }>(`/api/sub/${token}/client-download/ticket?task=${taskParam}`, {
        display: 'silent',
      })
      .then(({ ticket }) => {
        window.location.assign(
          `/api/sub/${token}/client-download/file?task=${taskParam}&ticket=${encodeURIComponent(ticket)}`,
        )
        setDownloadTask((current) =>
          current?.task_id === downloadTask.task_id
            ? { ...current, status: 'downloaded' }
            : current,
        )
      })
      .catch(() => {
        setDownloadError('浏览器下载客户端失败，请重新点击下载。')
        setDownloadTask((current) =>
          current?.task_id === downloadTask.task_id ? { ...current, status: 'failed' } : current,
        )
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
    () =>
      platformOrder.filter((platform) => clients.some((client) => client.platform === platform)),
    [clients],
  )

  const availableFormats = useMemo(() => {
    const seen = new Set(clients.map((client) => client.format))
    const known = formatOrder.filter((format) => seen.has(format))
    const extra = [...seen].filter((format) => !formatOrder.includes(format))
    return [...known, ...extra]
  }, [clients])

  const visibleClients =
    activePlatform === 'all'
      ? clients
      : clients.filter((client) => client.platform === activePlatform)

  if (error) return <ErrorState message={error} />
  if (!info) return <LoadingState />

  const usedTotal = info.used_up + info.used_down
  const remaining = Math.max(0, info.traffic_limit - usedTotal)
  const usagePercent =
    info.traffic_limit > 0 ? Math.min(100, (usedTotal / info.traffic_limit) * 100) : 0
  const overLimit = (info.traffic_limit > 0 && usedTotal > info.traffic_limit) || info.quota_exhausted
  const unlimited = info.traffic_limit <= 0
  const unavailable = info.disabled || info.expired || info.not_effective || overLimit
  const statusLabel = info.disabled
    ? '已停用'
    : info.not_effective
      ? '尚未生效'
      : info.expired
        ? '已到期'
        : overLimit
          ? '已超额'
          : '网络就绪'
  const statusDetail = unavailable ? '节点当前不可用' : '订阅与节点均可用'
  const credentialDetail = info.disabled
    ? '已被管理员停用'
    : info.not_effective
      ? '等待到达生效时刻'
      : info.expired
        ? '续期后恢复服务'
        : overLimit
          ? '流量配额已用尽，重置后恢复'
          : '凭证状态正常'
  const primaryClient = clients.find((client) => client.deeplink)
  const currentFormat = activeFormat && availableFormats.includes(activeFormat) ? activeFormat : ''
  const currentFormatLabel = currentFormat
    ? (formatLabels[currentFormat] ?? currentFormat.toUpperCase())
    : '通用'
  const endpointURL = currentFormat ? `${subURL}?format=${currentFormat}` : subURL
  const endpointCopied = copied === `endpoint-${currentFormat}`

  return (
    <main className="sub-page" data-status={unavailable ? 'warning' : 'ready'}>
      <header className="sub-bar">
        <a className="sub-brand" href={subURL} aria-label="Lattix 订阅主页">
          <span className="sub-mark">
            <LattixMark className="size-4 shrink-0" />
          </span>
          <span className="sub-brand-name">LATTIX</span>
          <span className="sub-brand-sub">订阅 / SUBSCRIPTION</span>
        </a>
        <span className="sub-bar-meta">自托管代理编排 / SELF-HOSTED</span>
        <span className="sub-bar-spacer" />
        <ThemeToggle />
        <div
          className="sub-status"
          data-tone={unavailable ? 'bad' : 'ok'}
          aria-label={`订阅状态：${statusLabel}`}
        >
          {statusLabel}
        </div>
      </header>

      <div className="sub-frame">
        <header className="page-header sub-head">
          <div className="page-header-copy sub-head-copy">
            <h1 className="page-title">{info.title}</h1>
            <div className="page-description sub-meta" role="group" aria-label="订阅基本信息">
              <span>{info.name}</span>
              <span className="sub-meta-sep" aria-hidden="true">
                /
              </span>
              <span>{info.nodes_count} 个节点</span>
              <span className="sub-meta-sep" aria-hidden="true">
                /
              </span>
              <span>到期 {formatExpiry(info.expires_at)}</span>
              <span className="sub-meta-sep" aria-hidden="true">
                /
              </span>
              <span>每 {info.update_interval} 小时自动更新</span>
            </div>
          </div>
          <div className="page-actions sub-actions">
            {primaryClient ? (
              <a className="cg-button is-primary" href={primaryClient.deeplink}>
                <ArrowDownToLine aria-hidden="true" />
                导入到 {primaryClient.name}
                <ArrowUpRight aria-hidden="true" />
              </a>
            ) : null}
            <button
              type="button"
              className="cg-button"
              onClick={() => handleCopy(subURL, 'hero-url')}
            >
              {copied === 'hero-url' ? (
                <CheckIcon aria-hidden="true" />
              ) : (
                <CopyIcon aria-hidden="true" />
              )}
              {copied === 'hero-url' ? '链接已复制' : '复制订阅地址'}
            </button>
            <button
              type="button"
              className="cg-button"
              aria-label="显示订阅二维码"
              title="显示订阅二维码"
              onClick={() => setQrOpen(true)}
            >
              <QrCodeIcon aria-hidden="true" />
              二维码
            </button>
          </div>
        </header>

        {unavailable ? (
          <Notice tone="danger" title={statusLabel} className="sub-alert">
            {info.disabled
              ? '订阅已被管理员停用。'
              : info.not_effective
                ? `订阅尚未生效${info.effective_at ? `，${formatExpiry(info.effective_at)} 起可用` : ''}。`
                : info.expired
                  ? '订阅有效期已结束。'
                  : '流量已超出当前配额。'}
          </Notice>
        ) : null}

        <section className="sub-mosaic" aria-label="订阅摘要">
          <article className="sub-mod sub-span-5">
            <header className="sub-mod-head">
              <span className="sub-tab">QUOTA</span>
              <h2 className="sub-mod-title">本期流量</h2>
              <span className="sub-mod-meta">
                {unlimited ? '无配额限制' : `${usagePercent.toFixed(1)}% 已使用`}
              </span>
            </header>
            <div
              className="sub-mod-body sub-quota"
              aria-label={unlimited ? '无限流量，无配额限制' : undefined}
            >
              <div className="sub-quota-main">
                <span className="sub-quota-label">剩余流量 / REMAINING</span>
                <strong className="sub-quota-value">
                  {unlimited ? '无限流量' : humanizeBytes(remaining)}
                </strong>
                <span className="sub-quota-detail">
                  {unlimited
                    ? `本期已用 ${humanizeBytes(usedTotal)} · 无配额限制`
                    : `本期已用 ${humanizeBytes(usedTotal)} · 上限 ${humanizeBytes(info.traffic_limit)}`}
                </span>
              </div>
              {unlimited ? null : (
                <div
                  className="sub-meter"
                  role="img"
                  aria-label={`流量已使用 ${usagePercent.toFixed(1)}%`}
                >
                  <span
                    className="sub-meter-fill"
                    data-tone={overLimit ? 'bad' : 'ok'}
                    style={{ width: `${usagePercent}%` }}
                  />
                </div>
              )}
              <dl className="sub-figures">
                <div>
                  <dt>上行 / UP</dt>
                  <dd className="tabular">{humanizeBytes(info.used_up)}</dd>
                </div>
                <div>
                  <dt>下行 / DOWN</dt>
                  <dd className="tabular">{humanizeBytes(info.used_down)}</dd>
                </div>
                <div>
                  <dt>{unlimited ? '本期已用 / USED' : '已用占比 / RATIO'}</dt>
                  <dd className="tabular">
                    {unlimited ? humanizeBytes(usedTotal) : `${usagePercent.toFixed(1)}%`}
                  </dd>
                </div>
              </dl>
            </div>
          </article>

          <article className="sub-mod sub-span-4">
            <header className="sub-mod-head">
              <span className="sub-tab">SUBSCRIPTION</span>
              <h2 className="sub-mod-title">订阅信息</h2>
              <span className="sub-mod-meta">LATTIX / {info.update_interval}H</span>
            </header>
            <div className="sub-mod-body sub-mod-body-flush">
              <dl className="sub-ledger">
                <div>
                  <dt>账户</dt>
                  <dd>{info.name}</dd>
                </div>
                <div>
                  <dt>有效期至</dt>
                  <dd className="tabular">{formatExpiry(info.expires_at)}</dd>
                </div>
                {info.effective_at ? (
                  <div>
                    <dt>生效时刻</dt>
                    <dd className="tabular">{formatExpiry(info.effective_at)}</dd>
                  </div>
                ) : null}
                <div>
                  <dt>凭证状态</dt>
                  <dd className={unavailable ? 'is-bad' : 'is-ok'}>{credentialDetail}</dd>
                </div>
                <div>
                  <dt>节点数量</dt>
                  <dd className="tabular">{info.nodes_count} 个</dd>
                </div>
                <div>
                  <dt>自动更新</dt>
                  <dd>每 {info.update_interval} 小时</dd>
                </div>
              </dl>
            </div>
          </article>

          <article className="sub-mod sub-span-3">
            <header className="sub-mod-head">
              <span className="sub-tab">STATUS</span>
              <h2 className="sub-mod-title">状态</h2>
              <span className="sub-mod-meta">LIVE</span>
            </header>
            <div className="sub-mod-body sub-state">
              <strong className="sub-state-value" data-tone={unavailable ? 'bad' : 'ok'}>
                {statusLabel}
              </strong>
              <span className="sub-state-detail">{statusDetail}</span>
              <dl className="sub-facts">
                <div>
                  <dt>客户端</dt>
                  <dd className="tabular">{clients.length}</dd>
                </div>
                <div>
                  <dt>格式</dt>
                  <dd className="tabular">{availableFormats.length}</dd>
                </div>
                <div>
                  <dt>链路</dt>
                  <dd className="tabular">{linkStatus?.length ?? 0}</dd>
                </div>
              </dl>
            </div>
          </article>
        </section>

        <section className="sub-mod sub-clients" aria-label="客户端">
          <header className="sub-mod-head">
            <span className="sub-tab">CLIENTS</span>
            <h2 className="sub-mod-title">客户端</h2>
            <span className="sub-mod-meta">
              共 {clients.length} 个 · {availableFormats.length} 种格式
            </span>
          </header>

          {clients.length > 0 ? (
            <div className="sub-clients-body">
              <div className="sub-tabs" role="tablist" aria-label="客户端平台">
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
              <p className="sub-guide">
                选择客户端一键导入；无法直接导入的客户端会复制对应格式的订阅地址。
              </p>

              <div className="sub-client-grid">
                {visibleClients.map((client, index) => {
                  const label = `${client.platform}-${client.name}`
                  const isAppStore = Boolean(client.app_store_url)
                  const isDownloadable = !isAppStore && Boolean(client.download_variants?.length)
                  const cellClass =
                    `sub-client-cell ${lastRowClass(index, visibleClients.length)}`.trim()
                  const content = (
                    <>
                      <span className="sub-client-copy">
                        <strong>{client.name}</strong>
                        <small>
                          {platformLabels[client.platform] ?? client.platform} · {client.format}
                        </small>
                      </span>
                      <span
                        className={`sub-client-icon${copied === label ? ' is-copied' : ''}`}
                        aria-hidden="true"
                      >
                        {copied === label ? (
                          <CheckIcon size={13} />
                        ) : isAppStore ? (
                          <ArrowUpRight size={13} />
                        ) : client.deeplink ? (
                          <ArrowUpRight size={13} />
                        ) : isDownloadable ? (
                          <DownloadIcon size={13} />
                        ) : (
                          <CopyIcon size={13} />
                        )}
                      </span>
                    </>
                  )
                  if (isAppStore) {
                    return (
                      <a
                        key={label}
                        className={cellClass}
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
                        <div key={label} className={`${cellClass} is-dual`}>
                          <a
                            className="sub-client-main"
                            href={client.deeplink}
                            aria-label={`导入到 ${client.name}`}
                          >
                            {content}
                          </a>
                          <button
                            type="button"
                            className="sub-client-download"
                            onClick={() => openDownload(client)}
                            aria-label={`下载 ${client.name}`}
                          >
                            <DownloadIcon size={13} aria-hidden="true" />
                          </button>
                        </div>
                      )
                    }
                    return (
                      <button
                        key={label}
                        type="button"
                        className={cellClass}
                        onClick={() => openDownload(client)}
                        aria-label={`下载 ${client.name}`}
                      >
                        {content}
                      </button>
                    )
                  }
                  return client.deeplink ? (
                    <a key={label} className={cellClass} href={client.deeplink}>
                      {content}
                    </a>
                  ) : (
                    <button
                      key={label}
                      type="button"
                      className={cellClass}
                      onClick={() => handleCopy(`${subURL}?format=${client.format}`, label)}
                    >
                      {content}
                    </button>
                  )
                })}
              </div>
            </div>
          ) : (
            <div className="sub-empty">
              <span className="sub-empty-icon">
                <WifiIcon size={15} aria-hidden="true" />
              </span>
              <strong className="sub-empty-title">暂无一键导入客户端</strong>
              <span className="sub-empty-desc">仍可复制通用订阅地址，在客户端中手动添加。</span>
            </div>
          )}
        </section>

        <section className="sub-mod sub-endpoint" aria-label="订阅地址">
          <header className="sub-mod-head">
            <span className="sub-tab">ENDPOINT</span>
            <h2 className="sub-mod-title">订阅地址</h2>
            <span className="sub-mod-meta">选择格式后复制对应地址</span>
          </header>
          <div className="sub-mod-body sub-endpoint-body">
            <div className="sub-formats" role="group" aria-label="订阅格式">
              <button
                type="button"
                aria-pressed={currentFormat === ''}
                onClick={() => setActiveFormat('')}
              >
                通用
              </button>
              {availableFormats.map((format) => (
                <button
                  key={format}
                  type="button"
                  aria-pressed={currentFormat === format}
                  onClick={() => setActiveFormat(format)}
                >
                  {formatLabels[format] ?? format.toUpperCase()}
                </button>
              ))}
            </div>
            <div className="sub-endpoint-row">
              <code className="sub-endpoint-code">{endpointURL}</code>
              <button
                type="button"
                className="cg-button is-primary is-icon"
                aria-label={
                  currentFormat ? `复制${currentFormatLabel}格式订阅地址` : '复制通用订阅地址'
                }
                title="复制订阅地址"
                onClick={() => handleCopy(endpointURL, `endpoint-${currentFormat}`)}
              >
                {endpointCopied ? (
                  <CheckIcon aria-hidden="true" />
                ) : (
                  <CopyIcon aria-hidden="true" />
                )}
              </button>
            </div>
            <p className="sub-endpoint-hint">在客户端中手动添加订阅；不同客户端请选择对应格式。</p>
          </div>
        </section>

        <section className="sub-mod sub-links" aria-label="链路状态">
          <header className="sub-mod-head">
            <span className="sub-tab">LINKS</span>
            <h2 className="sub-mod-title">链路状态</h2>
            <span className="sub-mod-meta">最近 30 次延迟（节点到面板服务器）</span>
          </header>
          <div className="sub-mod-body sub-mod-body-flush">
            {linkStatus && linkStatus.length > 0 ? (
              <div className="sub-link-list">
                {linkStatus.map((link) => (
                  <article key={link.label} className="sub-link-row">
                    <span className="sub-link-name">{link.label}</span>
                    <div className="sub-hop-list">
                      {link.hops.map((hop) => (
                        <LatencyHistory key={hop.label} label={hop.label} samples={hop.samples} />
                      ))}
                    </div>
                  </article>
                ))}
              </div>
            ) : linkStatus ? (
              <div className="sub-empty-line">暂无已采集的链路状态</div>
            ) : null}
          </div>
        </section>

        <footer className="sub-foot">
          <span>LATTIX</span>
          <span>
            每 {info.update_interval} 小时自动更新 · {statusDetail}
          </span>
        </footer>
      </div>

      {downloadClient ? (
        <div
          className="sub-modal-backdrop"
          role="presentation"
          onMouseDown={() => {
            if (!downloadTask || !['queued', 'downloading'].includes(downloadTask.status))
              closeDownload()
          }}
        >
          <section
            className="sub-modal"
            role="dialog"
            aria-modal="true"
            aria-labelledby="download-modal-title"
            onMouseDown={(event) => event.stopPropagation()}
          >
            <header className="sub-modal-head">
              <span className="sub-tab">PACKAGE</span>
              <h2 className="sub-modal-title" id="download-modal-title">
                下载 {downloadClient.name}
              </h2>
              <button
                type="button"
                className="cg-icon-button sub-modal-close"
                aria-label="关闭下载窗口"
                title="关闭"
                disabled={Boolean(
                  downloadTask && ['queued', 'downloading'].includes(downloadTask.status),
                )}
                onClick={closeDownload}
              >
                <XIcon aria-hidden="true" />
              </button>
            </header>

            <div className="sub-modal-body">
              <label className="sub-field-label" htmlFor="download-variant">
                安装包架构
              </label>
              <select
                id="download-variant"
                className="sub-select"
                value={downloadVariant}
                disabled={Boolean(
                  downloadTask && ['queued', 'downloading'].includes(downloadTask.status),
                )}
                onChange={(event) => setDownloadVariant(event.target.value)}
              >
                {downloadClient.download_variants?.map((variant) => (
                  <option key={variant.id} value={variant.id}>
                    {variant.label}
                  </option>
                ))}
              </select>

              {downloadTask ? (
                <div className="sub-progress" aria-live="polite">
                  <div className="sub-progress-label">
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
                    <strong className="tabular">
                      {Math.round(Math.max(0, Math.min(1, downloadTask.progress)) * 100)}%
                    </strong>
                  </div>
                  <div className="sub-progress-track">
                    <span
                      className="sub-progress-fill"
                      style={{
                        width: `${Math.round(Math.max(0, Math.min(1, downloadTask.progress)) * 100)}%`,
                      }}
                    />
                  </div>
                  {downloadTask.filename ? (
                    <small className="sub-progress-meta">{downloadTask.filename}</small>
                  ) : null}
                  {downloadTask.source_url ? (
                    <small className="sub-progress-meta">
                      下载源：
                      <a href={downloadTask.source_url} target="_blank" rel="noreferrer">
                        {downloadTask.source_url}
                      </a>
                    </small>
                  ) : null}
                  {downloadTask.sha256 ? (
                    <small className="sub-sha">
                      <span>SHA-256</span>
                      <code>{downloadTask.sha256}</code>
                      <button
                        type="button"
                        className="sub-sha-copy"
                        aria-label="复制 SHA-256 校验码"
                        onClick={() => void handleCopy(downloadTask.sha256 ?? '', 'sha256')}
                      >
                        {copied === 'sha256' ? (
                          <CheckIcon size={12} aria-hidden="true" />
                        ) : (
                          <CopyIcon size={12} aria-hidden="true" />
                        )}
                      </button>
                    </small>
                  ) : null}
                </div>
              ) : null}

              {downloadError || downloadTask?.error ? (
                <p className="sub-modal-error" role="alert">
                  {downloadError || downloadTask?.error}
                </p>
              ) : null}

              <div className="sub-modal-actions">
                {downloadTask?.status === 'failed' && downloadTask.source_url ? (
                  <button
                    type="button"
                    className="cg-button"
                    onClick={() => window.open(downloadTask.source_url, '_blank', 'noreferrer')}
                  >
                    <ArrowUpRight aria-hidden="true" />
                    自行下载
                  </button>
                ) : null}
                <button
                  type="button"
                  className="cg-button is-primary"
                  disabled={
                    !downloadVariant ||
                    Boolean(
                      downloadTask &&
                      ['queued', 'downloading', 'done', 'downloaded'].includes(downloadTask.status),
                    )
                  }
                  onClick={() => void startDownload()}
                >
                  {downloadTask && ['queued', 'downloading'].includes(downloadTask.status) ? (
                    <RefreshCwIcon className="sub-spin" aria-hidden="true" />
                  ) : (
                    <DownloadIcon aria-hidden="true" />
                  )}
                  {downloadTask && ['queued', 'downloading'].includes(downloadTask.status)
                    ? '下载中'
                    : downloadTask?.status === 'failed'
                      ? '重试下载'
                      : '开始下载'}
                </button>
              </div>
            </div>
          </section>
        </div>
      ) : null}

      <QRDialog text={subURL} open={qrOpen} onClose={() => setQrOpen(false)} />
    </main>
  )
}
