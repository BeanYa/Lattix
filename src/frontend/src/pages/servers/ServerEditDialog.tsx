import { AlertTriangleIcon, CableIcon, PlusIcon, XIcon } from 'lucide-react'

import { CountryCombobox } from '@/components/CountryCombobox'
import { TagInput } from '@/components/TagInput'
import { Badge } from '@/components/ui/badge'
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
import type { Provider } from '@/lib/types'
import { cn } from '@/lib/utils'

import { BillingTrafficFields, PortRowsEditor } from './server-form-fields'
import { ExternalExitFields } from './ExternalExitFields'
import { SERVER_TYPES, addrCandidates, serverTypeOf } from './server-form-utils'
import type { ServerEditFormController } from './use-server-edit-form'

/** 编辑服务器对话框（视图）；状态与提交逻辑在 useServerEditForm。 */
export function ServerEditDialog({
  controller,
  providers,
  onManageProviders,
}: {
  controller: ServerEditFormController
  providers: Provider[]
  onManageProviders: () => void
}) {
  const {
    target,
    saving,
    error,
    linkConflict,
    form,
    patch,
    countryOptions,
    citySuggestions,
    addCandidates,
    addAddress,
    setServerType,
    setCountry,
    setExternalLink,
    setExternalExit,
    parseExternalLink,
    closeEdit,
    onSubmit,
  } = controller
  const currentServerType = target ? serverTypeOf(target) : 'direct'
  const external = form.serverType === 'external'
  const isNat = form.serverType === 'nat'
  const typeChanged = target !== null && form.serverType !== currentServerType
  return (
    <Dialog open={target !== null} onOpenChange={(next) => !next && closeEdit()}>
      <DialogContent className="max-h-[90vh] overflow-y-auto sm:max-w-2xl">
        <DialogHeader>
          <DialogTitle>编辑服务器</DialogTitle>
          <DialogDescription>
            {external
              ? `修改「${target?.alias}」的服务器类型、外部直链协议参数与资料。`
              : `修改「${target?.alias}」的服务器类型、公网地址、可用端口段与配置；类型或可用端口变化时，受影响链路将自动执行 TryExchange。`}
          </DialogDescription>
        </DialogHeader>
        <form onSubmit={onSubmit} className="space-y-4">
          <div className="space-y-2">
            <Label id="edit-server-type-label">服务器类型</Label>
            <div
              role="radiogroup"
              aria-labelledby="edit-server-type-label"
              className="grid grid-cols-3 gap-2"
            >
              {SERVER_TYPES.map(([value, label]) => (
                <label
                  key={value}
                  className={cn('sv-radio-card', form.serverType === value && 'is-active')}
                >
                  <input
                    type="radio"
                    name="edit-server-type"
                    value={value}
                    checked={form.serverType === value}
                    onChange={() => setServerType(value)}
                    className="sr-only"
                  />
                  {label}
                </label>
              ))}
            </div>
            {typeChanged ? (
              form.serverType === 'external' ? (
                <div className="sv-inline-warning">
                  <AlertTriangleIcon className="mt-0.5 size-3.5 shrink-0" />
                  <span>
                    转为外部直链将断开面板控制并吊销 Agent 凭证（不会远程卸载目标机 Xray）；当前受影响链路将标记为「转换失败」并在订阅中下线。
                  </span>
                </div>
              ) : currentServerType === 'external' ? (
                <div className="sv-inline-note">
                  <CableIcon className="mt-0.5 size-3.5 shrink-0" />
                  <span>
                    转为托管服务器后将生成新的 Agent 安装命令；引用该节点的链路将自动尝试重编排，无法适配的链路将在链路页标记为「转换失败」。
                  </span>
                </div>
              ) : (
                <div className="sv-inline-note">
                  <CableIcon className="mt-0.5 size-3.5 shrink-0" />
                  <span>
                    切换服务器类型或调整 NAT 可用端口后，系统将自动对受影响链路执行 TryExchange 切换端口与传输；无法自动适配的链路将在链路页标记为「转换失败」。
                  </span>
                </div>
              )
            ) : null}
          </div>
          <div className="space-y-2">
            <Label htmlFor="editAlias">名称</Label>
            <Input
              id="editAlias"
              value={form.alias}
              onChange={(e) => patch({ alias: e.target.value })}
              maxLength={100}
              required={!external}
            />
          </div>
          {external ? (
            <div className="space-y-4">
              <ExternalExitFields
                idPrefix="editExternal"
                link={form.external.link}
                parsing={form.external.parsing}
                parseError={form.external.parseError}
                exit={form.external.exit}
                onLinkChange={setExternalLink}
                onParse={parseExternalLink}
                onExitChange={setExternalExit}
              />
              <div className="sv-inline-note">
                <CableIcon className="mt-0.5 size-3.5 shrink-0" />
                <span>
                  外部直链节点没有 Agent：安装命令、凭证轮换、修复、清理/重建
                  Xray、升级、服务器测试与设置覆盖均不可用。
                </span>
              </div>
            </div>
          ) : null}
          <div className="space-y-4">
            <div className="space-y-2">
              <Label htmlFor="edit-country">国家/地区</Label>
              <CountryCombobox
                id="edit-country"
                value={form.countryCode}
                options={countryOptions}
                onValueChange={setCountry}
                required={!external}
              />
            </div>
            <div className="space-y-2">
              <Label htmlFor="editLocation">地区/城市</Label>
              <Input
                id="editLocation"
                value={form.location}
                onChange={(e) => patch({ location: e.target.value })}
                placeholder="选择城市或输入机房位置"
                list="edit-server-location-options"
                maxLength={100}
              />
              <datalist id="edit-server-location-options">
                {citySuggestions.map((city) => (
                  <option key={city} value={city} />
                ))}
              </datalist>
            </div>
          </div>
          <div className="space-y-2">
            <Label htmlFor="editTags">标签（Tag）</Label>
            <TagInput
              id="editTags"
              value={form.tags}
              onChange={(tags) => patch({ tags })}
              placeholder="输入标签后按回车"
            />
            <p className="text-xs text-muted-foreground">
              回车或逗号确认；新建链路时可通过 {'{{TAG[0]}}'} 等参数引用。
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
            <div className="space-y-2">
              <Label>公网地址{isNat ? '（必填）' : ''}</Label>
              {form.addresses.length > 0 ? (
                <div className="space-y-1.5">
                  {form.addresses.map((addr) => {
                    const family = addressFamily(addr)
                    return (
                      <div key={addr} className="flex items-center gap-2">
                        <label className="flex min-w-0 flex-1 items-center gap-2 text-sm">
                          <input
                            type="radio"
                            name="edit-default-address"
                            checked={form.defaultAddr === addr}
                            onChange={() => patch({ defaultAddr: addr })}
                            title="设为默认地址"
                          />
                          <span className="truncate font-mono text-xs">{addr}</span>
                        </label>
                        <Badge variant="outline">
                          {family === 'ipv4' ? 'IPv4' : family === 'ipv6' ? 'IPv6' : '域名'}
                        </Badge>
                        {target && addrCandidates(target).includes(addr) ? (
                          <span className="shrink-0 text-xs text-muted-foreground">agent 上报</span>
                        ) : null}
                        {addr === form.defaultAddr ? (
                          <span className="shrink-0 text-xs text-muted-foreground">默认</span>
                        ) : (
                          <Button
                            type="button"
                            variant="outline"
                            size="icon"
                            title="删除该地址；引用它的链路跳将回退到默认地址"
                            onClick={() =>
                              patch({ addresses: form.addresses.filter((a) => a !== addr) })
                            }
                          >
                            <XIcon />
                          </Button>
                        )}
                      </div>
                    )
                  })}
                </div>
              ) : (
                <p className="text-xs text-muted-foreground">
                  暂无公网地址，请从下方候选或手动输入添加。
                </p>
              )}
              {addCandidates.length > 0 ? (
                <Select
                  value=""
                  onValueChange={(v) => {
                    if (v) addAddress(v)
                  }}
                  items={addCandidates.map((c) => ({ value: c, label: c }))}
                >
                  <SelectTrigger className="w-full">
                    <SelectValue placeholder="从 agent 上报地址添加…" />
                  </SelectTrigger>
                  {/* 动作型下拉：在触发器下方展开，避免选中项覆盖触发器造成误读 */}
                  <SelectContent alignItemWithTrigger={false}>
                    {addCandidates.map((c) => (
                      <SelectItem key={c} value={c}>
                        {c}
                        {c === target?.learned_addr ? '（拨入学习）' : ''}
                      </SelectItem>
                    ))}
                  </SelectContent>
                </Select>
              ) : null}
              <div className="flex items-center gap-2">
                <Input
                  id="editAddressInput"
                  value={form.addrInput}
                  onChange={(e) => patch({ addrInput: e.target.value })}
                  placeholder="手动输入地址，例如：1.2.3.4 或 hk-01.example.com"
                />
                <Button
                  type="button"
                  variant="outline"
                  size="sm"
                  disabled={!form.addrInput.trim()}
                  onClick={() => addAddress(form.addrInput)}
                >
                  <PlusIcon />
                  添加
                </Button>
              </div>
              <p className="text-xs text-muted-foreground">
                单选按钮选择默认地址；链路各跳未指定地址时使用默认地址。
              </p>
            </div>
          )}
          {external ? null : (
            <div className="space-y-2">
              <Label htmlFor="editXrayOverride">xray 版本（覆盖面板默认）</Label>
              <Select
                value={form.xrayOverride}
                onValueChange={(value) =>
                  value !== undefined && value !== null && patch({ xrayOverride: value })
                }
                items={[
                  { value: '', label: '跟随面板默认' },
                  ...form.xrayVersions
                    .filter((v) => v !== 'latest')
                    .map((version) => ({ value: version, label: version })),
                ]}
              >
                <SelectTrigger id="editXrayOverride" className="w-full">
                  <SelectValue placeholder="跟随面板默认" />
                </SelectTrigger>
                <SelectContent>
                  <SelectItem value="">跟随面板默认</SelectItem>
                  {form.xrayVersions
                    .filter((v) => v !== 'latest')
                    .map((version) => (
                      <SelectItem key={version} value={version}>
                        {version}
                      </SelectItem>
                    ))}
                </SelectContent>
              </Select>
            </div>
          )}
          {isNat ? (
            <div className="space-y-2">
              <Label>可用端口</Label>
              <PortRowsEditor rows={form.portRows} onChange={(portRows) => patch({ portRows })} />
              <p className="text-xs text-muted-foreground">
                每行一段：单端口 10000、范围 10001-10010、非 1:1 映射
                20001-20010:10001-10010（外部段:内部段）。全部留空 = 仅出口档（无入站能力）。
              </p>
            </div>
          ) : null}
          <BillingTrafficFields
            billing={form.billing}
            setBilling={(billing) => patch({ billing })}
            traffic={form.traffic}
            setTraffic={(traffic) => patch({ traffic })}
            providers={providers}
            onManageProviders={onManageProviders}
          />
          {error && <p className="text-sm text-destructive whitespace-pre-wrap">{error}</p>}
          {linkConflict ? (
            <div className="sv-inline-warning">
              <AlertTriangleIcon className="mt-0.5 size-3.5 shrink-0" />
              <span>链接不可修改时请先调整引用链路。</span>
            </div>
          ) : null}
          <DialogFooter>
            <Button
              type="submit"
              disabled={
                saving ||
                (!external && (!form.alias.trim() || !form.countryCode)) ||
                (isNat && !form.defaultAddr)
              }
            >
              {saving ? '保存中…' : '保存'}
            </Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  )
}
