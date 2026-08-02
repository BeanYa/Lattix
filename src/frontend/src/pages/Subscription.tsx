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
  ChevronRightIcon,
  CircleAlertIcon,
  CopyIcon,
  GaugeIcon,
  RadioIcon,
  RefreshCwIcon,
  ShieldCheckIcon,
  WifiIcon,
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
  announcement: string
  update_interval: string
}

interface ClientInfo {
  name: string
  platform: string
  deeplink: string
  format: string
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
  announcement: '新的东京与洛杉矶线路已上线。客户端会在下一次自动更新时同步节点。',
  update_interval: '6',
}

const previewClients: ClientInfo[] = [
  { name: 'Stash', platform: 'ios', deeplink: '#preview-stash', format: 'stash' },
  { name: 'Shadowrocket', platform: 'ios', deeplink: '#preview-shadowrocket', format: 'base64' },
  { name: 'Clash Meta', platform: 'android', deeplink: '#preview-clash-meta', format: 'mihomo' },
  { name: 'v2rayNG', platform: 'android', deeplink: '#preview-v2rayng', format: 'base64' },
  { name: 'Mihomo Party', platform: 'windows', deeplink: '#preview-mihomo-party', format: 'mihomo' },
  { name: 'Clash Verge', platform: 'macos', deeplink: '#preview-clash-verge', format: 'mihomo' },
  { name: '通用订阅', platform: 'universal', deeplink: '', format: 'base64' },
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
  const [error, setError] = useState('')
  const [copied, setCopied] = useState('')
  const [activePlatform, setActivePlatform] = useState('all')
  const copyTimer = useRef<number | undefined>(undefined)

  useEffect(() => {
    if (!token) {
      setError('链接中缺少有效的订阅凭证。')
      return
    }
    if (isPreview) {
      setInfo(previewInfo)
      setClients(previewClients)
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
    ])
      .then(([nextInfo, nextClients]) => {
        setInfo(nextInfo)
        setClients(nextClients)
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
                  const content = (
                    <>
                      <span className="client-index">{String(index + 1).padStart(2, '0')}</span>
                      <span className="client-name">
                        <strong>{client.name}</strong>
                        <small>{platformLabels[client.platform] ?? client.platform} · {client.format}</small>
                      </span>
                      <span className="client-action-icon">
                        {copied === label ? <CheckIcon /> : client.deeplink ? <ArrowUpRight /> : <CopyIcon />}
                      </span>
                    </>
                  )
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

        {info.announcement ? (
          <aside className="announcement">
            <span className="announcement-icon"><WifiIcon /></span>
            <div>
              <span>网络公告</span>
              <p>{info.announcement}</p>
            </div>
            <ChevronRightIcon />
          </aside>
        ) : null}

        <footer className="subscription-footer">
          <span>Lattix</span>
          <span>每 {info.update_interval} 小时自动更新 · {statusDetail}</span>
        </footer>
      </div>
    </main>
  )
}
