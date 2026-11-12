import { ArrowRightIcon, GlobeIcon, ServerIcon } from 'lucide-react'

import { formatLatency, latencyTone } from '@/pages/chains/chain-stats'

import './link-status.css'

/** CarrierStatus 是入口服务器到一个三网公开 CDN 测速点的最新延迟。 */
export interface CarrierStatus {
  label: string
  latency_ms: number | null
  sampled_at?: string
}

/** LinkStatus 是一条链路的匿名状态：入口三网延迟 + 内部段延迟加总（无服务器标识）。
 * entry_banned 表示入口（第一跳）服务器被 Ban 巡检判定且为可直连服务器（§告警分类）。 */
export interface LinkStatus {
  id?: string
  label: string
  entry: string
  carriers: CarrierStatus[]
  internal_ms: number | null
  entry_banned?: boolean
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

/** CarrierLatency 是单条三网源延迟读数：口径与链路详情段延迟一致
 * （<100/100–300/>300ms），失败样本如实显示「超时」，无样本显示「—」。
 * hover 说明保留源名与采样时间。 */
function CarrierLatency({ carrier }: { carrier: CarrierStatus }) {
  if (carrier.latency_ms === null) {
    const missing = !carrier.sampled_at
    return (
      <span
        className="sub-link-card-latency"
        data-tone={missing ? 'idle' : 'fail'}
        title={`${carrier.label} · ${missing ? '暂无三网测速样本' : '最近一次三网探测失败/超时'}`}
      >
        {missing ? '—' : '超时'}
      </span>
    )
  }
  return (
    <span
      className="sub-link-card-latency"
      data-tone={latencyTone(carrier.latency_ms)}
      title={`${carrier.label} · 延迟 ${formatLatency(carrier.latency_ms)}${
        carrier.sampled_at ? ` · 采样 ${formatSampleTime(carrier.sampled_at)}` : ''
      }`}
    >
      {formatLatency(carrier.latency_ms)}
    </span>
  )
}

/** LinkStatusCard 是单条链路的紧凑状态卡：卡头是链路名与入口角色（Banned 时加标），
 * 中段是三网测速源读数，卡尾是链路内部段与 Internet 终点。三网 → 入口 → 内部 →
 * Internet 的语义与旧流式行一致，只是把纵向堆叠压成三列读数。 */
function LinkStatusCard({
  link,
  unreachableRegion,
}: {
  link: LinkStatus
  unreachableRegion: string
}) {
  const internal = link.internal_ms
  const internalTone = internal === null || internal === 0 ? 'idle' : latencyTone(internal)
  const internalLabel = internal === null ? '—' : internal === 0 ? '直连' : formatLatency(internal)
  const internalTitle =
    internal === null
      ? '内部段延迟不完整（缺新鲜测速样本）'
      : internal === 0
        ? '单跳直连，无内部链路段'
        : '链路内部各段首选 RTT 之和'
  const region = unreachableRegion || '大陆'
  return (
    <article className="sub-link-card">
      <header className="sub-link-card-head">
        <h3 className="sub-link-card-name" title={link.label}>
          {link.label}
        </h3>
        <span className="sub-link-card-role" title={`入口角色：${link.entry}`}>
          <ServerIcon size={11} aria-hidden="true" />
          {link.entry}
        </span>
        {link.entry_banned ? (
          <span
            className="sub-link-unreachable"
            title={`入口服务器三网测速（${region}）判定 Banned`}
          >
            Banned
          </span>
        ) : null}
      </header>
      {link.carriers.length > 0 ? (
        <div className="sub-link-card-carriers" role="group" aria-label="入口三网延迟">
          {link.carriers.map((carrier) => (
            <div className="sub-link-card-carrier" key={carrier.label}>
              <span className="sub-link-card-carrier-name" title={carrier.label}>
                {carrier.label}
              </span>
              <CarrierLatency carrier={carrier} />
            </div>
          ))}
        </div>
      ) : (
        <p className="sub-link-card-empty">暂无三网测速数据</p>
      )}
      <div className="sub-link-card-tail">
        <span className="sub-link-card-seg">
          <span className="sub-link-card-seg-label">内部</span>
          <span className="sub-link-latency" data-tone={internalTone} title={internalTitle}>
            {internalLabel}
          </span>
        </span>
        <i className="sub-link-card-track" aria-hidden="true" />
        <ArrowRightIcon className="sub-link-card-arrow" size={11} aria-hidden="true" />
        <span className="sub-link-card-wan">
          <GlobeIcon size={12} aria-hidden="true" />
          Internet
        </span>
      </div>
    </article>
  )
}

/** LinkStatusGrid 是公开订阅页的链路状态区：每条链路一张小卡片，桌面多列、窄屏单列。
 * 空列表由页面侧处理，这里只负责有数据时的呈现。 */
export function LinkStatusGrid({
  links,
  unreachableRegion,
}: {
  links: LinkStatus[]
  unreachableRegion: string
}) {
  return (
    <ul className="sub-link-grid">
      {links.map((link) => (
        <li className="sub-link-grid-item" key={link.id ?? link.label}>
          <LinkStatusCard link={link} unreachableRegion={unreachableRegion} />
        </li>
      ))}
    </ul>
  )
}
