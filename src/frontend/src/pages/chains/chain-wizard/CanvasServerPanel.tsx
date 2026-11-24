import { useMemo, useRef, useState } from 'react'
import { flushSync } from 'react-dom'
import { PlusIcon, SearchIcon } from 'lucide-react'

import { CountryFlag } from '@/components/CountryFlag'
import { Input } from '@/components/ui/input'
import { Tabs, TabsContent, TabsList, TabsTrigger } from '@/components/ui/tabs'
import { cn } from '@/lib/utils'
import type { CatalogProbeRow, OrchestrationTemplate, Server } from '@/lib/types'

import { CARD_W, CARD_H, isExternalServer, isUsableExternalServer } from './chain-canvas'
import { ChainNodeDragPreview } from './ChainNodeCard'

import { ServerProbeMini } from './ServerProbeMini'

/** 画布左侧服务器面板：搜索过滤 + 简化版服务器卡（名称 + 三网测速，参考服务器详情卡样式），
 * 整卡可拖拽到画布添加节点（同服务器可重复添加），「+」为点选添加。 */
export function CanvasServerPanel({
  servers,
  probesByServer,
  probeTitle,
  usedCount,
  canAddMore,
  onAdd,
  emptyCanvas,
  templates = [],
  onAddTemplate,
  templateError,
}: {
  templates?: OrchestrationTemplate[]
  onAddTemplate?: (templateId: number) => void
  templateError?: string
  emptyCanvas: boolean
  servers: Server[]
  probesByServer: Map<number, CatalogProbeRow[]>
  probeTitle: string | null
  usedCount: (serverId: number) => number
  canAddMore: boolean
  onAdd: (server: Server) => void
}) {
  const [tab, setTab] = useState<'servers' | 'templates'>('servers')
  const [query, setQuery] = useState('')
  const [draggedServer, setDraggedServer] = useState<Server | null>(null)
  const previewRef = useRef<HTMLDivElement>(null)
  const filtered = useMemo(() => {
    const keyword = query.trim().toLowerCase()
    if (!keyword) return servers
    return servers.filter((server) =>
      `${server.alias} ${server.location} ${server.country_code}`.toLowerCase().includes(keyword),
    )
  }, [servers, query])

  return (
    <Tabs
      render={<aside />}
      className="cw-server-panel"
      aria-label="服务器与编排模板"
      value={tab}
      onValueChange={(value) => {
        setTab(value as 'servers' | 'templates')
        setQuery('')
      }}
    >
      <TabsList className="cw-library-tabs" aria-label="节点来源">
        <TabsTrigger value="servers" aria-controls="cw-server-list">
          服务器
        </TabsTrigger>
        <TabsTrigger value="templates" aria-controls="cw-template-list">
          编排模板
        </TabsTrigger>
      </TabsList>
      <div className="cw-server-panel-search">
        <SearchIcon aria-hidden />
        <Input
          value={query}
          onChange={(event) => setQuery(event.target.value)}
          placeholder={tab === 'servers' ? '搜索服务器' : '搜索编排模板'}
          aria-label={tab === 'servers' ? '搜索服务器' : '搜索编排模板'}
        />
      </div>
      {tab === 'templates' ? (
        <TabsContent value="templates" id="cw-template-list" className="cw-library-content">
          <p className="cw-server-panel-hint">
            点击或拖入主选服务器与完整编排；同一主选需确认替换。
          </p>
          {templateError ? (
            <p className="cw-server-panel-hint" role="alert">
              {templateError}
            </p>
          ) : null}
          <ul className="cw-server-list">
            {templates
              .filter((t) => t.name.toLowerCase().includes(query.trim().toLowerCase()))
              .map((template) => {
                const rootId = template.nodes.find(
                  (n) => n.kind === 'server' && n.is_root,
                )?.server_id
                const existing = rootId !== undefined && usedCount(rootId) > 0
                const disabled = !rootId || (!canAddMore && !existing)
                return (
                  <li
                    key={template.id}
                    className="cw-server-item cw-template-item"
                    draggable={!disabled}
                    onDragStart={(event) => {
                      event.dataTransfer.setData(
                        'text/x-lattix-orchestration-template',
                        String(template.id),
                      )
                      event.dataTransfer.effectAllowed = 'copy'
                    }}
                  >
                    <div className="cw-server-item-head">
                      <span className="cw-server-item-alias">{template.name}</span>
                      <button
                        type="button"
                        className="cw-server-item-add"
                        disabled={disabled}
                        aria-label={`添加模板 ${template.name} 到画布`}
                        onClick={() => onAddTemplate?.(template.id)}
                      >
                        <PlusIcon />
                      </button>
                    </div>
                    <p className="cw-template-meta">
                      主选：
                      {servers.find((s) => s.id === rootId)?.alias ?? `#${rootId ?? '未设置'}`}
                    </p>
                    <p className="cw-template-meta">
                      {template.nodes.filter((n) => n.kind === 'server').length} 台服务器 ·{' '}
                      {template.nodes.filter((n) => n.kind === 'trigger').length} 个动作 ·{' '}
                      {template.usage_count} 个引用
                    </p>
                  </li>
                )
              })}
            {!templates.some((t) => t.name.toLowerCase().includes(query.trim().toLowerCase())) ? (
              <li className="cw-server-panel-empty">没有匹配的编排模板</li>
            ) : null}
          </ul>
        </TabsContent>
      ) : (
        <TabsContent value="servers" id="cw-server-list" className="cw-library-content">
          {!canAddMore ? (
            <p className="cw-server-panel-hint">画布节点已达上限，先移除节点再添加</p>
          ) : (
            <p className="cw-server-panel-hint">
              拖到画布添加节点；可分支/汇合，同一服务器在一张画布中只能出现一次
            </p>
          )}
          <ul className="cw-server-list">
            {filtered.map((server) => {
              const count = usedCount(server.id)
              // 外部节点未补全地址/端口（服务器表单没有必填项）：不能加入画布。
              const unusable = isExternalServer(server) && !isUsableExternalServer(server)
              return (
                <li
                  key={server.id}
                  className={cn('cw-server-item', (!canAddMore || unusable) && 'is-disabled')}
                  draggable={canAddMore && !unusable}
                  onDragEnd={() => setDraggedServer(null)}
                  onDragStart={(event) => {
                    event.dataTransfer.setData('text/x-lattix-server', String(server.id))
                    event.dataTransfer.effectAllowed = 'copy'
                    flushSync(() => setDraggedServer(server))
                    const preview = previewRef.current
                    if (preview)
                      event.dataTransfer.setDragImage(preview, CARD_W / 2 + 12, CARD_H / 2 + 12)
                  }}
                >
                  <div className="cw-server-item-head">
                    <CountryFlag code={server.country_code} />
                    <span className="cw-server-item-alias" title={server.alias}>
                      {server.alias}
                    </span>
                    {isExternalServer(server) ? (
                      <em
                        className="cw-server-item-exitonly"
                        title={
                          unusable
                            ? '未托管外部直链节点未配置地址/端口：请先在服务器页补全协议参数'
                            : '未托管外部直链节点：只能作链路最后一跳（出口）'
                        }
                      >
                        {unusable ? '未配置参数' : '仅可作最后一跳'}
                      </em>
                    ) : null}
                    {count > 0 ? <span className="cw-server-item-count">×{count}</span> : null}
                    <button
                      type="button"
                      className="cw-server-item-add"
                      aria-label={`添加 ${server.alias} 到画布`}
                      disabled={!canAddMore || unusable}
                      title={unusable ? '请先在服务器页补全该外部节点的地址与端口' : undefined}
                      onClick={() => onAdd(server)}
                    >
                      <PlusIcon />
                    </button>
                  </div>
                  <ServerProbeMini
                    server={server}
                    probeRows={probesByServer.get(server.id) ?? []}
                    probeTitle={probeTitle}
                  />
                </li>
              )
            })}
            {filtered.length === 0 ? (
              <li className="cw-server-panel-empty">没有匹配的服务器</li>
            ) : null}
          </ul>
        </TabsContent>
      )}
      <div
        ref={previewRef}
        className="cw-drag-image"
        aria-hidden
        inert
        style={{ width: CARD_W + 24, height: CARD_H + 24 }}
      >
        {draggedServer ? (
          <ChainNodeDragPreview
            server={draggedServer}
            probeRows={probesByServer.get(draggedServer.id) ?? []}
            probeTitle={probeTitle}
            emptyCanvas={emptyCanvas}
          />
        ) : null}
      </div>
    </Tabs>
  )
}
