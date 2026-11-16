import { CountryCombobox } from '@/components/CountryCombobox'
import { TagInput } from '@/components/TagInput'
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
import type { Provider } from '@/lib/types'
import { cn } from '@/lib/utils'

import { BillingTrafficFields, ExternalLinkField, PortRowsEditor } from './server-form-fields'
import type { ServerTypeChoice } from './server-form-utils'
import type { ServerCreateFormController } from './use-server-create-form'

const SERVER_TYPES: ReadonlyArray<readonly [ServerTypeChoice, string]> = [
  ['direct', '独立 IP'],
  ['nat', 'NAT'],
  ['external', '外部直链'],
]

/** 添加服务器对话框（视图）；状态与提交逻辑在 useServerCreateForm。 */
export function ServerCreateDialog({
  controller,
  providers,
  onManageProviders,
}: {
  controller: ServerCreateFormController
  providers: Provider[]
  onManageProviders: () => void
}) {
  const {
    open,
    creating,
    createError,
    form,
    patch,
    countryOptions,
    citySuggestions,
    onOpenChange,
    setCountry,
    setServerType,
    setExternalLink,
    parseExternalLink,
    onCreate,
  } = controller
  const external = form.serverType === 'external'
  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="max-h-[90vh] overflow-y-auto sm:max-w-2xl">
        <DialogHeader>
          <DialogTitle>添加服务器</DialogTitle>
          <DialogDescription>
            {external
              ? '外部直链是未托管节点：地址与凭据来自分享链接，没有 Agent，只能作为链路的最后一跳出口。'
              : '输入别名创建服务器。'}
          </DialogDescription>
        </DialogHeader>
        <form onSubmit={onCreate} className="space-y-4">
          <div className="space-y-2">
            <Label id="server-type-label">服务器类型</Label>
            <div
              role="radiogroup"
              aria-labelledby="server-type-label"
              className="grid grid-cols-3 gap-2"
            >
              {SERVER_TYPES.map(([value, label]) => (
                <label
                  key={value}
                  className={cn('sv-radio-card', form.serverType === value && 'is-active')}
                >
                  <input
                    type="radio"
                    name="server-type"
                    value={value}
                    checked={form.serverType === value}
                    onChange={() => setServerType(value)}
                    className="sr-only"
                  />
                  {label}
                </label>
              ))}
            </div>
          </div>
          {external ? (
            <ExternalLinkField
              link={form.external.link}
              preview={form.external.preview}
              parseError={form.external.parseError}
              parsing={form.external.parsing}
              onChange={setExternalLink}
              onParse={parseExternalLink}
            />
          ) : null}
          <div className="space-y-2">
            <Label htmlFor="alias">别名</Label>
            <Input
              id="alias"
              value={form.alias}
              onChange={(e) => patch({ alias: e.target.value })}
              placeholder="例如：hk-01"
              required
              autoFocus={!external}
            />
            {external ? (
              <p className="text-xs text-muted-foreground">
                别名为空时，解析成功后自动使用节点名。
              </p>
            ) : null}
          </div>
          <div className="space-y-4">
            <div className="space-y-2">
              <Label htmlFor="country">国家/地区</Label>
              <CountryCombobox
                id="country"
                value={form.countryCode}
                options={countryOptions}
                onValueChange={setCountry}
              />
            </div>
            <div className="space-y-2">
              <Label htmlFor="location">地区/城市</Label>
              <Input
                id="location"
                value={form.location}
                onChange={(e) => patch({ location: e.target.value })}
                placeholder="选择城市或输入机房位置"
                list="server-location-options"
                maxLength={100}
              />
              <datalist id="server-location-options">
                {citySuggestions.map((city) => (
                  <option key={city} value={city} />
                ))}
              </datalist>
              <p className="text-xs text-muted-foreground">
                城市列表仅作辅助，也可填写自定义机房区域。
              </p>
            </div>
          </div>
          <div className="space-y-2">
            <Label htmlFor="tags">标签（Tag）</Label>
            <TagInput
              id="tags"
              value={form.tags}
              onChange={(tags) => patch({ tags })}
              placeholder="输入标签后按回车"
            />
            <p className="text-xs text-muted-foreground">
              回车或逗号确认，最多 10 个；名称模板可按顺序使用 {'{{TAG[0]}}'}、{'{{TAG[1]}}'}。
            </p>
          </div>
          <label className="flex items-start gap-2 text-sm">
            <input
              type="checkbox"
              className="sv-checkbox mt-0.5"
              checked={form.nonDirect}
              onChange={(e) => patch({ nonDirect: e.target.checked })}
            />
            <span>
              非直连（大陆不可直连）
              <span className="block text-xs text-muted-foreground">
                勾选后该服务器不参与 Ban 告警判定（三网测速丢包属预期，巡检将忽略）。
              </span>
            </span>
          </label>
          {external ? null : (
            <>
              <div className="space-y-2">
                <Label htmlFor="address">
                  公网地址{form.serverType === 'nat' ? '（必填）' : ''}
                </Label>
                <Input
                  id="address"
                  value={form.address}
                  onChange={(e) => patch({ address: e.target.value })}
                  placeholder={
                    form.serverType === 'nat'
                      ? '共享公网 IP 或域名（由 IDC 提供）'
                      : '留空按 agent 拨入地址自动学习'
                  }
                />
              </div>
              {form.serverType === 'nat' && (
                <div className="space-y-2">
                  <Label>可用端口</Label>
                  <PortRowsEditor
                    rows={form.portRows}
                    onChange={(portRows) => patch({ portRows })}
                  />
                  <p className="text-xs text-muted-foreground">
                    每行一段：单端口 10000、范围 10001-10010、非 1:1 映射
                    20001-20010:10001-10010（外部段:内部段）。全部留空 = 仅出口档（无入站能力）。
                  </p>
                </div>
              )}
            </>
          )}
          <BillingTrafficFields
            billing={form.billing}
            setBilling={(billing) => patch({ billing })}
            traffic={form.traffic}
            setTraffic={(traffic) => patch({ traffic })}
            providers={providers}
            onManageProviders={onManageProviders}
          />
          {createError && <p className="text-sm text-destructive">{createError}</p>}
          <DialogFooter className="sticky bottom-0 z-10 -mx-4 -mb-4 border-t bg-[var(--panel-surface)] px-4 py-3">
            <Button
              type="submit"
              disabled={
                creating ||
                !form.alias.trim() ||
                !form.countryCode ||
                (form.serverType === 'nat' && !form.address.trim()) ||
                (external && !form.external.link.trim())
              }
            >
              {creating ? '创建中…' : '创建'}
            </Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  )
}
