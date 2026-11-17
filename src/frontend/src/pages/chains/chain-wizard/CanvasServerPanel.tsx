import { useMemo, useRef, useState } from 'react'
import { flushSync } from 'react-dom'
import { PlusIcon, SearchIcon } from 'lucide-react'

import { CountryFlag } from '@/components/CountryFlag'
import { Input } from '@/components/ui/input'
import { cn } from '@/lib/utils'
import type { CatalogProbeRow, Server } from '@/lib/types'

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
}: {
  emptyCanvas: boolean
  servers: Server[]
  probesByServer: Map<number, CatalogProbeRow[]>
  probeTitle: string | null
  usedCount: (serverId: number) => number
  canAddMore: boolean
  onAdd: (server: Server) => void
}) {
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
    <aside className="cw-server-panel" aria-label="服务器列表">
      <div className="cw-server-panel-search">
        <SearchIcon aria-hidden />
        <Input
          value={query}
          onChange={(event) => setQuery(event.target.value)}
          placeholder="搜索服务器"
          aria-label="搜索服务器"
        />
      </div>
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
        {filtered.length === 0 ? <li className="cw-server-panel-empty">没有匹配的服务器</li> : null}
      </ul>
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
    </aside>
  )
}
