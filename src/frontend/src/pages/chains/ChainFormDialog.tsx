import { useState } from 'react'
import { PlusIcon, XIcon } from 'lucide-react'

import { NameTemplateInput } from '@/components/NameTemplateInput'
import { RealityDestPicker } from '@/components/RealityDestPicker'
import { Button } from '@/components/ui/button'
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from '@/components/ui/dialog'
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
import { isServerOnline } from '@/lib/server-state'
import { cn } from '@/lib/utils'
import type { Server } from '@/lib/types'

import {
  DIRECT_PROTOCOLS,
  FINGERPRINTS,
  FLOWS,
  NETWORKS,
  RELAY_PROTOCOLS,
  VLESS_ENCS,
  XHTTP_MODES,
  inboundCapable,
  type ChainFormController,
} from './use-chain-form'

function serverLabel(s: Server): string {
  const tags: string[] = []
  if (!isServerOnline(s)) {
    tags.push('离线')
  }
  if (!inboundCapable(s)) {
    tags.push('仅出口')
  }
  return tags.length > 0 ? `${s.alias}（${tags.join('，')}）` : s.alias
}

// 逐跳公网地址选择（§9）：候选 = 服务器 addresses（空则回退默认/学习地址）；空值 = 跟随服务器默认地址。
// 服务器同时有 IPv4/IPv6 字面量条目时提供族切换（域名条目两组均显示），切换后自动选中该族第一个地址。
function HopAddressField({
  server,
  value,
  onChange,
}: {
  server: Server | undefined
  value: string
  onChange: (addr: string) => void
}) {
  const [family, setFamily] = useState<'ipv4' | 'ipv6'>(() =>
    value && addressFamily(value) === 'ipv6' ? 'ipv6' : 'ipv4',
  )
  if (!server) {
    return null
  }
  const candidates =
    server.addresses.length > 0
      ? server.addresses
      : [...new Set([server.address, server.learned_addr].filter(Boolean))]
  if (candidates.length === 0) {
    return null
  }
  const hasV4 = candidates.some((a) => addressFamily(a) === 'ipv4')
  const hasV6 = candidates.some((a) => addressFamily(a) === 'ipv6')
  const showFamilySwitch = hasV4 && hasV6
  const invalid = value !== '' && !candidates.includes(value)
  const visible = candidates.filter((a) => {
    const f = addressFamily(a)
    return !showFamilySwitch || f === 'domain' || f === family
  })
  const items = [
    { value: '', label: '跟随服务器默认地址' },
    ...visible.map((a) => ({ value: a, label: a })),
    ...(invalid ? [{ value, label: `${value}（已失效，将回退默认地址）` }] : []),
  ]
  const switchFamily = (next: 'ipv4' | 'ipv6') => {
    setFamily(next)
    const first = candidates.find((a) => addressFamily(a) === next)
    if (first) {
      onChange(first)
    }
  }
  return (
    <div className="space-y-1.5">
      {showFamilySwitch ? (
        <div className="flex items-center gap-3 text-xs text-muted-foreground">
          <span>公网地址</span>
          {(['ipv4', 'ipv6'] as const).map((f) => (
            <label key={f} className="flex items-center gap-1">
              <input type="radio" checked={family === f} onChange={() => switchFamily(f)} />
              {f === 'ipv4' ? 'IPv4' : 'IPv6'}
            </label>
          ))}
        </div>
      ) : (
        <span className="text-xs text-muted-foreground">公网地址</span>
      )}
      <Select value={value} onValueChange={(v) => onChange(String(v ?? ''))} items={items}>
        <SelectTrigger className="w-full">
          <SelectValue placeholder="跟随服务器默认地址" />
        </SelectTrigger>
        <SelectContent>
          {items.map((item) => (
            <SelectItem key={item.value === '' ? '__default__' : item.value} value={item.value}>
              {item.label}
            </SelectItem>
          ))}
        </SelectContent>
      </Select>
      {invalid ? (
        <p className="text-xs text-destructive">
          所选地址已不在该服务器地址列表中，保存后将回退默认地址
        </p>
      ) : null}
    </div>
  )
}

/** 创建/编辑链路对话框（视图）；状态与提交逻辑在 useChainForm。 */
export function ChainFormDialog({
  controller,
  servers,
  panelShort,
}: {
  controller: ChainFormController
  servers: Server[]
  panelShort: string
}) {
  const {
    open,
    editingChainId,
    creating,
    createError,
    form,
    patch,
    isReality,
    entryPortHint,
    strictNameResult,
    onOpenChange,
    onTypeChange,
    setMiddle,
    setMiddleAddr,
    onSubmit,
  } = controller
  const serverSelectItems = servers.map((s) => ({ value: String(s.id), label: serverLabel(s) }))
  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="max-h-[85vh] overflow-y-auto">
        <DialogHeader>
          <DialogTitle>{editingChainId === null ? '创建链路' : '编辑链路'}</DialogTitle>
          <DialogDescription>
            {editingChainId === null
              ? '直连只包含一台服务器；中转依次选择入口 → 中转（0-2 个）→ 出口，客户端仅见入口。'
              : '修改将按出口到入口依次部署，已发布订阅在新 revision 完成前保持不变。'}
          </DialogDescription>
        </DialogHeader>
        <form onSubmit={onSubmit} className="space-y-4">
          <div className="space-y-2">
            <Label id="chain-type-label">链路类型</Label>
            <div
              role="radiogroup"
              aria-labelledby="chain-type-label"
              className="grid grid-cols-2 gap-2"
            >
              {(
                [
                  ['direct', '直连'],
                  ['relay', '中转'],
                ] as const
              ).map(([value, label]) => (
                <label
                  key={value}
                  className={cn('cg-chain-type', form.chainType === value && 'is-selected')}
                >
                  <input
                    type="radio"
                    name="chain-type"
                    value={value}
                    checked={form.chainType === value}
                    onChange={(event) => onTypeChange(event.target.value)}
                    className="sr-only"
                  />
                  {label}
                </label>
              ))}
            </div>
          </div>
          <div className="space-y-2">
            <Label htmlFor="chain-name-template">链路名称模板</Label>
            <NameTemplateInput
              id="chain-name-template"
              value={form.name}
              onChange={(value) => patch({ name: value })}
              context={{
                servers: controller.topologyServers,
                protocol: form.protocol,
                port: form.entryPort,
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
            <Label>{form.chainType === 'direct' ? '直连服务器' : '入口服务器'}</Label>
            <Select
              value={form.entryId}
              onValueChange={(v) => {
                patch({ entryId: String(v), entryAddr: '' })
              }}
              items={serverSelectItems}
            >
              <SelectTrigger className="w-full">
                <SelectValue placeholder="选择入口服务器" />
              </SelectTrigger>
              <SelectContent>
                {servers.map((s) => (
                  <SelectItem key={s.id} value={String(s.id)}>
                    {serverLabel(s)}
                  </SelectItem>
                ))}
              </SelectContent>
            </Select>
            <HopAddressField
              key={`entry-${form.entryId}`}
              server={servers.find((s) => String(s.id) === form.entryId)}
              value={form.entryAddr}
              onChange={(addr) => patch({ entryAddr: addr })}
            />
          </div>
          {form.chainType === 'relay' ? (
            <>
              <div className="space-y-2">
                <Label>中转服务器（0-2 个）</Label>
                {form.middleIds.map((id, i) => (
                  <div key={i} className="space-y-1.5">
                    <div className="flex items-center gap-2">
                      <Select
                        value={id}
                        onValueChange={(v) => setMiddle(i, String(v))}
                        items={serverSelectItems}
                      >
                        <SelectTrigger className="w-full">
                          <SelectValue placeholder={`中转 ${i + 1}`} />
                        </SelectTrigger>
                        <SelectContent>
                          {servers.map((s) => (
                            <SelectItem key={s.id} value={String(s.id)}>
                              {serverLabel(s)}
                            </SelectItem>
                          ))}
                        </SelectContent>
                      </Select>
                      <Button
                        type="button"
                        variant="outline"
                        size="sm"
                        onClick={() => {
                          patch({
                            middleIds: form.middleIds.filter((_, j) => j !== i),
                            middleAddrs: form.middleAddrs.filter((_, j) => j !== i),
                          })
                        }}
                      >
                        <XIcon />
                      </Button>
                    </div>
                    <HopAddressField
                      key={`middle-${i}-${id}`}
                      server={servers.find((s) => String(s.id) === id)}
                      value={form.middleAddrs[i] ?? ''}
                      onChange={(addr) => setMiddleAddr(i, addr)}
                    />
                  </div>
                ))}
                {form.middleIds.length < 2 && (
                  <Button
                    type="button"
                    variant="outline"
                    size="sm"
                    onClick={() => {
                      patch({
                        middleIds: [...form.middleIds, ''],
                        middleAddrs: [...form.middleAddrs, ''],
                      })
                    }}
                  >
                    <PlusIcon />
                    添加中转
                  </Button>
                )}
              </div>
              <div className="space-y-2">
                <Label>出口服务器</Label>
                <Select
                  value={form.exitId}
                  onValueChange={(v) => {
                    patch({ exitId: String(v), exitAddr: '' })
                  }}
                  items={serverSelectItems}
                >
                  <SelectTrigger className="w-full">
                    <SelectValue placeholder="选择出口服务器" />
                  </SelectTrigger>
                  <SelectContent>
                    {servers.map((s) => (
                      <SelectItem key={s.id} value={String(s.id)}>
                        {serverLabel(s)}
                      </SelectItem>
                    ))}
                  </SelectContent>
                </Select>
                <HopAddressField
                  key={`exit-${form.exitId}`}
                  server={servers.find((s) => String(s.id) === form.exitId)}
                  value={form.exitAddr}
                  onChange={(addr) => patch({ exitAddr: addr })}
                />
              </div>
            </>
          ) : null}
          <div className="space-y-2">
            <Label htmlFor="entryPort">
              {form.chainType === 'direct' ? '业务端口' : '入口端口'}
            </Label>
            <Input
              id="entryPort"
              type="number"
              min={1}
              max={65535}
              value={form.entryPort}
              onChange={(e) => patch({ entryPort: e.target.value })}
              placeholder="留空自动分配（须在服务器可用段内）"
            />
            {entryPortHint ? <p className="cg-chain-hint">{entryPortHint}</p> : null}
          </div>

          <div className="space-y-2">
            <Label>{form.chainType === 'direct' ? '协议' : '出口协议'}</Label>
            <Select value={form.protocol} onValueChange={(v) => v && patch({ protocol: v })}>
              <SelectTrigger className="w-full">
                <SelectValue />
              </SelectTrigger>
              <SelectContent>
                {(form.chainType === 'direct' ? DIRECT_PROTOCOLS : RELAY_PROTOCOLS).map((p) => (
                  <SelectItem key={p} value={p}>
                    {p}
                  </SelectItem>
                ))}
              </SelectContent>
            </Select>
          </div>
          {form.chainType === 'relay' ? (
            <div className="space-y-2">
              <Label htmlFor="exitNodePort">出口节点端口</Label>
              <Input
                id="exitNodePort"
                type="number"
                min={1}
                max={65535}
                value={form.port}
                onChange={(e) => patch({ port: e.target.value })}
                placeholder="留空自动分配"
              />
            </div>
          ) : null}

          {isReality && (
            <>
              <div className="space-y-2">
                <Label>传输（network）</Label>
                <Select value={form.network} onValueChange={(v) => v && patch({ network: v })}>
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
              <div className="space-y-2">
                <Label htmlFor="shortId">short_id</Label>
                <Input
                  id="shortId"
                  value={form.shortId}
                  onChange={(e) => patch({ shortId: e.target.value })}
                  placeholder="留空随机生成"
                />
              </div>
              <RealityDestPicker
                idPrefix="chain"
                preset={form.destPreset}
                onPresetChange={(value) => patch({ destPreset: value })}
                dest={form.dest}
                onDestChange={(value) => patch({ dest: value })}
                serverNames={form.serverNames}
                onServerNamesChange={(value) => patch({ serverNames: value })}
              />
            </>
          )}

          {form.chainType === 'direct' && form.protocol === 'dokodemo-door' ? (
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
          <DialogFooter>
            <Button
              type="submit"
              disabled={
                creating ||
                Boolean(form.name.trim() && strictNameResult.error) ||
                !form.entryId ||
                (form.chainType === 'relay' && !form.exitId)
              }
            >
              {creating
                ? editingChainId === null
                  ? '创建中…'
                  : '保存中…'
                : editingChainId === null
                  ? '创建'
                  : '保存修改'}
            </Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  )
}
