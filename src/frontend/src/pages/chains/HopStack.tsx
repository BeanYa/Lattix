import type { CSSProperties, ReactNode } from 'react'
import type { ChainOrchestration } from '@/lib/types'
import { cn } from '@/lib/utils'
import { buildHopStack, hopStackTotal } from './hop-stack'

/** 最多叠 2 张未运行的服务器卡，前面是当前跳位卡，其余用 +N 汇总。 */
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
      <div className="cg-hop-stack-deck">
        {backs.map((item, index) => (
          <div
            key={item.node.id}
            className="cg-hop cg-hop-stack-back"
            style={{ '--i': index + 1 } as CSSProperties}
            title={
              item.node.listen_port
                ? `${item.node.is_root ? '主选' : '备选'} · 端口 ${item.node.listen_port}`
                : item.node.is_root
                  ? '主选'
                  : '备选'
            }
            aria-hidden
          >
            <span className="cg-hop-head">
              <span className="cg-hop-num">{item.node.is_root ? 'R' : `B${index + 1}`}</span>
              <span className={cn('cg-hop-dot', item.node.online ? 'is-lime' : 'is-red')} />
              <strong className="cg-hop-name is-inline">
                {item.node.alias || `#${item.node.server_id}`}
              </strong>
              <span className="cg-hop-standby-badge">{item.node.is_root ? '主选' : '备选'}</span>
            </span>
            <span className="cg-hop-meta">
              {item.node.address ? <span>{item.node.address}</span> : null}
              {item.node.listen_port ? <span>端口 {item.node.listen_port}</span> : null}
              {item.node.kind_of_server === 'external' ? (
                <span className="cg-hop-external">未托管</span>
              ) : (
                <span className={item.node.online ? 'is-lime' : 'is-red'}>
                  {item.node.online ? 'Agent 在线' : 'Agent 离线'}
                </span>
              )}
              {item.node.banned ? <span className="is-red">被 ban</span> : null}
            </span>
          </div>
        ))}
        <div className="cg-hop-stack-front">{children}</div>
      </div>
      {orchestration?.triggered && active ? (
        <span className="cg-hop-stack-badge">
          当前运行：{active.node.alias || `#${active.node.server_id}`}
        </span>
      ) : null}
      {hidden > 0 ? <span className="cg-hop-stack-more">还有 {hidden} 台备选</span> : null}
    </div>
  )
}
