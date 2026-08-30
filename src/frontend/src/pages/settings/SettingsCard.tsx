import type { ReactNode } from 'react'
import type { LucideIcon } from 'lucide-react'

// 设置分区卡片：左列描述/图标，右列实际配置控件（纯展示组件，无逻辑）。
export function SettingsCard({
  icon: Icon,
  tag,
  title,
  description,
  aside,
  children,
}: {
  icon: LucideIcon
  tag: string
  title: ReactNode
  description?: ReactNode
  aside?: ReactNode
  children: ReactNode
}) {
  return (
    <section className="cg-card cg-set-card">
      <aside className="cg-set-card-aside">
        <span className="cg-set-card-icon">
          <Icon />
        </span>
        <span className="cg-micro cg-set-card-tag">{tag}</span>
        <h2 className="cg-title cg-set-card-title">{title}</h2>
        {description ? <p className="cg-set-card-desc">{description}</p> : null}
        {aside}
      </aside>
      <div className="cg-set-card-body">{children}</div>
    </section>
  )
}
