import { useState, type CSSProperties, type KeyboardEvent } from 'react'
import type { ChainOrchestration } from '@/lib/types'
import { cn } from '@/lib/utils'
import { buildHopStack, hopStackTotal } from './hop-stack'

export function HopStack({
  orchestration,
  hopAlias,
  onEdit,
}: {
  orchestration?: ChainOrchestration
  hopAlias: string
  onEdit?: () => void
}) {
  const [expanded, setExpanded] = useState(false)
  const stack = buildHopStack(orchestration)
  const total = hopStackTotal(orchestration)
  const active = stack.find((item) => item.active)

  if (!orchestration || stack.length === 0) {
    return (
      <div className="cg-hop-stack is-bare">
        <button
          type="button"
          className="cg-hop-stack-dot"
          aria-label="配置自动编排"
          onClick={onEdit}
        />
        <span className="cg-hop-stack-hint">自动编排</span>
      </div>
    )
  }

  const [root, ...backs] = stack
  const onKeyDown = (event: KeyboardEvent<HTMLDivElement>) => {
    if (event.key === 'Enter' || event.key === ' ') {
      event.preventDefault()
      onEdit?.()
    }
  }
  return (
    <div className={cn('cg-hop-stack', expanded && 'is-expanded')}>
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
        <div
          className="cg-hop-stack-card is-front"
          role="button"
          tabIndex={0}
          aria-label="编辑该跳自动编排"
          onClick={onEdit}
          onKeyDown={onKeyDown}
        >
          <span>{root.node.alias || hopAlias}</span>
          <em>原服务器</em>
        </div>
      </div>
      <button
        type="button"
        className="cg-hop-stack-dot"
        aria-expanded={expanded}
        aria-label="展开/收起编排卡片"
        onClick={() => setExpanded((value) => !value)}
      />
      {total > 4 ? <span className="cg-hop-stack-more">+{total - 4}</span> : null}
      {orchestration.triggered && active ? (
        <span className="cg-hop-stack-badge">
          当前运行：{active.node.alias || `#${active.node.server_id}`}
        </span>
      ) : null}
    </div>
  )
}
