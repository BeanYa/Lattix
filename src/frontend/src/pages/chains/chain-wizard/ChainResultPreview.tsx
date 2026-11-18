import { InfoIcon, TriangleAlertIcon } from 'lucide-react'

import type { Server } from '@/lib/types'

import { EXTERNAL_PROTOCOL_LABELS } from '../external-exit'
import type { ChainPreviewPath } from '../use-chain-form'
import { HopLink } from '../ChainHopLink'
import { TrafficSummary } from '../ChainTrafficSummary'
import type { CanvasWarning } from './chain-canvas'
import { chainWarningText } from './chain-warning'

/** 拓扑步骤的校验摘要：硬错误在下面单独列出，这里只列空悬链路并提示下一步可预览结果。 */
export function ChainValidationSummary({
  pathCount,
  warnings,
  serverById,
}: {
  pathCount: number
  warnings: CanvasWarning[]
  serverById: Map<number, Server>
}) {
  return (
    <section className="cw-preview" aria-label="拓扑校验">
      <header className="cw-preview-head">
        <span className="cw-preview-title">拓扑校验</span>
        {pathCount > 0 ? <span className="cw-preview-count">将创建 {pathCount} 条链路</span> : null}
      </header>
      {pathCount === 0 ? (
        <p className="cw-preview-empty">
          尚无可生成的链路：从入网流量连出，并把每条路径的末端连到 Internet 出口。
        </p>
      ) : (
        <p className="cw-preview-empty">
          点「下一步」在协议与细节页查看链路结果预览；端口等配置以画布填写值为准。
        </p>
      )}
      {warnings.length > 0 ? (
        <ul className="cw-preview-warnings" aria-label="不会生成的链路">
          {warnings.map((warning, index) => (
            <li key={(warning.nodeId ?? 'warning') + '-' + index}>
              <TriangleAlertIcon aria-hidden />
              {chainWarningText(warning, serverById)}
            </li>
          ))}
        </ul>
      ) : null}
    </section>
  )
}

const ROLE_LABEL: Record<'entry' | 'middle' | 'exit', string> = {
  entry: '入口',
  middle: '中转',
  exit: '出口',
}

/** 链路结果预览：一条画布路径 = 一条链路，按链路详情（cg-chain-entry）的列表样式展示
 * 将创建/编辑后的结果。端口等配置显示编辑器中所见值，自动端口在部署后才生成。 */
export function ChainResultPreview({
  chains,
  names,
  warnings,
  serverById,
  mode,
  entryShared,
  trafficMultiplier,
  autoEnabled = false,
}: {
  chains: ChainPreviewPath[]
  names: string[]
  warnings: CanvasWarning[]
  serverById: Map<number, Server>
  mode: 'create' | 'edit'
  entryShared: boolean
  trafficMultiplier: string
  autoEnabled?: boolean
}) {
  return (
    <section className="cw-result" aria-label="链路结果预览">
      <header className="cw-result-head">
        <span className="cw-result-title">结果预览</span>
        <span className="cw-result-count">
          {mode === 'edit' ? '编辑后生效 1 条链路' : `将创建 ${chains.length} 条链路`}
        </span>
        <span className="cw-result-note">
          端口按编辑器填写值显示；自动分配的端口与真实生效参数在部署后生成。
        </span>
      </header>
      <div className="cg-chain-rows cw-result-rows">
        {chains.map((chain, index) => {
          const lineIdx = index % 9
          const direct = chain.hops.length === 1
          const externalProtocol = chain.externalExit
            ? (EXTERNAL_PROTOCOL_LABELS[chain.externalExit.protocol] ?? chain.externalExit.protocol)
            : ''
          return (
            <article
              key={chain.key}
              className="cg-chain-entry"
              data-tone="is-blue"
              style={
                {
                  '--entry-line-color': `var(--panel-line-${lineIdx})`,
                } as React.CSSProperties
              }
            >
              <header className="cg-chain-entry-head">
                <div className="cg-chain-entry-title">
                  <span
                    className="cg-line-badge"
                    style={{ ['--line-color' as string]: `var(--panel-line-${lineIdx})` }}
                  >
                    L{lineIdx}
                  </span>
                  <strong className="cg-chain-name">{names[index] ?? `链路 ${index + 1}`}</strong>
                  <span className="cg-chain-tag">{direct ? '直连' : '中转'}</span>
                  {autoEnabled ? (
                    <span className="cg-chain-tag is-external" title="已开启自动编排容灾">
                      自动编排
                    </span>
                  ) : null}
                  {chain.externalExit ? (
                    <>
                      <span className="cg-chain-tag is-external" title="出口为未托管外部直链节点">
                        外部出口
                      </span>
                      <span className="cg-chain-external-proto" title="外部出口协议">
                        {externalProtocol}
                      </span>
                    </>
                  ) : null}
                  <span className="cg-status is-blue">{mode === 'edit' ? '待更新' : '待创建'}</span>
                </div>
                <div className="cg-chain-entry-side">
                  <span className="cg-chain-meta">
                    <InfoIcon />
                    尚未部署：以下为编辑器中的配置
                  </span>
                </div>
              </header>
              <div className="cg-chain-entry-body">
                <div className="cg-chain-split">
                  <div className="cg-hop-scroll">
                    <div className="cg-hop-flow">
                      {chain.hops.map((hop, hopIndex) => (
                        <div
                          key={hop.server.id}
                          className={hopIndex > 0 ? 'cg-hop-seg has-link' : 'cg-hop-seg'}
                        >
                          {hopIndex > 0 ? <HopLink segment={null} flowing={false} /> : null}
                          <div className="cg-hop">
                            <span className="cg-hop-head">
                              <span className="cg-hop-num">
                                {String(hopIndex + 1).padStart(2, '0')}
                              </span>
                              <span className="cg-hop-dot is-blue" />
                              <span className="cg-hop-role">
                                {direct ? '直连' : ROLE_LABEL[hop.role]} · 待部署
                              </span>
                            </span>
                            <strong className="cg-hop-name">{hop.server.alias}</strong>
                            <span className="cg-hop-meta">
                              {hop.external ? (
                                <>
                                  <span>外部节点</span>
                                  <span>端口 {hop.port ?? '来自链接'}</span>
                                </>
                              ) : (
                                <span>{hop.port ? `端口 ${hop.port}` : '端口 自动分配'}</span>
                              )}
                              {(hop.role === 'entry' || direct) && entryShared ? (
                                <span className="cg-hop-shared">共享入口</span>
                              ) : null}
                              <span>未部署</span>
                            </span>
                          </div>
                        </div>
                      ))}
                    </div>
                  </div>
                  <div className="cw-result-side">
                    <TrafficSummary today={0} multiplier={trafficMultiplier || '1.000'} />
                    <p className="cg-chain-hint">
                      订阅条目与流量统计在链路部署完成后生成；入口为用户可见的接入点。
                    </p>
                  </div>
                </div>
              </div>
            </article>
          )
        })}
      </div>
      {warnings.length > 0 ? (
        <ul className="cw-preview-warnings cw-result-warnings" aria-label="不会生成的链路">
          {warnings.map((warning, index) => (
            <li key={(warning.nodeId ?? 'warning') + '-' + index}>
              <TriangleAlertIcon aria-hidden />
              {chainWarningText(warning, serverById)}
            </li>
          ))}
        </ul>
      ) : null}
    </section>
  )
}
