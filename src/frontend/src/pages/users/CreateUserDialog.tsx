import { CopyButton } from '@/components/CopyButton'
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
import { humanizeBytes } from '@/lib/format'
import type { LinkOption } from '@/lib/links'
import type {
  ExternalSubscription,
  SubscriptionRuleCategory,
  SubscriptionTemplate,
} from '@/lib/types'

import { ExternalModeSelect, TrafficLimitInput } from './user-form-fields'
import type { CreateUserFormController } from './use-create-user-form'

/** 创建用户对话框（视图）；状态与提交逻辑在 useCreateUserForm。 */
export function CreateUserDialog({
  controller,
  linkOptions,
  ruleCategories,
  templates,
  extSubs,
}: {
  controller: CreateUserFormController
  linkOptions: LinkOption[]
  ruleCategories: SubscriptionRuleCategory[]
  templates: SubscriptionTemplate[]
  extSubs: ExternalSubscription[]
}) {
  const {
    open,
    creating,
    createError,
    created,
    form,
    patch,
    onOpenChange,
    onToggleCreateLink,
    onExpiresAtChange,
    toggleExt,
    setExtMode,
    onCreate,
  } = controller
  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="max-h-[85vh] sm:max-w-4xl overflow-y-auto [&>*]:min-w-0">
        <DialogHeader>
          <DialogTitle>创建用户</DialogTitle>
          <DialogDescription>
            {created ? '用户已创建，请将订阅链接发给用户。' : '输入姓名创建用户。'}
          </DialogDescription>
        </DialogHeader>
        {created ? (
          <div className="space-y-3">
            <div className="space-y-2">
              <Label>订阅链接</Label>
              <div className="cg-users-created-url">{created.sub_url}</div>
            </div>
            <DialogFooter showCloseButton>
              <CopyButton text={created.sub_url} />
            </DialogFooter>
          </div>
        ) : (
          <form onSubmit={onCreate} className="space-y-4">
            <div className="space-y-2">
              <Label htmlFor="name">姓名</Label>
              <Input
                id="name"
                value={form.name}
                onChange={(e) => patch({ name: e.target.value })}
                placeholder="例如：张三"
                required
                autoFocus
              />
            </div>
            <div className="space-y-2">
              <Label htmlFor="expires-at">
                有效期（可选，留空为长期；选日期则当天 23:59 到期）
              </Label>
              <Input
                id="expires-at"
                type="date"
                value={form.expiresAt}
                onChange={(e) => onExpiresAtChange(e.target.value)}
              />
              {form.expiresAt && <p className="cg-hint">重置日默认取到期日（可修改）。</p>}
            </div>
            <div className="space-y-2">
              <Label>分配链路（可选）</Label>
              {linkOptions.length === 0 ? (
                <p className="cg-hint">暂无链路，请先在「链路」页创建。</p>
              ) : (
                linkOptions.map((link) => (
                  <label key={link.chainId} className="cg-check-row">
                    <input
                      type="checkbox"
                      className="cg-checkbox"
                      checked={form.linkSel.includes(link.chainId)}
                      onChange={(e) => onToggleCreateLink(link.chainId, e.target.checked)}
                    />
                    <span className="cg-status is-blue">
                      {link.type === 'direct' ? '直连' : '中转'}
                    </span>
                    <span>{link.name}</span>
                    <span className="cg-check-row-detail">{link.detail}</span>
                  </label>
                ))
              )}
            </div>
            <div className="space-y-2">
              <Label>订阅设置（可选，留空跟随全局）</Label>
              <div className="grid gap-3 sm:grid-cols-2">
                <div className="space-y-1">
                  <TrafficLimitInput
                    value={form.trafficLimit}
                    unit={form.trafficUnit}
                    onValueChange={(value) => patch({ trafficLimit: value })}
                    onUnitChange={(unit) => patch({ trafficUnit: unit })}
                  />
                </div>
                <div className="space-y-1">
                  <Input
                    type="number"
                    min={1}
                    max={31}
                    step={1}
                    value={form.resetDay}
                    onChange={(e) => patch({ resetDay: e.target.value })}
                    placeholder="重置日（留空跟随有效期/创建日）"
                  />
                </div>
                <div className="space-y-1">
                  <Input
                    value={form.planName}
                    onChange={(e) => patch({ planName: e.target.value })}
                    placeholder="套餐名，如 VIP1"
                  />
                </div>
                <div className="space-y-1">
                  <Input
                    value={form.appURL}
                    onChange={(e) => patch({ appURL: e.target.value })}
                    placeholder="客户端跳转链接"
                  />
                </div>
              </div>
            </div>
            <SubscriptionRoutingFields
              value={form.routing}
              onChange={(routing) => patch({ routing })}
              categories={ruleCategories}
              templates={templates}
            />
            <div className="space-y-2 border-t pt-3">
              <Label>外部订阅（叠加 = 额度相加，并入 = 已用计入面板配额，附加 = 仅节点）</Label>
              {extSubs.length === 0 ? (
                <p className="cg-hint">暂无外部订阅，请先在「外部订阅」页添加。</p>
              ) : (
                extSubs.map((sub) => {
                  const checked = form.ext[sub.id] !== undefined
                  return (
                    <label key={sub.id} className="cg-check-row">
                      <input
                        type="checkbox"
                        className="cg-checkbox"
                        checked={checked}
                        onChange={(e) => toggleExt(sub.id, e.target.checked)}
                      />
                      <span>{sub.name}</span>
                      <span className="cg-check-row-detail">
                        {sub.total > 0
                          ? `${humanizeBytes(sub.total)} / 已用 ${humanizeBytes(sub.upload + sub.download)}`
                          : '额度未知'}
                      </span>
                      <span className="ml-auto">
                        <ExternalModeSelect
                          value={checked ? form.ext[sub.id] : 'stack'}
                          disabled={!checked}
                          onChange={(mode) => setExtMode(sub.id, mode)}
                        />
                      </span>
                    </label>
                  )
                })
              )}
            </div>
            {createError && <Notice tone="danger">{createError}</Notice>}
            <DialogFooter>
              <Button type="submit" disabled={creating || !form.name.trim()}>
                {creating ? '创建中…' : '创建'}
              </Button>
            </DialogFooter>
          </form>
        )}
      </DialogContent>
    </Dialog>
  )
}
