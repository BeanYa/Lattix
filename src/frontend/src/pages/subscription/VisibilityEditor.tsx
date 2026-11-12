import { useId, useState, type ReactNode } from 'react'
import { CheckIcon, ChevronDownIcon } from 'lucide-react'

import {
  visibilityItemKind,
  type VisibilityExternalSubscription,
  type VisibilityLink,
} from './visibility-items'
import './visibility.css'

export type { VisibilityExternalSubscription, VisibilityLink } from './visibility-items'

interface VisibilitySelection {
  hiddenIds: ReadonlySet<string>
  onVisibilityChange: (id: string, visible: boolean) => void
}

/** 链路身份色按 id % 9 分配（DESIGN.md：线路身份色只出现在链路徽标与链路序列）。 */
function identityColor(id: string): string | undefined {
  const numeric = Number(id.slice(id.indexOf(':') + 1))
  return id.startsWith('chain:') && Number.isFinite(numeric) && numeric >= 0
    ? `var(--panel-line-${numeric % 9})`
    : undefined
}

function VisibilityCheckbox({
  item,
  hiddenIds,
  onVisibilityChange,
}: VisibilitySelection & { item: VisibilityLink }) {
  const visible = !hiddenIds.has(item.id)
  const color = identityColor(item.id)
  return (
    <label className="vis-item-main">
      <span className="vis-check">
        <input
          type="checkbox"
          checked={visible}
          onChange={(event) => onVisibilityChange(item.id, event.target.checked)}
        />
        <span className="vis-check-box" aria-hidden="true">
          {visible ? <CheckIcon size={12} strokeWidth={3} /> : null}
        </span>
      </span>
      <span className="vis-item-copy">
        <span className="vis-item-label">{item.label}</span>
        <span className="vis-item-meta">
          {color ? (
            <span className="vis-id-dot" style={{ background: color }} aria-hidden="true" />
          ) : null}
          {visibilityItemKind(item)}
        </span>
      </span>
      <span className="vis-item-state" data-visible={visible}>
        {visible ? '可见' : '已隐藏'}
      </span>
    </label>
  )
}

function ExternalVisibilityRow({
  source,
  ...selection
}: VisibilitySelection & { source: VisibilityExternalSubscription }) {
  const nodeListId = useId()
  const [expanded, setExpanded] = useState(false)
  return (
    <li className="vis-item is-external" data-expanded={expanded || undefined}>
      <div className="vis-item-head">
        <VisibilityCheckbox item={source} {...selection} />
        {source.nodes.length > 0 ? (
          <button
            type="button"
            className="vis-disclosure"
            aria-expanded={expanded}
            aria-controls={nodeListId}
            aria-label={`${source.label}的 ${source.nodes.length} 个节点`}
            onClick={() => setExpanded((previous) => !previous)}
          >
            {source.nodes.length} 个节点
            <ChevronDownIcon size={14} aria-hidden="true" />
          </button>
        ) : (
          <span className="vis-node-count">暂无节点</span>
        )}
      </div>
      {source.nodes.length > 0 ? (
        <ul id={nodeListId} className="vis-nodes sub-visibility-nodes" hidden={!expanded}>
          {source.nodes.map((node, index) => (
            <li key={`${node}-${index}`}>{node}</li>
          ))}
        </ul>
      ) : null}
    </li>
  )
}

function SectionBulkToggle({
  hiddenIds,
  items,
  onVisibilityChange,
}: VisibilitySelection & { items: VisibilityLink[] }) {
  const allVisible = items.every((item) => !hiddenIds.has(item.id))
  if (items.length === 0) return null
  return (
    <button
      type="button"
      className="vis-bulk"
      onClick={() => {
        for (const item of items) onVisibilityChange(item.id, !allVisible)
      }}
    >
      {allVisible ? '全部隐藏' : '全部显示'}
    </button>
  )
}

function Section({
  title,
  description,
  items,
  children,
  ...selection
}: VisibilitySelection & {
  title: string
  description: string
  items: VisibilityLink[]
  children: ReactNode
}) {
  const headingId = useId()
  const visible = items.filter((item) => !selection.hiddenIds.has(item.id)).length
  return (
    <section className="vis-section" aria-labelledby={headingId}>
      <header className="vis-section-head">
        <div className="vis-section-copy">
          <h3 className="vis-section-title" id={headingId}>
            {title}
          </h3>
          <p className="vis-section-desc">{description}</p>
        </div>
        <div className="vis-section-tools">
          <span className="vis-section-count">
            <span className="vis-section-count-value">{visible}</span>
            <span className="vis-section-count-total">/ {items.length} 可见</span>
          </span>
          <SectionBulkToggle items={items} {...selection} />
        </div>
      </header>
      {children}
    </section>
  )
}

export function VisibilityEditor({
  links,
  externalSubscriptions,
  ...selection
}: VisibilitySelection & {
  links: VisibilityLink[]
  externalSubscriptions: VisibilityExternalSubscription[]
}) {
  if (links.length === 0 && externalSubscriptions.length === 0) {
    return <p className="vis-empty">暂无可选的链路或外部订阅。</p>
  }

  return (
    <div className="vis-editor">
      {links.length > 0 ? (
        <Section
          title="面板链路"
          description="订阅中展示的中转与直连链路"
          items={links}
          {...selection}
        >
          <ul className="vis-list">
            {links.map((link) => (
              <li className="vis-item" key={link.id}>
                <VisibilityCheckbox item={link} {...selection} />
              </li>
            ))}
          </ul>
        </Section>
      ) : null}
      {externalSubscriptions.length > 0 ? (
        <Section
          title="外部订阅"
          description="整份开关；展开可查看来源内的节点"
          items={externalSubscriptions}
          {...selection}
        >
          <ul className="vis-list is-external">
            {externalSubscriptions.map((source) => (
              <ExternalVisibilityRow key={source.id} source={source} {...selection} />
            ))}
          </ul>
        </Section>
      ) : null}
    </div>
  )
}
