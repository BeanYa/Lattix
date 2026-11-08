import { humanizeBytes } from '@/lib/format'
import type { TrafficQuotaSummary } from '@/lib/types'

/** QuotaCardView 是「服务器流量额度」单卡的展示模型。 */
export interface QuotaCardView {
  value: string
  detail: string
  tone?: 'warn' | 'danger'
}

/** humanizeSignedBytes 在 humanizeBytes 基础上保留负号（超支场景显示为负值）。 */
export function humanizeSignedBytes(bytes: number): string {
  return bytes < 0 ? `-${humanizeBytes(-bytes)}` : humanizeBytes(bytes)
}

/**
 * quotaCardViews 把被控服务器的流量额度汇总映射成三卡文案：
 * 总可用 = 已设上限服务器上限加总；当前可用 = 总可用 − 已用（可为负）；
 * 已用 = 全部被控服务器当前计费周期已用加总。
 * 无限/未设置上限的服务器不计入总可用，此时当前可用展示「—」。
 */
export function quotaCardViews(
  quota: TrafficQuotaSummary,
  agentsTotal: number,
): { total: QuotaCardView; current: QuotaCardView; used: QuotaCardView } {
  const total = quota.total_available_bytes
  const current = quota.current_available_bytes
  const used = quota.used_bytes

  const totalCard: QuotaCardView = {
    value: humanizeBytes(total),
    detail:
      total > 0 ? `${quota.limited_servers} / ${agentsTotal} 台已设上限` : '暂无服务器设置流量上限',
  }

  let currentCard: QuotaCardView
  if (total <= 0) {
    currentCard = { value: '—', detail: '无限流量服务器不计入上限' }
  } else if (current < 0) {
    currentCard = {
      value: humanizeSignedBytes(current),
      detail: `已用超出上限 ${humanizeBytes(-current)}`,
      tone: 'danger',
    }
  } else {
    // total>0 且 current>=0 时已用必然不超过总上限，占比落在 0–100%。
    const usedPercent = Math.round((used / total) * 100)
    currentCard = {
      value: humanizeSignedBytes(current),
      detail: `已用 ${usedPercent}%`,
      tone: usedPercent >= 90 ? 'danger' : usedPercent >= 75 ? 'warn' : undefined,
    }
  }

  return {
    total: totalCard,
    current: currentCard,
    used: {
      value: humanizeBytes(used),
      detail: agentsTotal > 0 ? `全部 ${agentsTotal} 台 · 当前计费周期` : '暂无被控服务器',
    },
  }
}
