import { Notice } from '@/components/PagePrimitives'
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

import { TrafficLimitInput } from './user-form-fields'
import type { ReactivateUserController } from './use-reactivate-user'

/** 到期用户重新激活对话框（视图）；状态与提交逻辑在 useReactivateUser。 */
export function ReactivateUserDialog({ controller }: { controller: ReactivateUserController }) {
  const { target, saving, err, form, patch, close, onSave } = controller
  return (
    <Dialog open={target !== null} onOpenChange={(next) => !next && close()}>
      <DialogContent className="sm:max-w-lg">
        <DialogHeader>
          <DialogTitle>重新激活「{target?.name}」</DialogTitle>
          <DialogDescription>
            重设流量与有效期后立即恢复：订阅链接保持不变，客户端可无感继续使用；节点在保存后自动恢复下发。
          </DialogDescription>
        </DialogHeader>
        <div className="space-y-4">
          <div className="space-y-2">
            <Label>流量配额（留空为不限）</Label>
            <TrafficLimitInput
              value={form.trafficLimit}
              unit={form.trafficUnit}
              onValueChange={(value) => patch({ trafficLimit: value })}
              onUnitChange={(unit) => patch({ trafficUnit: unit })}
            />
            {target?.is_trial && (
              <p className="cg-hint">试用流量将清零重新累计，按新配额执行（用尽即再次停权）。</p>
            )}
          </div>
          <div className="space-y-2">
            <Label htmlFor="reactivate-expires-at">新有效期（精确到秒）</Label>
            <Input
              id="reactivate-expires-at"
              type="datetime-local"
              step={1}
              value={form.expiresAt}
              onChange={(e) => patch({ expiresAt: e.target.value })}
              required
            />
            <p className="cg-hint">到期后可再次重新激活；到期前链路与订阅照常可用。</p>
          </div>
        </div>
        {err && <Notice tone="danger">{err}</Notice>}
        <DialogFooter showCloseButton>
          <Button disabled={saving} onClick={onSave}>
            {saving ? '激活中…' : '重新激活'}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}
