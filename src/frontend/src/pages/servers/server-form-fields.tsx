import { Building2Icon, PlusIcon, XIcon } from 'lucide-react'

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
import { Separator } from '@/components/ui/separator'
import { CURRENCIES } from '@/lib/format'
import type { IntervalUnit, Provider, TrafficAccountingMode } from '@/lib/types'

import {
  addInterval,
  localDate,
  type BillingFormState,
  type TrafficFormState,
} from './server-form-utils'

/** 流量额度 + 计费分区字段块，创建/编辑服务器对话框共用。 */
export function BillingTrafficFields({
  billing,
  setBilling,
  traffic,
  setTraffic,
  providers,
  onManageProviders,
}: {
  billing: BillingFormState
  setBilling: (value: BillingFormState) => void
  traffic: TrafficFormState
  setTraffic: (value: TrafficFormState) => void
  providers: Provider[]
  onManageProviders: () => void
}) {
  return (
    <div className="space-y-5">
      <Separator />
      <section className="space-y-3">
        <div>
          <h3 className="text-sm font-medium">流量额度</h3>
          <p className="text-xs text-muted-foreground">
            十进制换算：1 GB = 10^9 bytes，1 TB = 1000 GB
          </p>
        </div>
        <label className="flex items-center gap-2 text-sm">
          <input
            type="checkbox"
            checked={traffic.limited}
            onChange={(e) => setTraffic({ ...traffic, limited: e.target.checked })}
          />
          有限额度
        </label>
        {traffic.limited ? (
          <div className="grid grid-cols-[1fr_110px] gap-2">
            <Input
              type="number"
              min="0.01"
              step="0.01"
              value={traffic.quota}
              onChange={(e) => setTraffic({ ...traffic, quota: e.target.value })}
            />
            <Select
              value={traffic.quotaUnit}
              onValueChange={(v) => v && setTraffic({ ...traffic, quotaUnit: v as 'GB' | 'TB' })}
              items={[
                { value: 'GB', label: 'GB' },
                { value: 'TB', label: 'TB' },
              ]}
            >
              <SelectTrigger>
                <SelectValue />
              </SelectTrigger>
              <SelectContent>
                <SelectItem value="GB">GB</SelectItem>
                <SelectItem value="TB">TB</SelectItem>
              </SelectContent>
            </Select>
          </div>
        ) : null}
        <div className="grid gap-3 sm:grid-cols-2">
          <div className="space-y-2">
            <Label>计流方式</Label>
            <Select
              value={traffic.accountingMode}
              onValueChange={(v) =>
                v && setTraffic({ ...traffic, accountingMode: v as TrafficAccountingMode })
              }
              items={[
                { value: 'outbound', label: '仅出站' },
                { value: 'bidirectional', label: '入站 + 出站' },
                { value: 'max', label: '取较大方向' },
              ]}
            >
              <SelectTrigger>
                <SelectValue />
              </SelectTrigger>
              <SelectContent>
                <SelectItem value="outbound">仅出站</SelectItem>
                <SelectItem value="bidirectional">入站 + 出站</SelectItem>
                <SelectItem value="max">取较大方向</SelectItem>
              </SelectContent>
            </Select>
          </div>
          <div className="space-y-2">
            <Label>重置锚点</Label>
            <Input
              type="date"
              value={traffic.anchorOn}
              onChange={(e) => setTraffic({ ...traffic, anchorOn: e.target.value })}
            />
          </div>
        </div>
      </section>
      <Separator />
      <section className="space-y-3">
        <label className="flex items-center gap-2 text-sm font-medium">
          <input
            type="checkbox"
            checked={billing.enabled}
            onChange={(e) => setBilling({ ...billing, enabled: e.target.checked })}
          />
          统计计费
        </label>
        {billing.enabled ? (
          <>
            <div className="grid grid-cols-[1fr_auto] items-end gap-2">
              <div className="space-y-2">
                <Label>服务商</Label>
                <Select
                  value={billing.providerId}
                  onValueChange={(v) => v && setBilling({ ...billing, providerId: v })}
                  items={providers.map((p) => ({ value: String(p.id), label: p.name }))}
                >
                  <SelectTrigger>
                    <SelectValue placeholder="选择服务商" />
                  </SelectTrigger>
                  <SelectContent>
                    {providers.length === 0 ? (
                      <p className="px-3 py-6 text-center text-sm text-muted-foreground">
                        暂无服务商，请先添加
                      </p>
                    ) : (
                      providers.map((p) => (
                        <SelectItem key={p.id} value={String(p.id)}>
                          {p.name}
                        </SelectItem>
                      ))
                    )}
                  </SelectContent>
                </Select>
              </div>
              <Button
                type="button"
                variant="outline"
                size="icon"
                title="管理服务商"
                onClick={onManageProviders}
              >
                <Building2Icon />
              </Button>
            </div>
            <div className="grid grid-cols-[1fr_110px] gap-2">
              <div className="space-y-2">
                <Label>每周期实付金额</Label>
                <Input
                  type="number"
                  min="0"
                  step="0.01"
                  value={billing.amount}
                  onChange={(e) => setBilling({ ...billing, amount: e.target.value })}
                />
              </div>
              <div className="space-y-2">
                <Label>币种</Label>
                <Select
                  value={billing.currency}
                  onValueChange={(v) => v && setBilling({ ...billing, currency: v })}
                  items={CURRENCIES.map((c) => ({ value: c, label: c }))}
                >
                  <SelectTrigger>
                    <SelectValue />
                  </SelectTrigger>
                  <SelectContent>
                    {CURRENCIES.map((c) => (
                      <SelectItem key={c} value={c}>
                        {c}
                      </SelectItem>
                    ))}
                  </SelectContent>
                </Select>
              </div>
            </div>
            <div className="grid gap-3 sm:grid-cols-2">
              <div className="space-y-2">
                <Label>开通日期</Label>
                <Input
                  type="date"
                  max={localDate()}
                  value={billing.startedOn}
                  onChange={(e) =>
                    setBilling({
                      ...billing,
                      startedOn: e.target.value,
                      renewalOn: addInterval(
                        e.target.value,
                        billing.intervalCount,
                        billing.intervalUnit,
                      ),
                    })
                  }
                />
              </div>
              <div className="space-y-2">
                <Label>下次续费日</Label>
                <Input
                  type="date"
                  value={billing.renewalOn}
                  onChange={(e) => setBilling({ ...billing, renewalOn: e.target.value })}
                />
              </div>
            </div>
            <div className="grid grid-cols-[1fr_140px] gap-2">
              <div className="space-y-2">
                <Label>计费周期</Label>
                <Input
                  type="number"
                  min="1"
                  value={billing.intervalCount}
                  onChange={(e) =>
                    setBilling({ ...billing, intervalCount: Number(e.target.value) })
                  }
                />
              </div>
              <div className="space-y-2">
                <Label>单位</Label>
                <Select
                  value={billing.intervalUnit}
                  onValueChange={(v) =>
                    v && setBilling({ ...billing, intervalUnit: v as IntervalUnit })
                  }
                  items={[
                    { value: 'day', label: '天' },
                    { value: 'month', label: '月' },
                    { value: 'year', label: '年' },
                  ]}
                >
                  <SelectTrigger>
                    <SelectValue />
                  </SelectTrigger>
                  <SelectContent>
                    <SelectItem value="day">天</SelectItem>
                    <SelectItem value="month">月</SelectItem>
                    <SelectItem value="year">年</SelectItem>
                  </SelectContent>
                </Select>
              </div>
            </div>
          </>
        ) : null}
      </section>
    </div>
  )
}

// 可用端口动态行（§21）：每行一个文本输入，支持 10000 / 10001-10010 / 20001-20010:10001-10010。
export function PortRowsEditor({
  rows,
  onChange,
}: {
  rows: string[]
  onChange: (rows: string[]) => void
}) {
  const setRow = (i: number, value: string) => {
    const next = rows.slice()
    next[i] = value
    onChange(next)
  }
  return (
    <div className="space-y-2">
      {rows.map((row, i) => (
        <div key={i} className="flex items-center gap-2">
          <Input
            value={row}
            onChange={(e) => setRow(i, e.target.value)}
            placeholder="10000 或 10001-10010 或 20001-20010:10001-10010"
          />
          <Button
            type="button"
            variant="outline"
            size="sm"
            disabled={rows.length <= 1}
            onClick={() => onChange(rows.filter((_, j) => j !== i))}
          >
            <XIcon />
          </Button>
        </div>
      ))}
      <Button type="button" variant="outline" size="sm" onClick={() => onChange([...rows, ''])}>
        <PlusIcon />
        添加端口段
      </Button>
    </div>
  )
}
