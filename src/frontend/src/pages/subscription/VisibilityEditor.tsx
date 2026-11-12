import { useId, useState } from 'react'
import { ChevronDownIcon } from 'lucide-react'

import '../subscription.css'

export interface VisibilityLink {
  id: string
  label: string
}

export interface VisibilityExternalSubscription extends VisibilityLink {
  nodes: string[]
}

interface VisibilitySelection {
  hiddenIds: ReadonlySet<string>
  onVisibilityChange: (id: string, visible: boolean) => void
}

function VisibilityCheckbox({
  item,
  hiddenIds,
  onVisibilityChange,
}: VisibilitySelection & { item: VisibilityLink }) {
  return (
    <label className="sub-visibility-check">
      <input
        type="checkbox"
        checked={!hiddenIds.has(item.id)}
        onChange={(event) => onVisibilityChange(item.id, event.target.checked)}
      />
      <span>{item.label}</span>
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
    <li className="sub-visibility-item">
      <div className="sub-visibility-row">
        <VisibilityCheckbox item={source} {...selection} />
        {source.nodes.length > 0 ? (
          <button
            type="button"
            className="sub-visibility-disclosure"
            aria-expanded={expanded}
            aria-controls={nodeListId}
            aria-label={`${source.label}的 ${source.nodes.length} 个节点`}
            onClick={() => setExpanded((previous) => !previous)}
          >
            {source.nodes.length} 个节点
            <ChevronDownIcon size={14} aria-hidden="true" />
          </button>
        ) : (
          <span className="sub-visibility-node-count">暂无节点</span>
        )}
      </div>
      {source.nodes.length > 0 ? (
        <ul id={nodeListId} className="sub-visibility-nodes" hidden={!expanded}>
          {source.nodes.map((node, index) => (
            <li key={`${node}-${index}`}>{node}</li>
          ))}
        </ul>
      ) : null}
    </li>
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
    return <p className="sub-cfg-hint">暂无可选的链路或外部订阅。</p>
  }

  return (
    <div className="sub-visibility-editor">
      {links.length > 0 ? (
        <fieldset>
          <legend>面板链路</legend>
          <ul className="sub-visibility-list">
            {links.map((link) => (
              <li className="sub-visibility-item" key={link.id}>
                <div className="sub-visibility-row">
                  <VisibilityCheckbox item={link} {...selection} />
                </div>
              </li>
            ))}
          </ul>
        </fieldset>
      ) : null}
      {externalSubscriptions.length > 0 ? (
        <fieldset>
          <legend>外部订阅</legend>
          <ul className="sub-visibility-list">
            {externalSubscriptions.map((source) => (
              <ExternalVisibilityRow key={source.id} source={source} {...selection} />
            ))}
          </ul>
        </fieldset>
      ) : null}
    </div>
  )
}
