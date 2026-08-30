import { Notice } from '@/components/PagePrimitives'
import { SubscriptionRoutingFields } from '@/components/SubscriptionRoutingFields'
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
import type { SubscriptionRuleCategory, SubscriptionTemplate } from '@/lib/types'

import { TrafficLimitInput } from './user-form-fields'
import type { UserSubSettingsController } from './use-user-sub-settings'

/** 用户订阅设置对话框（视图）；状态与保存逻辑在 useUserSubSettings。 */
export function UserSubSettingsDialog({
  controller,
  ruleCategories,
  templates,
}: {
  controller: UserSubSettingsController
  ruleCategories: SubscriptionRuleCategory[]
  templates: SubscriptionTemplate[]
}) {
  const { target, saving, err, form, patch, close, onExpiresAtChange, clearExpiresAt, onSave } =
    controller
  return (
    <Dialog open={target !== null} onOpenChange={(next) => !next && close()}>
      <DialogContent className="max-h-[90vh] sm:max-w-5xl overflow-y-auto">
        <DialogHeader>
          <DialogTitle>订阅设置</DialogTitle>
          <DialogDescription>
            「{target?.name}」的有效期、落地页、分流策略与发布订阅快照。
          </DialogDescription>
        </DialogHeader>
        <div className="space-y-4">
          <div className="grid items-end gap-4 sm:grid-cols-2">
            <div className="space-y-2">
              <Label>流量配额（留空为不限）</Label>
              <TrafficLimitInput
                value={form.trafficLimit}
                unit={form.trafficUnit}
                onValueChange={(value) => patch({ trafficLimit: value })}
                onUnitChange={(unit) => patch({ trafficUnit: unit })}
              />
            </div>
            <div className="space-y-2">
              <Label>重置日（1–31，留空跟随有效期到期日/创建日）</Label>
              <Input
                type="number"
                min={1}
                max={31}
                step={1}
                value={form.resetDay}
                onChange={(e) => patch({ resetDay: e.target.value })}
                placeholder="创建日"
              />
            </div>
          </div>
          <div className="space-y-2">
            <Label htmlFor="sub-expires-at">
              有效期（留空并保存即为长期；选日期则当天 23:59 到期）
            </Label>
            <div className="flex items-center gap-2">
              <Input
                id="sub-expires-at"
                type="date"
                className="flex-1"
                value={form.expiresAt}
                onChange={(e) => onExpiresAtChange(e.target.value)}
              />
              {form.expiresAt && (
                <Button type="button" variant="outline" onClick={clearExpiresAt}>
                  清除有效期
                </Button>
              )}
            </div>
            <p className="cg-hint">
              到期后自动停权（订阅保留但链路为空）；延长或清除有效期会恢复其链路。
            </p>
          </div>
          <div className="space-y-2">
            <Label>落地页标题覆盖</Label>
            <Input
              value={form.titleOverride}
              onChange={(e) => patch({ titleOverride: e.target.value })}
              placeholder="留空跟随全局"
            />
          </div>
          <div className="space-y-2">
            <Label>公告覆盖（Markdown）</Label>
            <textarea
              className="cg-textarea"
              rows={3}
              value={form.announcementOverride}
              onChange={(e) => patch({ announcementOverride: e.target.value })}
              placeholder="留空跟随全局"
            />
          </div>
          <div className="grid gap-4 sm:grid-cols-2">
            <div className="space-y-2">
              <Label>套餐名</Label>
              <Input
                value={form.planName}
                onChange={(e) => patch({ planName: e.target.value })}
                placeholder="留空跟随全局"
              />
              <p className="cg-hint">客户端 hover 流量信息时显示</p>
            </div>
            <div className="space-y-2">
              <Label>跳转链接</Label>
              <Input
                value={form.appURL}
                onChange={(e) => patch({ appURL: e.target.value })}
                placeholder="留空跟随全局"
              />
              <p className="cg-hint">客户端流量卡片可点击跳转的按钮</p>
            </div>
          </div>
          <SubscriptionRoutingFields
            value={form.routing}
            onChange={(routing) => patch({ routing })}
            categories={ruleCategories}
            templates={templates}
          />
        </div>
        {err && <Notice tone="danger">{err}</Notice>}
        <DialogFooter>
          <Button disabled={saving} onClick={onSave}>
            {saving ? '保存中…' : '保存'}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}
