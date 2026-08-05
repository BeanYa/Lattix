import { useCallback, useEffect, useMemo, useState } from 'react'
import { PlusIcon, Trash2Icon } from 'lucide-react'
import { Button } from '@/components/ui/button'
import { Badge } from '@/components/ui/badge'
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from '@/components/ui/dialog'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from '@/components/ui/select'
import { Tabs, TabsList, TabsTrigger } from '@/components/ui/tabs'
import { EmptyState, LoadingState, Notice, Page, PageHeader } from '@/components/PagePrimitives'
import { api, errorMessage } from '@/lib/api'
import { buildLinkOptions } from '@/lib/links'
import type { Chain, ExternalSubscription, ExternalSubscriptionMode, LinkGroup, SubUser, UserGroup } from '@/lib/types'
import { useAppDialog } from '@/lib/app-dialog'
import { humanizeBytes } from '@/lib/format'

const EXTERNAL_MODE_LABELS: Record<ExternalSubscriptionMode, string> = {
  stack: '叠加',
  merge: '并入',
  nodes: '仅节点',
}

function ModeSelect({ value, disabled, onChange }: { value: ExternalSubscriptionMode; disabled?: boolean; onChange: (m: ExternalSubscriptionMode) => void }) {
  return (
    <Select value={value} disabled={disabled} onValueChange={(v) => v && onChange(v as ExternalSubscriptionMode)}>
      <SelectTrigger className="h-7 w-24 text-xs"><SelectValue /></SelectTrigger>
      <SelectContent>
        {(Object.keys(EXTERNAL_MODE_LABELS) as ExternalSubscriptionMode[]).map((mode) => (
          <SelectItem key={mode} value={mode}>{EXTERNAL_MODE_LABELS[mode]}</SelectItem>
        ))}
      </SelectContent>
    </Select>
  )
}

export default function Groups() {
  const [tab, setTab] = useState<'links' | 'users'>('links')
  return (
    <Page>
      <PageHeader title="分组" description="链路分组编排链路与外部订阅；用户分组把链路分组分配给用户。" />
      <Tabs value={tab} onValueChange={(value) => value && setTab(value as 'links' | 'users')}>
        <TabsList>
          <TabsTrigger value="links">链路分组</TabsTrigger>
          <TabsTrigger value="users">用户分组</TabsTrigger>
        </TabsList>
      </Tabs>
      {tab === 'links' ? <LinkGroupsTab /> : <UserGroupsTab />}
    </Page>
  )
}

function LinkGroupsTab() {
  const { confirm } = useAppDialog()
  const [groups, setGroups] = useState<LinkGroup[] | null>(null)
  const [chains, setChains] = useState<Chain[]>([])
  const [extSubs, setExtSubs] = useState<ExternalSubscription[]>([])
  const [error, setError] = useState('')
  const [editing, setEditing] = useState<LinkGroup | null | 'new'>(null)
  const [saving, setSaving] = useState(false)

  const load = useCallback(async () => {
    try {
      const [gs, cs, subs] = await Promise.all([
        api.linkGroups({ display: 'silent' }),
        api.chains({ display: 'silent' }),
        api.externalSubscriptions({ display: 'silent' }),
      ])
      setGroups(gs)
      setChains(cs)
      setExtSubs(subs)
      setError('')
    } catch (err) {
      setError(errorMessage(err))
    }
  }, [])
  useEffect(() => {
    void load()
    const timer = setInterval(load, 5000)
    return () => clearInterval(timer)
  }, [load])

  const linkOptions = useMemo(() => buildLinkOptions(chains), [chains])

  async function onDelete(g: LinkGroup) {
    if (!(await confirm({ title: '删除链路分组', description: `删除「${g.name}」后，引用它的用户分组将不再包含该分组的链路与外部订阅。`, confirmLabel: '删除', destructive: true }))) return
    try {
      await api.linkGroupDelete(g.id)
      void load()
    } catch (err) {
      setError(errorMessage(err))
    }
  }

  return (
    <div className="space-y-4">
      <div className="flex justify-end">
        <Button onClick={() => setEditing('new')}><PlusIcon className="size-4" />新建链路分组</Button>
      </div>
      {error && <Notice tone="danger">{error}</Notice>}
      {groups === null ? <LoadingState /> : groups.length === 0 ? (
        <EmptyState title="暂无链路分组" description="创建链路分组，把链路与外部订阅编排到一起。" />
      ) : (
        groups.map((g) => (
          <div key={g.id} className="game-panel space-y-2 rounded-lg border p-4">
            <div className="flex items-center justify-between">
              <div className="flex items-center gap-2">
                <span className="font-medium">{g.name}</span>
                <Badge variant="secondary">{g.chain_count} 链路</Badge>
                <Badge variant="secondary">{g.external_subscription_count} 外部订阅</Badge>
                {(g.user_group_names ?? []).map((n) => <Badge key={n} variant="outline">{n}</Badge>)}
              </div>
              <div className="flex gap-2">
                <Button variant="outline" size="sm" onClick={() => setEditing(g)}>编辑</Button>
                <Button variant="outline" size="sm" onClick={() => onDelete(g)}><Trash2Icon className="size-4" /></Button>
              </div>
            </div>
          </div>
        ))
      )}
      {editing && (
        <LinkGroupDialog
          group={editing === 'new' ? null : editing}
          linkOptions={linkOptions}
          extSubs={extSubs}
          saving={saving}
          setSaving={setSaving}
          onClose={() => setEditing(null)}
          onSaved={() => { setEditing(null); void load() }}
        />
      )}
    </div>
  )
}

function LinkGroupDialog({ group, linkOptions, extSubs, saving, setSaving, onClose, onSaved }: {
  group: LinkGroup | null
  linkOptions: ReturnType<typeof buildLinkOptions>
  extSubs: ExternalSubscription[]
  saving: boolean
  setSaving: (v: boolean) => void
  onClose: () => void
  onSaved: () => void
}) {
  const [name, setName] = useState(group?.name ?? '')
  const [chainSel, setChainSel] = useState<number[]>(group?.chain_ids ?? [])
  const [extSel, setExtSel] = useState<Record<number, ExternalSubscriptionMode>>(() => {
    const init: Record<number, ExternalSubscriptionMode> = {}
    for (const sub of group?.external_subscriptions ?? []) init[sub.subscription_id] = sub.mode
    return init
  })
  const [error, setError] = useState('')

  function toggleChain(id: number, checked: boolean) {
    setChainSel((cur) => (checked ? [...cur, id] : cur.filter((x) => x !== id)))
  }

  async function onSave() {
    if (!name.trim()) { setError('分组名称不能为空'); return }
    setSaving(true)
    setError('')
    try {
      const external_subscriptions = Object.entries(extSel).map(([sid, mode]) => ({ subscription_id: Number(sid), mode }))
      if (group) {
        await api.linkGroupUpdate({ id: group.id, name: name.trim(), chain_ids: chainSel, external_subscriptions })
      } else {
        await api.linkGroupCreate({ name: name.trim(), chain_ids: chainSel, external_subscriptions })
      }
      onSaved()
    } catch (err) {
      setError(errorMessage(err))
    } finally {
      setSaving(false)
    }
  }

  return (
    <Dialog open onOpenChange={(next) => !next && onClose()}>
      <DialogContent className="max-h-[85vh] overflow-y-auto">
        <DialogHeader>
          <DialogTitle>{group ? '编辑链路分组' : '新建链路分组'}</DialogTitle>
          <DialogDescription>勾选链路与外部订阅；外部订阅整体原子参与分组。</DialogDescription>
        </DialogHeader>
        <div className="space-y-3">
          <div>
            <Label>分组名称</Label>
            <Input value={name} onChange={(e) => setName(e.target.value)} placeholder="例如：旗舰线路" />
          </div>
          <div className="space-y-2">
            <Label>链路（仅共享入口链路可分配）</Label>
            {linkOptions.length === 0 ? (
              <p className="text-sm text-muted-foreground">暂无链路，请先在「链路」页创建。</p>
            ) : (
              linkOptions.map((link) => (
                <label key={link.chainId} className="flex cursor-pointer items-center gap-2 rounded-md border p-2 text-sm">
                  <input type="checkbox" className="size-4 accent-primary" checked={chainSel.includes(link.chainId)} onChange={(e) => toggleChain(link.chainId, e.target.checked)} />
                  <Badge variant="secondary">{link.type === 'direct' ? '直连' : '中转'}</Badge>
                  <span>{link.name}</span>
                  <span className="text-xs text-muted-foreground">{link.detail}</span>
                  {link.status !== 'active' && <span className="text-xs text-muted-foreground">（{link.status}）</span>}
                </label>
              ))
            )}
          </div>
          <div className="space-y-2 border-t pt-3">
            <Label>外部订阅（叠加 = 额度相加，并入 = 已用计入面板配额，附加 = 仅节点）</Label>
            {extSubs.length === 0 ? (
              <p className="text-sm text-muted-foreground">暂无外部订阅，请先在「外部订阅」页添加。</p>
            ) : (
              extSubs.map((sub) => {
                const checked = extSel[sub.id] !== undefined
                return (
                  <label key={sub.id} className="flex cursor-pointer items-center gap-2 rounded-md border p-2 text-sm">
                    <input type="checkbox" className="size-4 accent-primary" checked={checked}
                      onChange={(e) => setExtSel((cur) => { const next = { ...cur }; if (e.target.checked) next[sub.id] = 'stack'; else delete next[sub.id]; return next })} />
                    <span>{sub.name}</span>
                    <span className="text-xs text-muted-foreground">
                      {sub.total > 0 ? `${humanizeBytes(sub.total)} / 已用 ${humanizeBytes(sub.upload + sub.download)}` : '额度未知'}
                    </span>
                    <span className="ml-auto">
                      <ModeSelect value={checked ? extSel[sub.id] : 'stack'} disabled={!checked} onChange={(mode) => setExtSel((cur) => ({ ...cur, [sub.id]: mode }))} />
                    </span>
                  </label>
                )
              })
            )}
          </div>
          {error && <p className="text-sm text-destructive">{error}</p>}
        </div>
        <DialogFooter>
          <Button onClick={onSave} disabled={saving}>{saving ? '保存中…' : '保存'}</Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}

function UserGroupsTab() {
  const { confirm } = useAppDialog()
  const [groups, setGroups] = useState<UserGroup[] | null>(null)
  const [users, setUsers] = useState<SubUser[]>([])
  const [linkGroups, setLinkGroups] = useState<LinkGroup[]>([])
  const [error, setError] = useState('')
  const [editing, setEditing] = useState<UserGroup | null | 'new'>(null)
  const [saving, setSaving] = useState(false)

  const load = useCallback(async () => {
    try {
      const [gs, us, lgs] = await Promise.all([
        api.userGroups({ display: 'silent' }),
        api.users({ display: 'silent' }),
        api.linkGroups({ display: 'silent' }),
      ])
      setGroups(gs)
      setUsers(us)
      setLinkGroups(lgs)
      setError('')
    } catch (err) {
      setError(errorMessage(err))
    }
  }, [])
  useEffect(() => {
    void load()
    const timer = setInterval(load, 5000)
    return () => clearInterval(timer)
  }, [load])

  async function onDelete(g: UserGroup) {
    if (!(await confirm({ title: '删除用户分组', description: `删除「${g.name}」后，成员恢复直接分配。`, confirmLabel: '删除', destructive: true }))) return
    try {
      await api.userGroupDelete(g.id)
      void load()
    } catch (err) {
      setError(errorMessage(err))
    }
  }

  return (
    <div className="space-y-4">
      <div className="flex justify-end">
        <Button onClick={() => setEditing('new')}><PlusIcon className="size-4" />新建用户分组</Button>
      </div>
      {error && <Notice tone="danger">{error}</Notice>}
      {groups === null ? <LoadingState /> : groups.length === 0 ? (
        <EmptyState title="暂无用户分组" description="创建用户分组，把链路分组分配给一批用户。" />
      ) : (
        groups.map((g) => (
          <div key={g.id} className="game-panel space-y-2 rounded-lg border p-4">
            <div className="flex items-center justify-between">
              <div className="flex items-center gap-2">
                <span className="font-medium">{g.name}</span>
                <Badge variant="secondary">{g.member_count} 用户</Badge>
                <Badge variant="secondary">{g.link_group_count} 链路分组</Badge>
              </div>
              <div className="flex gap-2">
                <Button variant="outline" size="sm" onClick={() => setEditing(g)}>编辑</Button>
                <Button variant="outline" size="sm" onClick={() => onDelete(g)}><Trash2Icon className="size-4" /></Button>
              </div>
            </div>
          </div>
        ))
      )}
      {editing && (
        <UserGroupDialog
          group={editing === 'new' ? null : editing}
          users={users}
          linkGroups={linkGroups}
          saving={saving}
          setSaving={setSaving}
          onClose={() => setEditing(null)}
          onSaved={() => { setEditing(null); void load() }}
        />
      )}
    </div>
  )
}

function UserGroupDialog({ group, users, linkGroups, saving, setSaving, onClose, onSaved }: {
  group: UserGroup | null
  users: SubUser[]
  linkGroups: LinkGroup[]
  saving: boolean
  setSaving: (v: boolean) => void
  onClose: () => void
  onSaved: () => void
}) {
  const [name, setName] = useState(group?.name ?? '')
  const [userSel, setUserSel] = useState<number[]>(group?.user_ids ?? [])
  const [linkGroupSel, setLinkGroupSel] = useState<number[]>(group?.link_group_ids ?? [])
  const [error, setError] = useState('')

  async function onSave() {
    if (!name.trim()) { setError('分组名称不能为空'); return }
    setSaving(true)
    setError('')
    try {
      if (group) {
        await api.userGroupUpdate({ id: group.id, name: name.trim(), user_ids: userSel, link_group_ids: linkGroupSel })
      } else {
        await api.userGroupCreate({ name: name.trim(), user_ids: userSel, link_group_ids: linkGroupSel })
      }
      onSaved()
    } catch (err) {
      setError(errorMessage(err))
    } finally {
      setSaving(false)
    }
  }

  return (
    <Dialog open onOpenChange={(next) => !next && onClose()}>
      <DialogContent className="max-h-[85vh] overflow-y-auto">
        <DialogHeader>
          <DialogTitle>{group ? '编辑用户分组' : '新建用户分组'}</DialogTitle>
          <DialogDescription>组内用户不再直接分配链路，订阅内容由关联的链路分组派生。</DialogDescription>
        </DialogHeader>
        <div className="space-y-3">
          <div>
            <Label>分组名称</Label>
            <Input value={name} onChange={(e) => setName(e.target.value)} placeholder="例如：青铜会员" />
          </div>
          <div className="space-y-2">
            <Label>用户</Label>
            {users.length === 0 ? (
              <p className="text-sm text-muted-foreground">暂无用户。</p>
            ) : (
              users.map((u) => (
                <label key={u.id} className="flex cursor-pointer items-center gap-2 rounded-md border p-2 text-sm">
                  <input type="checkbox" className="size-4 accent-primary" checked={userSel.includes(u.id)}
                    onChange={(e) => setUserSel((cur) => (e.target.checked ? [...cur, u.id] : cur.filter((x) => x !== u.id)))} />
                  <span>{u.name}</span>
                </label>
              ))
            )}
          </div>
          <div className="space-y-2 border-t pt-3">
            <Label>关联链路分组</Label>
            {linkGroups.length === 0 ? (
              <p className="text-sm text-muted-foreground">暂无链路分组，请先在「链路分组」页创建。</p>
            ) : (
              linkGroups.map((g) => (
                <label key={g.id} className="flex cursor-pointer items-center gap-2 rounded-md border p-2 text-sm">
                  <input type="checkbox" className="size-4 accent-primary" checked={linkGroupSel.includes(g.id)}
                    onChange={(e) => setLinkGroupSel((cur) => (e.target.checked ? [...cur, g.id] : cur.filter((x) => x !== g.id)))} />
                  <span>{g.name}</span>
                  <span className="ml-auto text-xs text-muted-foreground">{g.chain_count} 链路 / {g.external_subscription_count} 外部订阅</span>
                </label>
              ))
            )}
          </div>
          {error && <p className="text-sm text-destructive">{error}</p>}
        </div>
        <DialogFooter>
          <Button onClick={onSave} disabled={saving}>{saving ? '保存中…' : '保存'}</Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}
