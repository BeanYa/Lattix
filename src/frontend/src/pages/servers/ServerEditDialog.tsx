import { PlusIcon, XIcon } from 'lucide-react'

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

import { BillingTrafficFields, PortRowsEditor } from './server-form-fields'
import { addrCandidates } from './server-form-utils'
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
    form,
    patch,
    countryOptions,
    citySuggestions,
    addCandidates,
    addAddress,
    setCountry,
    closeEdit,
    onSubmit,
  } = controller
  return (
    <Dialog open={target !== null} onOpenChange={(next) => !next && closeEdit()}>
      <DialogContent className="max-h-[90vh] overflow-y-auto sm:max-w-2xl">
        <DialogHeader>
          <DialogTitle>编辑服务器</DialogTitle>
          <DialogDescription>
            {target?.machine_type === 'nat'
              ? `修改「${target?.alias}」的公网地址与可用端口段（NAT 类型地址必填；端口段收窄时存量节点/链跳端口不得越界）。机器类型建后不可互转。`
              : `修改「${target?.alias}」的公网地址，订阅中节点地址随之更新。内置地址来自 agent 上报的网卡地址与拨入学习地址；自定义留空则下次 agent 连接时按拨入地址重新自动学习。`}
          </DialogDescription>
        </DialogHeader>
        <form onSubmit={onSubmit} className="space-y-4">
          <div className="space-y-2">
            <Label htmlFor="editAlias">名称</Label>
            <Input
              id="editAlias"
              value={form.alias}
              onChange={(e) => patch({ alias: e.target.value })}
              maxLength={100}
              required
            />
          </div>
          <div className="space-y-4">
            <div className="space-y-2">
              <Label htmlFor="edit-country">国家/地区</Label>
              <CountryCombobox
                id="edit-country"
                value={form.countryCode}
                options={countryOptions}
                onValueChange={setCountry}
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
          <div className="space-y-2">
            <Label>公网地址{target?.machine_type === 'nat' ? '（必填）' : ''}</Label>
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
          {target?.machine_type === 'nat' && (
            <div className="space-y-2">
              <Label>可用端口</Label>
              <PortRowsEditor rows={form.portRows} onChange={(portRows) => patch({ portRows })} />
              <p className="text-xs text-muted-foreground">
                每行一段：单端口 10000、范围 10001-10010、非 1:1 映射
                20001-20010:10001-10010（外部段:内部段）。全部留空 = 仅出口档（无入站能力）。
              </p>
            </div>
          )}
          <BillingTrafficFields
            billing={form.billing}
            setBilling={(billing) => patch({ billing })}
            traffic={form.traffic}
            setTraffic={(traffic) => patch({ traffic })}
            providers={providers}
            onManageProviders={onManageProviders}
          />
          {error && <p className="text-sm text-destructive">{error}</p>}
          <DialogFooter>
            <Button
              type="submit"
              disabled={
                saving ||
                !form.alias.trim() ||
                (target?.machine_type === 'nat' && !form.defaultAddr)
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
