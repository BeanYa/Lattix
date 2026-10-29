import { Fragment } from 'react'
import { ArrowLeftIcon, GlobeIcon } from 'lucide-react'

import { NameTemplateInput } from '@/components/NameTemplateInput'
import { RealityDestPicker } from '@/components/RealityDestPicker'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from '@/components/ui/select'
import { addressFamily } from '@/lib/address'
import { cn } from '@/lib/utils'
import { xrayVersionKnownBelow } from '@/lib/xray-version'
import type { Server } from '@/lib/types'

import {
  DIRECT_PROTOCOLS,
  FINGERPRINTS,
  FLOWS,
  NETWORKS,
  PROTOCOL_LABELS,
  RELAY_PROTOCOLS,
  SS_METHODS,
  VLESS_ENCS,
  VMESS_CIPHERS,
  XHTTP_MODES,
  isPlainNetwork,
  securityOptions,
  type ChainFormController,
} from '../use-chain-form'

/** 拓扑摘要：画布推导的入口 → 中转 → 出口 → Internet 只读回顾。 */
function TopologySummary({
  servers,
  chainType,
  entryPort,
  exitPort,
}: {
  servers: Server[]
  chainType: 'direct' | 'relay'
  entryPort: string
  exitPort: string
}) {
  return (
    <div className="cw-summary" aria-label="链路拓扑摘要">
      <span className="cw-summary-entry">客户端</span>
      <span className="cw-summary-arrow">→</span>
      {servers.map((server, index) => (
        <Fragment key={server.id}>
          <span className="cw-summary-hop">
            {server.alias}
            {index === 0 && entryPort ? (
              <span className="cw-summary-port">:{entryPort}</span>
            ) : null}
            {chainType === 'relay' && index === servers.length - 1 && exitPort ? (
              <span className="cw-summary-port">:{exitPort}</span>
            ) : null}
          </span>
          <span className="cw-summary-arrow">→</span>
        </Fragment>
      ))}
      <span className="cw-summary-internet">
        <GlobeIcon aria-hidden />
        Internet
      </span>
    </div>
  )
}

/** 向导第二步：协议与细节表单（拓扑已在第一步画布确定并校验）。 */
export function ChainDetailsStep({
  controller,
  panelShort,
  chainType,
  entryPort,
  exitPort,
  onBack,
}: {
  controller: ChainFormController
  panelShort: string
  chainType: 'direct' | 'relay'
  entryPort: string
  exitPort: string
  onBack: () => void
}) {
  const {
    editingChainId,
    creating,
    createError,
    form,
    patch,
    isReality,
    isHy2,
    entryPortHint,
    entryBlockLocked,
    strictNameResult,
    onNetworkChange,
    onSecurityChange,
    onProtocolChange,
    onSubmit,
  } = controller
  const plainNetwork = isPlainNetwork(form.network)
  // 落地服务器（ACME 域名检测对象，§4）：直连=唯一服务器，中转=出口服务器。
  const landingServer =
    chainType === 'direct' ? controller.topologyServers[0] : controller.topologyServers.at(-1)
  // 落地服务器生效 xray 版本：settings 覆盖优先（空 = 未设置），回退 agent 上报版本。
  const landingXrayVersion =
    landingServer?.effective_xray_version || landingServer?.xray_version || ''
  const landingDomain = landingServer?.addresses.find((a) => addressFamily(a) === 'domain')
  return (
    <div className="cw-details">
      <TopologySummary
        servers={controller.topologyServers}
        chainType={chainType}
        entryPort={entryPort}
        exitPort={exitPort}
      />
      <form onSubmit={onSubmit} className="space-y-4">
        <div className="space-y-2">
          <Label htmlFor="chain-name-template">链路名称模板</Label>
          <NameTemplateInput
            id="chain-name-template"
            value={form.name}
            onChange={(value) => patch({ name: value })}
            context={{
              servers: controller.topologyServers,
              protocol: form.protocol,
              port: entryPort,
              hopIndexes: controller.hopIndexes,
              panelShort,
            }}
            allowEmpty
            placeholder="留空自动生成 Chain #xxxx"
            emptyHint="留空将在创建时自动生成 Chain #xxxx（4 位随机大小写字母）"
          />
          <p className="cg-chain-hint">
            输入 {'{{'} 后可选择变量；中转节点显示为 HOP_1/HOP_2，对应模板中的 HOP[1]/HOP[2]。
          </p>
        </div>

        <div className="space-y-2">
          <Label>{chainType === 'direct' ? '协议' : '出口协议'}</Label>
          <Select value={form.protocol} onValueChange={onProtocolChange}>
            <SelectTrigger className="w-full">
              <SelectValue />
            </SelectTrigger>
            <SelectContent>
              {(chainType === 'direct' ? DIRECT_PROTOCOLS : RELAY_PROTOCOLS).map((p) => {
                // hy2 需要 xray ≥ 26.3.27（后端 shared.XrayMinVersionHy2）：落地服务器版本已知且
                // 过低才禁用，未上报不拦（agent 部署时终审）。
                const hy2Blocked = p === 'hysteria' && xrayVersionKnownBelow(landingXrayVersion)
                return (
                  <SelectItem key={p} value={p} disabled={hy2Blocked}>
                    {PROTOCOL_LABELS[p] ?? p}
                    {hy2Blocked ? '（节点 xray 版本过低，请先升级）' : ''}
                  </SelectItem>
                )
              })}
            </SelectContent>
          </Select>
        </div>

        {chainType === 'relay' && (form.protocol === 'hysteria' || form.protocol === 'vless') && (
          <div className="space-y-2">
            <label
              className={cn(
                'cg-chain-type',
                form.entryProtocolEnabled && 'is-selected',
                entryBlockLocked && 'opacity-50 pointer-events-none',
              )}
            >
              <input
                type="checkbox"
                className="sr-only"
                checked={form.entryProtocolEnabled}
                disabled={entryBlockLocked}
                onChange={(e) => patch({ entryProtocolEnabled: e.target.checked })}
              />
              使用独立入口协议（VLESS + Reality）
            </label>
            {entryBlockLocked && (
              <p className="cg-chain-hint">存量链路不能新增入口协议区块，请新建链路。</p>
            )}
            {form.protocol === 'hysteria' && !form.entryProtocolEnabled && (
              <p className="cg-chain-hint">
                推荐：勾选后 UDP 只在服务器间流动，规避运营商 UDP QoS。
              </p>
            )}
            {form.entryProtocolEnabled && (
              <>
                <p className="cg-chain-hint">
                  入口以 VLESS+Reality 终结客户端流量后按出口协议转发；客户端仅见入口协议参数。
                  以下参数全部可留空自动生成。
                </p>
                <div className="space-y-2">
                  <Label htmlFor="entryShortId">入口 short_id（可空）</Label>
                  <Input
                    id="entryShortId"
                    value={form.entryShortId}
                    onChange={(e) => patch({ entryShortId: e.target.value })}
                    placeholder="留空自动生成"
                  />
                </div>
                <div className="space-y-2">
                  <Label htmlFor="entryDest">入口 dest（可空）</Label>
                  <Input
                    id="entryDest"
                    value={form.entryDest}
                    onChange={(e) => patch({ entryDest: e.target.value })}
                    placeholder="dl.google.com:443"
                  />
                </div>
                <div className="space-y-2">
                  <Label htmlFor="entryServerNames">入口 server_names（逗号分隔，可空）</Label>
                  <Input
                    id="entryServerNames"
                    value={form.entryServerNames}
                    onChange={(e) => patch({ entryServerNames: e.target.value })}
                    placeholder="dl.google.com"
                  />
                </div>
              </>
            )}
          </div>
        )}
        {entryPortHint ? <p className="cg-chain-hint">{entryPortHint}</p> : null}

        {isReality && (
          <>
            <div className="space-y-2">
              <Label>传输（network）</Label>
              <Select value={form.network} onValueChange={onNetworkChange}>
                <SelectTrigger className="w-full">
                  <SelectValue />
                </SelectTrigger>
                <SelectContent>
                  {NETWORKS.map((n) => (
                    <SelectItem key={n} value={n}>
                      {n}
                    </SelectItem>
                  ))}
                </SelectContent>
              </Select>
            </div>
            <div className="space-y-2">
              <Label>安全层（security）</Label>
              <Select
                value={form.security}
                onValueChange={onSecurityChange}
                items={securityOptions(form.protocol, form.network).map((s) => ({
                  value: s,
                  label:
                    s === 'reality'
                      ? 'Reality（推荐 · 抗封锁最强）'
                      : s === 'tls'
                        ? 'TLS（证书：自签伪装 / ACME 真实域名）'
                        : 'none（明文 · 仅适合套 CDN）',
                }))}
              >
                <SelectTrigger className="w-full">
                  <SelectValue />
                </SelectTrigger>
                <SelectContent>
                  {securityOptions(form.protocol, form.network).map((s) => (
                    <SelectItem key={s} value={s}>
                      {s === 'reality'
                        ? 'Reality（推荐 · 抗封锁最强）'
                        : s === 'tls'
                          ? 'TLS（证书：自签伪装 / ACME 真实域名）'
                          : 'none（明文 · 仅适合套 CDN）'}
                    </SelectItem>
                  ))}
                </SelectContent>
              </Select>
            </div>
            {form.network === 'xhttp' && (
              <>
                <div className="space-y-2">
                  <Label htmlFor="xhttpPath">XHTTP path</Label>
                  <Input
                    id="xhttpPath"
                    value={form.path}
                    onChange={(e) => patch({ path: e.target.value })}
                    placeholder="/"
                  />
                </div>
                <div className="space-y-2">
                  <Label>XHTTP mode</Label>
                  <Select value={form.mode} onValueChange={(v) => v && patch({ mode: v })}>
                    <SelectTrigger className="w-full">
                      <SelectValue />
                    </SelectTrigger>
                    <SelectContent>
                      {XHTTP_MODES.map((m) => (
                        <SelectItem key={m} value={m}>
                          {m}
                        </SelectItem>
                      ))}
                    </SelectContent>
                  </Select>
                </div>
                <div className="space-y-2">
                  <Label htmlFor="xhttpHost">XHTTP host（可空）</Label>
                  <Input
                    id="xhttpHost"
                    value={form.host}
                    onChange={(e) => patch({ host: e.target.value })}
                    placeholder="留空不设置"
                  />
                </div>
              </>
            )}
            {form.network === 'grpc' && (
              <div className="space-y-2">
                <Label htmlFor="grpcServiceName">gRPC serviceName</Label>
                <Input
                  id="grpcServiceName"
                  value={form.serviceName}
                  onChange={(e) => patch({ serviceName: e.target.value })}
                  placeholder="grpc"
                />
              </div>
            )}
            {plainNetwork && (
              <>
                <div className="space-y-2">
                  <Label htmlFor="plainPath">
                    {form.network === 'ws' ? 'WS path' : 'HTTPUpgrade path'}
                  </Label>
                  <Input
                    id="plainPath"
                    value={form.path}
                    onChange={(e) => patch({ path: e.target.value })}
                    placeholder="/"
                  />
                </div>
                <div className="space-y-2">
                  <Label htmlFor="plainHost">
                    {form.network === 'ws' ? 'WS host（可空）' : 'HTTPUpgrade host（可空）'}
                  </Label>
                  <Input
                    id="plainHost"
                    value={form.host}
                    onChange={(e) => patch({ host: e.target.value })}
                    placeholder="留空不设置"
                  />
                </div>
                <p className="cg-chain-hint">
                  ws/httpupgrade 支持 none（明文 · 套 CDN）与 tls（证书）安全层； vless 选 none
                  时需启用 VLESS Encryption。
                </p>
              </>
            )}
            {form.protocol === 'vless' && (
              <div className="space-y-2">
                <Label>VLESS Encryption（可与 flow 组合）</Label>
                <Select
                  value={form.encryption}
                  onValueChange={(v) => v !== null && patch({ encryption: v })}
                  items={VLESS_ENCS}
                >
                  <SelectTrigger className="w-full">
                    <SelectValue />
                  </SelectTrigger>
                  <SelectContent>
                    {VLESS_ENCS.map((e) => (
                      <SelectItem key={e.value} value={e.value}>
                        {e.label}
                      </SelectItem>
                    ))}
                  </SelectContent>
                </Select>
              </div>
            )}
            {form.protocol === 'vless' && form.network === 'tcp' && (
              <div className="space-y-2">
                <Label>flow</Label>
                <Select
                  value={form.flow}
                  onValueChange={(v) => v && patch({ flow: v })}
                  items={FLOWS.map((f) => ({ value: f, label: f === 'none' ? '无' : f }))}
                >
                  <SelectTrigger className="w-full">
                    <SelectValue />
                  </SelectTrigger>
                  <SelectContent>
                    {FLOWS.map((f) => (
                      <SelectItem key={f} value={f}>
                        {f === 'none' ? '无' : f}
                      </SelectItem>
                    ))}
                  </SelectContent>
                </Select>
              </div>
            )}
            {form.security !== 'none' && (
              <div className="space-y-2">
                <Label>uTLS 指纹（客户端）</Label>
                <Select
                  value={form.fingerprint}
                  onValueChange={(v) => v && patch({ fingerprint: v })}
                >
                  <SelectTrigger className="w-full">
                    <SelectValue />
                  </SelectTrigger>
                  <SelectContent>
                    {FINGERPRINTS.map((f) => (
                      <SelectItem key={f} value={f}>
                        {f}
                      </SelectItem>
                    ))}
                  </SelectContent>
                </Select>
              </div>
            )}
            {form.security === 'reality' && (
              <div className="space-y-2">
                <Label htmlFor="shortId">short_id</Label>
                <Input
                  id="shortId"
                  value={form.shortId}
                  onChange={(e) => patch({ shortId: e.target.value })}
                  placeholder="留空随机生成"
                />
              </div>
            )}
            {form.security === 'reality' && (
              <RealityDestPicker
                idPrefix="chain"
                preset={form.destPreset}
                onPresetChange={(value) => patch({ destPreset: value })}
                dest={form.dest}
                onDestChange={(value) => patch({ dest: value })}
                serverNames={form.serverNames}
                onServerNamesChange={(value) => patch({ serverNames: value })}
              />
            )}
          </>
        )}

        {((isReality && form.security === 'tls') || isHy2) && (
          <div className="space-y-2">
            <Label id="cert-mode-label">证书模式</Label>
            <div
              role="radiogroup"
              aria-labelledby="cert-mode-label"
              className="grid grid-cols-2 gap-2"
            >
              <label className={cn('cg-chain-type', form.certMode === 'selfsign' && 'is-selected')}>
                <input
                  type="radio"
                  name="cert-mode"
                  value="selfsign"
                  checked={form.certMode === 'selfsign'}
                  onChange={() => patch({ certMode: 'selfsign' })}
                  className="sr-only"
                />
                伪装域名自签
              </label>
              <label
                className={cn(
                  'cg-chain-type',
                  form.certMode === 'acme' && 'is-selected',
                  !landingDomain && 'opacity-50 pointer-events-none',
                )}
              >
                <input
                  type="radio"
                  name="cert-mode"
                  value="acme"
                  checked={form.certMode === 'acme'}
                  disabled={!landingDomain}
                  onChange={() => patch({ certMode: 'acme' })}
                  className="sr-only"
                />
                使用落地服务器域名（ACME）
              </label>
            </div>
            {form.certMode === 'selfsign' ? (
              <>
                <Label htmlFor="tlsDomain">伪装域名（可空）</Label>
                <Input
                  id="tlsDomain"
                  value={form.tlsDomain}
                  onChange={(e) => patch({ tlsDomain: e.target.value })}
                  placeholder="留空从常见域名预设池随机选取"
                />
                <p className="cg-chain-hint">
                  仅作 TLS 伪装身份（证书 CN/SAN 与客户端 SNI），不要求指向本机；
                  订阅以证书指纹（pin）校验，客户端无需信任系统 CA。
                </p>
              </>
            ) : landingDomain ? (
              <p className="cg-chain-hint">
                {`将沿用落地服务器域名 ${landingDomain}，由节点自动安装 acme.sh 签发并续期（需域名解析指向本机、80 端口空闲）。`}
              </p>
            ) : null}
            {!landingDomain && (
              <p className="cg-chain-hint">
                落地服务器未设置域名，请先在服务器地址中配置域名或改用自签模式。
              </p>
            )}
          </div>
        )}

        {isHy2 && (
          <>
            <p className="cg-chain-hint">
              hy2 全程走 UDP，需服务商放行 UDP；UDP 受限时改用 VLESS。
            </p>
            <div className="space-y-2">
              <Label htmlFor="obfsPassword">混淆密码（salamander，留空自动生成）</Label>
              <Input
                id="obfsPassword"
                value={form.obfsPassword}
                onChange={(e) => patch({ obfsPassword: e.target.value })}
                placeholder="留空自动生成"
              />
            </div>
            <div className="grid grid-cols-2 gap-2">
              <div className="space-y-2">
                <Label htmlFor="upMbps">上行带宽（Mbps）</Label>
                <Input
                  id="upMbps"
                  type="number"
                  min={0}
                  value={form.upMbps}
                  onChange={(e) => patch({ upMbps: e.target.value })}
                />
              </div>
              <div className="space-y-2">
                <Label htmlFor="downMbps">下行带宽（Mbps）</Label>
                <Input
                  id="downMbps"
                  type="number"
                  min={0}
                  value={form.downMbps}
                  onChange={(e) => patch({ downMbps: e.target.value })}
                />
              </div>
            </div>
            <div className="space-y-2">
              <Label htmlFor="portHop">端口跳跃段（留空自动分配 32 段；off 关闭）</Label>
              <Input
                id="portHop"
                value={form.portHop}
                onChange={(e) => patch({ portHop: e.target.value })}
                placeholder="如 20000-20031；NAT 公共端口不足时自动关闭并降级"
              />
              <p className="cg-chain-hint">
                客户端在段内逐包换端口以抗 QoS；公共端口不足 8 个的机器自动退回固定单端口。
              </p>
            </div>
          </>
        )}

        {form.protocol === 'shadowsocks' && (
          <div className="space-y-2">
            <Label>加密方式（method）</Label>
            <Select
              value={form.method}
              onValueChange={(v) => v && patch({ method: v })}
              items={SS_METHODS}
            >
              <SelectTrigger className="w-full">
                <SelectValue />
              </SelectTrigger>
              <SelectContent>
                {SS_METHODS.map((m) => (
                  <SelectItem key={m.value} value={m.value}>
                    {m.label}
                  </SelectItem>
                ))}
              </SelectContent>
            </Select>
          </div>
        )}
        {form.protocol === 'vmess' && (
          <div className="space-y-2">
            <Label>加密方式（cipher）</Label>
            <Select
              value={form.cipher}
              onValueChange={(v) => v && patch({ cipher: v })}
              items={VMESS_CIPHERS}
            >
              <SelectTrigger className="w-full">
                <SelectValue />
              </SelectTrigger>
              <SelectContent>
                {VMESS_CIPHERS.map((c) => (
                  <SelectItem key={c.value} value={c.value}>
                    {c.label}
                  </SelectItem>
                ))}
              </SelectContent>
            </Select>
          </div>
        )}

        {chainType === 'direct' && form.protocol === 'dokodemo-door' ? (
          <>
            <div className="space-y-2">
              <Label htmlFor="targetAddress">目标地址</Label>
              <Input
                id="targetAddress"
                value={form.targetAddress}
                onChange={(event) => patch({ targetAddress: event.target.value })}
                placeholder="例如：10.0.0.2 或 internal.example.com"
              />
            </div>
            <div className="space-y-2">
              <Label htmlFor="targetPort">目标端口</Label>
              <Input
                id="targetPort"
                type="number"
                min={1}
                max={65535}
                value={form.targetPort}
                onChange={(event) => patch({ targetPort: event.target.value })}
                placeholder="转发目的地端口"
              />
            </div>
          </>
        ) : null}

        <div className="space-y-2">
          <Label htmlFor="chain-traffic-multiplier">流量倍率</Label>
          <Input
            id="chain-traffic-multiplier"
            type="number"
            min="0.001"
            max="1000"
            step="0.001"
            value={form.trafficMultiplier}
            onChange={(event) => patch({ trafficMultiplier: event.target.value })}
            required
          />
        </div>

        {createError && <p className="cg-chain-error">{createError}</p>}
        <div className="flex items-center justify-end gap-2">
          <Button type="button" variant="outline" onClick={onBack}>
            <ArrowLeftIcon />
            上一步（拓扑）
          </Button>
          <Button
            type="submit"
            disabled={creating || Boolean(form.name.trim() && strictNameResult.error)}
          >
            {creating
              ? editingChainId === null
                ? '创建中…'
                : '保存中…'
              : editingChainId === null
                ? '创建'
                : '保存修改'}
          </Button>
        </div>
      </form>
    </div>
  )
}
