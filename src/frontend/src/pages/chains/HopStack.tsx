import { useState, type CSSProperties, type KeyboardEvent } from 'react'
import { createPortal } from 'react-dom'
import type { Chain, ChainHop, ChainOrchestration } from '@/lib/types'
import { cn } from '@/lib/utils'
import { buildHopStack, hopStackTotal } from './hop-stack'
import { OrchestrationEditor } from './orchestration/OrchestrationEditor'

export function HopStack({
  orchestration,
  hopAlias,
  chain,
  hop,
  onEdit,
  onChanged,
}: {
  orchestration?: ChainOrchestration
  hopAlias: string
  chain?: Chain
  hop?: ChainHop
  onEdit?: () => void
  onChanged?: () => void
}) {
  const [expanded, setExpanded] = useState(false)
  const [editorOpen, setEditorOpen] = useState(false)
  const openEditor = () => {
    if (onEdit) {
      onEdit()
      return
    }
    if (chain && hop) setEditorOpen(true)
  }
  const stack = buildHopStack(orchestration)
  const total = hopStackTotal(orchestration)
  const active = stack.find((item) => item.active)

  if (!orchestration || stack.length === 0) {
    // 未配置编排：只留黄点入口，文案按需浮出——普通链路不应被标注成「自动编排」。
    return (
      <div className="cg-hop-stack is-bare">
        <button
          type="button"
          className="cg-hop-stack-dot"
          aria-label="配置自动编排"
          title="配置自动编排"
          onClick={openEditor}
        />
        <span className="cg-hop-stack-hint">配置编排</span>
      </div>
    )
  }

  const [root, ...backs] = stack
  const onKeyDown = (event: KeyboardEvent<HTMLDivElement>) => {
    if (event.key === 'Enter' || event.key === ' ') {
      event.preventDefault()
      openEditor()
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
          onClick={openEditor}
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
      {editorOpen && chain && hop
        ? createPortal(
            <OrchestrationEditor
              chain={chain}
              hop={hop}
              onClose={() => setEditorOpen(false)}
              onSaved={() => {
                setEditorOpen(false)
                onChanged?.()
              }}
            />,
            document.body,
          )
        : null}
    </div>
  )
}
