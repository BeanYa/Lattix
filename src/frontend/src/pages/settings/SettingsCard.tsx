import type { ReactNode } from 'react'

// 设置分区模块：1px 发丝线矩形 + 红色索引标签；描述行贴顶，配置控件在下方密排。
// 纯展示组件，无逻辑；标签统一按「INDEX / SECTION」拆成模块索引与右侧 meta。
export function SettingsCard({
  tag,
  title,
  description,
  aside,
  children,
}: {
  tag: string
  title: ReactNode
  description?: ReactNode
  aside?: ReactNode
  children: ReactNode
}) {
  const [index, ...rest] = tag.split('/').map((part) => part.trim())
  const meta = rest.join(' / ')

  return (
    <section className="st-mod">
      <header className="st-mod-head">
        <span className="st-index">{index}</span>
        <h2 className="st-mod-title">{title}</h2>
        <span className="st-mod-spacer" />
        {meta ? <span className="st-mod-meta">{meta}</span> : null}
        {aside ? <span className="st-mod-aside">{aside}</span> : null}
      </header>
      {description ? <p className="st-mod-desc">{description}</p> : null}
      <div className="st-mod-body">{children}</div>
    </section>
  )
}
