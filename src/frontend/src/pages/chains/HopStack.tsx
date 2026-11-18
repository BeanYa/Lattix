import type { CSSProperties, ReactNode } from 'react'
import type { ChainOrchestration } from '@/lib/types'
import { cn } from '@/lib/utils'
import { buildHopStack, hopStackTotal } from './hop-stack'

/** 最多叠 3 张备选卡（含前卡共 4 张），其余用 +N 汇总。 */
const MAX_BACKS = 3

/**
 * 链路详情里的跳位堆叠（只读）：前卡就是跳位本身的服务器卡片（children），
 * 备选用同样的卡片造型叠在它后面（右上偏移），不另外画一套小方块。
 * 没有备选的跳不包层，保持普通链路的原样式。
 * 详情/概览只做信息查询，编排的编辑入口在链路编辑器画布的卡片上。
 */
export function HopStack({
  orchestration,
  children,
}: {
  orchestration?: ChainOrchestration
  children: ReactNode
}) {
  const stack = buildHopStack(orchestration)
  const backs = stack.slice(1, 1 + MAX_BACKS)
  const active = stack.find((item) => item.active)
  if (stack.length <= 1) return <>{children}</>
  const hidden = Math.max(0, hopStackTotal(orchestration) - 1 - backs.length)
  return (
    <div className="cg-hop-stack" style={{ '--cg-stack-depth': backs.length } as CSSProperties}>
      {backs.map((item, index) => (
        <div
          key={item.node.id}
          className={cn('cg-hop-stack-back', item.active && 'is-active')}
          style={{ '--i': index + 1 } as CSSProperties}
          aria-hidden
        >
          <span>{item.node.alias || `#${item.node.server_id}`}</span>
        </div>
      ))}
      <div className="cg-hop-stack-front">{children}</div>
      {orchestration?.triggered && active ? (
        <span className="cg-hop-stack-badge">
          当前运行：{active.node.alias || `#${active.node.server_id}`}
        </span>
      ) : null}
      {hidden > 0 ? <span className="cg-hop-stack-more">还有 {hidden} 台备选</span> : null}
    </div>
  )
}
