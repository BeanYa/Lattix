import { CoinsIcon, PlusIcon, RefreshCwIcon, SearchIcon, Trash2Icon } from 'lucide-react'

import { LoadingState, Notice } from '@/components/PagePrimitives'
import { Button } from '@/components/ui/button'
import {
  Dialog,
  DialogContent,
  DialogDescription,
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
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from '@/components/ui/table'
import { CURRENCIES, formatDateTime } from '@/lib/format'
import { cn } from '@/lib/utils'

import { SettingsCard } from './SettingsCard'
import type { ExchangeRatesController } from './use-exchange-rates'

/** 费用换算卡片与公开汇率对话框（视图）；状态与汇率操作在 useExchangeRates。 */
export function ExchangeRatesCard({
  controller,
  reportingCurrency,
  onReportingCurrencyChange,
  timezone,
}: {
  controller: ExchangeRatesController
  reportingCurrency: string
  onReportingCurrencyChange: (currency: string) => void
  timezone: string
}) {
  const {
    exchangeData,
    customSource,
    customSourceAmount,
    customTargetAmount,
    customBaseSide,
    refreshingRates,
    publicRatesOpen,
    loadingPublicRates,
    publicRatesError,
    deletingCustomRateID,
    customSourceOptions,
    reportingCurrencyPending,
    customRateReady,
    setPublicRatesOpen,
    setCustomSource,
    setCustomSourceAmount,
    setCustomTargetAmount,
    refreshRates,
    showPublicRates,
    addCustomRate,
    changeCustomBaseSide,
    setCustomRateEnabled,
    deleteCustomRate,
  } = controller
  return (
    <>
      <SettingsCard
        icon={CoinsIcon}
        tag="BILLING / EXCHANGE"
        title="费用换算"
        description="服务器保留原价和原币种，汇总及详情按统计币种折算。"
      >
        <div className="grid gap-3 sm:grid-cols-[minmax(0,1fr)_auto]">
          <div className="flex flex-col gap-2">
            <Label>统计币种</Label>
            <Select
              value={reportingCurrency}
              onValueChange={(v) => v && onReportingCurrencyChange(v)}
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
          <div className="flex flex-wrap gap-2 self-end">
            <Button type="button" variant="outline" onClick={() => void showPublicRates()}>
              <SearchIcon />
              公开汇率查询
            </Button>
            <Button
              type="button"
              variant="outline"
              disabled={refreshingRates}
              onClick={refreshRates}
            >
              <RefreshCwIcon className={refreshingRates ? 'animate-spin' : ''} />
              {refreshingRates ? '刷新中…' : '立即刷新汇率'}
            </Button>
          </div>
        </div>
        <div className="cg-set-divider" />
        <div className="cg-set-group">
          <div className="cg-set-inline-row">
            <Label>自定义汇率</Label>
            <div className="cg-set-segment" role="group" aria-label="自定义汇率基准侧">
              <button
                type="button"
                className={cn(customBaseSide === 'source' && 'is-active')}
                onClick={() => changeCustomBaseSide('source')}
              >
                源币种 = 1
              </button>
              <button
                type="button"
                className={cn(customBaseSide === 'target' && 'is-active')}
                onClick={() => changeCustomBaseSide('target')}
              >
                展示币种 = 1
              </button>
            </div>
          </div>
          <div className="grid gap-2 sm:grid-cols-[minmax(0,1fr)_auto_minmax(0,1fr)] sm:items-center">
            <div className="grid grid-cols-[minmax(0,1fr)_96px] gap-2">
              <Input
                type="number"
                min="0"
                step="any"
                placeholder="金额"
                readOnly={customBaseSide === 'source'}
                value={customSourceAmount}
                onChange={(e) => setCustomSourceAmount(e.target.value)}
                aria-label="源币种金额"
              />
              <Select
                value={customSource}
                onValueChange={(v) => v && setCustomSource(v)}
                items={customSourceOptions.map((c) => ({ value: c, label: c }))}
              >
                <SelectTrigger aria-label="源币种">
                  <SelectValue placeholder="币种" />
                </SelectTrigger>
                <SelectContent>
                  {customSourceOptions.map((c) => (
                    <SelectItem key={c} value={c}>
                      {c}
                    </SelectItem>
                  ))}
                </SelectContent>
              </Select>
            </div>
            <span className="cg-set-ratio">:</span>
            <div className="grid grid-cols-[minmax(0,1fr)_96px] gap-2">
              <Input
                type="number"
                min="0"
                step="any"
                placeholder="金额"
                readOnly={customBaseSide === 'target'}
                value={customTargetAmount}
                onChange={(e) => setCustomTargetAmount(e.target.value)}
                aria-label="展示币种金额"
              />
              <div className="cg-set-static-field" aria-label="展示币种">
                {reportingCurrency}
              </div>
            </div>
          </div>
          <div>
            <button
              type="button"
              className="cg-button is-primary"
              disabled={!customRateReady}
              onClick={addCustomRate}
            >
              <PlusIcon />
              保存并启用
            </button>
          </div>
          {reportingCurrencyPending ? (
            <p className="cg-set-msg-info">展示币种已修改，请先保存设置再添加自定义汇率。</p>
          ) : null}
          <p className="cg-set-note">
            两侧必须有一侧为 1。以 1 USD : 7 CNY 为例：USD 直接按该汇率换算；CAD、EUR、JPY 等先按
            Frankfurter 换成 USD，再按自定义汇率换成 CNY；原价为 CNY
            的费用保持不变。切换展示币种不会删除记录，仅目标币种匹配当前展示币种的启用项参与自定义结果。费用详情同时显示公共汇率与自定义汇率结果。
          </p>
        </div>
        <div className="cg-set-divider" />
        <div className="cg-set-rate-list">
          {(exchangeData?.custom_rates ?? []).map((rate) => (
            <div key={rate.id} className="cg-set-rate-row">
              <div className="cg-set-rate-info">
                <span className="cg-set-rate-text">
                  {rate.source_amount} {rate.source_currency} : {rate.target_amount}{' '}
                  {rate.target_currency}
                </span>
                <span
                  className={cn(
                    'cg-status',
                    rate.enabled && rate.target_currency === reportingCurrency
                      ? 'is-lime'
                      : rate.enabled
                        ? 'is-blue'
                        : 'is-muted',
                  )}
                >
                  {rate.enabled && rate.target_currency === reportingCurrency
                    ? '当前使用'
                    : rate.enabled
                      ? `未应用 · ${rate.target_currency}`
                      : '已停用'}
                </span>
              </div>
              <div className="cg-set-rate-actions">
                <Button
                  type="button"
                  variant={rate.enabled ? 'secondary' : 'outline'}
                  size="sm"
                  onClick={() => setCustomRateEnabled(rate, !rate.enabled)}
                >
                  {rate.enabled ? '停用' : '启用'}
                </Button>
                <Button
                  type="button"
                  variant="ghost"
                  size="icon-sm"
                  className="cg-set-danger-btn"
                  aria-label="删除自定义汇率"
                  disabled={deletingCustomRateID === rate.id}
                  onClick={() => void deleteCustomRate(rate.id)}
                >
                  <Trash2Icon />
                </Button>
              </div>
            </div>
          ))}
          <p className="cg-set-note">
            公开汇率日期：{exchangeData?.rates[0]?.rate_date || '暂无缓存'}
          </p>
        </div>
      </SettingsCard>

      {/* 公开汇率 Dialog */}
      <Dialog open={publicRatesOpen} onOpenChange={setPublicRatesOpen}>
        <DialogContent className="max-h-[85vh] sm:max-w-4xl">
          <DialogHeader>
            <DialogTitle>公开汇率</DialogTitle>
            <DialogDescription>
              Frankfurter 公开汇率缓存，拉取 EUR / USD / CNY / JPY / CAD 五种基准。
            </DialogDescription>
          </DialogHeader>
          {loadingPublicRates ? (
            <LoadingState />
          ) : publicRatesError ? (
            <Notice tone="danger">{publicRatesError}</Notice>
          ) : exchangeData?.rates.length ? (
            <div className="max-h-[60vh] overflow-auto rounded-lg border">
              <Table>
                <TableHeader className="sticky top-0 z-10 bg-popover">
                  <TableRow>
                    <TableHead>基准币种</TableHead>
                    <TableHead>报价币种</TableHead>
                    <TableHead>公开汇率</TableHead>
                    <TableHead>汇率日期</TableHead>
                    <TableHead>抓取时间</TableHead>
                    <TableHead>来源</TableHead>
                  </TableRow>
                </TableHeader>
                <TableBody>
                  {exchangeData.rates.map((rate) => (
                    <TableRow key={`${rate.base_currency}-${rate.quote_currency}`}>
                      <TableCell className="font-medium">{rate.base_currency}</TableCell>
                      <TableCell className="font-medium">{rate.quote_currency}</TableCell>
                      <TableCell className="tabular-nums">
                        1 {rate.base_currency} = {rate.rate} {rate.quote_currency}
                      </TableCell>
                      <TableCell>{rate.rate_date}</TableCell>
                      <TableCell>{formatDateTime(rate.fetched_at, timezone)}</TableCell>
                      <TableCell className="capitalize">{rate.source}</TableCell>
                    </TableRow>
                  ))}
                </TableBody>
              </Table>
            </div>
          ) : (
            <p className="cg-set-dialog-empty">暂无公开汇率缓存，请先刷新汇率。</p>
          )}
        </DialogContent>
      </Dialog>
    </>
  )
}
