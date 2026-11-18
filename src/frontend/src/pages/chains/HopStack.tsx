import type { CSSProperties, ReactNode } from 'react'
import type { ChainOrchestration } from '@/lib/types'
import { cn } from '@/lib/utils'
import { buildHopStack, hopStackTotal } from './hop-stack'

/** 最多叠 2 张备选卡（前面是跳位原卡），其余用 +N 汇总。 */
const MAX_BACKS = 2

/**
 * 链路详情里的跳位堆叠（只读）：前卡就是跳位本身的服务器卡片（children），
 * 备选是同款的服务器卡片——同样的卡片表面、同样的头行——依次向右上偏移叠在
 * 后面，露出一条可读的卡头。没有备选的跳不包层，保持普通链路的原样式。
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
          className={cn('cg-hop', 'cg-hop-stack-back', item.active && 'is-active')}
          style={{ '--i': index + 1 } as CSSProperties}
          title={
            item.node.listen_port
              ? `备选 · 端口 ${item.node.listen_port} · ${item.node.online ? '在线' : '离线'}`
              : `备选 · ${item.node.online ? '在线' : '离线'}`
          }
          aria-hidden
        >
          <span className="cg-hop-head">
            <span className="cg-hop-num">B{index + 1}</span>
            <span className={cn('cg-hop-dot', item.node.online ? 'is-lime' : 'is-red')} />
            <span className="cg-hop-role">备选</span>
            <strong className="cg-hop-name is-inline">
              {item.node.alias || `#${item.node.server_id}`}
            </strong>
          </span>
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
