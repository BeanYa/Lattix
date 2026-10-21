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
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from '@/components/ui/select'
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
    mode,
    creating,
    createError,
    created,
    form,
    groups,
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
          <DialogTitle>{mode === 'trial' ? '创建试用用户' : '创建用户'}</DialogTitle>
          <DialogDescription>
            {created
              ? mode === 'trial'
                ? '试用用户已创建，请将订阅链接发给用户；到期后账号将自动删除。'
                : '用户已创建，请将订阅链接发给用户。'
              : mode === 'trial'
                ? '填写姓名、试用流量与有效期创建临时账号。'
                : '输入姓名创建用户。'}
          </DialogDescription>
        </DialogHeader>
        {!created && mode === 'trial' && (
          <Notice tone="warning" role="alert" title="试用用户规则">
            试用用户到期后将自动删除（账号、订阅与节点访问一并移除，无法恢复）；流量用尽仅停用、不会删除，可在用户列表执行「流量重置」恢复。如需继续使用，请在到期前于「订阅设置」延长有效期。
          </Notice>
        )}
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
            <div className="grid gap-3 sm:grid-cols-2">
              <div className="space-y-2">
                <Label>用户分组（可选）</Label>
                <Select
                  value={form.userGroupId || 'none'}
                  onValueChange={(next) =>
                    next && patch({ userGroupId: next === 'none' ? '' : next })
                  }
                  items={[
                    { value: 'none', label: '无分组' },
                    ...groups.map((g) => ({ value: String(g.id), label: g.name })),
                  ]}
                >
                  <SelectTrigger aria-label="用户分组">
                    <SelectValue />
                  </SelectTrigger>
                  <SelectContent>
                    <SelectItem value="none">无分组</SelectItem>
                    {groups.map((g) => (
                      <SelectItem key={g.id} value={String(g.id)}>
                        {g.name}
                      </SelectItem>
                    ))}
                  </SelectContent>
                </Select>
                {form.userGroupId && (
                  <p className="cg-hint">
                    已选分组：链路绑定与外部订阅由该分组派生（直接分配被遮蔽）。
                  </p>
                )}
              </div>
            </div>
            <div className="space-y-2">
              <Label>生效时间</Label>
              <div className="flex flex-wrap items-center gap-3">
                <label className="flex items-center gap-1">
                  <input
                    type="radio"
                    name="effective-mode"
                    checked={form.effectiveMode === 'now'}
                    onChange={() => patch({ effectiveMode: 'now' })}
                  />
                  立即
                </label>
                <label className="flex items-center gap-1">
                  <input
                    type="radio"
                    name="effective-mode"
                    checked={form.effectiveMode === 'scheduled'}
                    onChange={() => patch({ effectiveMode: 'scheduled' })}
                  />
                  定时
                </label>
                {form.effectiveMode === 'scheduled' && (
                  <Input
                    type="datetime-local"
                    step={1}
                    className="flex-1"
                    value={form.effectiveAt}
                    onChange={(e) => patch({ effectiveAt: e.target.value })}
                    required
                  />
                )}
              </div>
              {form.effectiveMode === 'scheduled' && (
                <p className="cg-hint">
                  生效前订阅与链路均不可用；到生效时刻自动上线（精确到秒）。
                </p>
              )}
            </div>
            <div className="space-y-2">
              <Label>有效期</Label>
              <div className="flex flex-wrap items-center gap-3">
                {mode === 'normal' && (
                  <label className="flex items-center gap-1">
                    <input
                      type="radio"
                      name="expiry-mode"
                      checked={form.expiryMode === 'never'}
                      onChange={() => patch({ expiryMode: 'never' })}
                    />
                    长期
                  </label>
                )}
                <label className="flex items-center gap-1">
                  <input
                    type="radio"
                    name="expiry-mode"
                    checked={form.expiryMode === 'until'}
                    onChange={() => patch({ expiryMode: 'until' })}
                  />
                  到期时间
                </label>
                {form.expiryMode === 'until' && (
                  <Input
                    type="datetime-local"
                    step={1}
                    className="flex-1"
                    value={form.expiresAt}
                    onChange={(e) => onExpiresAtChange(e.target.value)}
                    required
                  />
                )}
                <label className="flex items-center gap-1">
                  <input
                    type="radio"
                    name="expiry-mode"
                    checked={form.expiryMode === 'days'}
                    onChange={() => patch({ expiryMode: 'days' })}
                  />
                  按天数
                </label>
                {form.expiryMode === 'days' && (
                  <Input
                    type="number"
                    min={1}
                    step={1}
                    className="w-28"
                    value={form.durationDays}
                    onChange={(e) => patch({ durationDays: e.target.value })}
                    placeholder="天数"
                    required
                  />
                )}
              </div>
              <p className="cg-hint">
                {mode === 'trial'
                  ? '试用用户必须设置有效期：到期后账号自动删除；如需继续使用请在到期前延长有效期。'
                  : form.expiryMode === 'days'
                    ? '自生效时刻起算（立即生效则自创建时刻起算），精确到秒。'
                    : '到期精确到秒；到期后节点连接与订阅更新同时失效，可延长有效期重新生效。'}
              </p>
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
              <Label>
                {mode === 'trial'
                  ? '订阅设置（试用流量用尽即停权、不删除；其余留空跟随全局）'
                  : '订阅设置（可选，留空跟随全局）'}
              </Label>
              <div className="grid gap-3 sm:grid-cols-2">
                <div className="space-y-1">
                  <TrafficLimitInput
                    value={form.trafficLimit}
                    unit={form.trafficUnit}
                    onValueChange={(value) => patch({ trafficLimit: value })}
                    onUnitChange={(unit) => patch({ trafficUnit: unit })}
                    placeholder={mode === 'trial' ? '试用流量' : '流量配额'}
                  />
                  {mode === 'trial' && (
                    <p className="cg-hint">
                      试用流量用尽即停用（不删除），可在用户列表执行「流量重置」恢复。
                    </p>
                  )}
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
