import { useState, type CSSProperties, type KeyboardEvent } from 'react'
import type { ChainOrchestration } from '@/lib/types'
import { cn } from '@/lib/utils'
import { buildHopStack, hopStackTotal } from './hop-stack'

/**
 * 链路详情里的跳位堆叠（只读）：详情/概览只做信息查询，编排的编辑入口在链路编辑器
 * 画布的卡片上。未配置编排的跳不渲染任何东西，沿用原样式；配置了备选的跳以堆叠卡片
 * 展示，最前方是原服务器（根），其余为备选，当前运行的服务器标注「编排已触发」。
 * 点击堆叠展开/收起全部备选（纯展示，不进入编辑）。
 */
export function HopStack({
  orchestration,
  hopAlias,
}: {
  orchestration?: ChainOrchestration
  hopAlias: string
}) {
  const [expanded, setExpanded] = useState(false)
  const stack = buildHopStack(orchestration)
  // 没有编排图，或图里只有根服务器（没有备选）：保持普通链路的原样。
  if (stack.length === 0) return null
  const [root, ...backs] = stack
  const total = hopStackTotal(orchestration)
  const active = stack.find((item) => item.active)
  const single = backs.length === 0 && !orchestration?.triggered
  if (single) return null

  const onKeyDown = (event: KeyboardEvent<HTMLDivElement>) => {
    if (event.key === 'Enter' || event.key === ' ') {
      event.preventDefault()
      setExpanded((value) => !value)
    }
  }
  return (
    <div
      className={cn('cg-hop-stack', expanded && 'is-expanded')}
      role="button"
      tabIndex={0}
      aria-expanded={expanded}
      aria-label={`展开/收起 ${hopAlias} 的备选服务器`}
      title="展开/收起备选服务器"
      onClick={() => setExpanded((value) => !value)}
      onKeyDown={onKeyDown}
    >
      <div className="cg-hop-stack-cards">
        {backs.map((item, index) => (
          <div
            key={item.node.id}
            className={cn('cg-hop-stack-card', item.active && 'is-active')}
            style={{ '--i': index + 1 } as CSSProperties}
          >
            <span>{item.node.alias || `#${item.node.server_id}`}</span>
            {item.active ? <em>编排已触发</em> : null}
          </div>
        ))}
        <div className="cg-hop-stack-card is-front">
          <span>{root.node.alias || hopAlias}</span>
          <em>原服务器</em>
        </div>
      </div>
      {total > 4 ? <span className="cg-hop-stack-more">+{total - 4}</span> : null}
      {orchestration?.triggered && active ? (
        <span className="cg-hop-stack-badge">
          当前运行：{active.node.alias || `#${active.node.server_id}`}
        </span>
      ) : null}
    </div>
  )
}
