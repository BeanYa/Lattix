import { useCallback, useEffect, useMemo, useState } from 'react'
import { PlusIcon, Trash2Icon } from 'lucide-react'
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
import { EmptyState, LoadingState, Notice, Page, PageHeader } from '@/components/PagePrimitives'
import { api, errorMessage } from '@/lib/api'
import { buildLinkOptions } from '@/lib/links'
import { chainStatusStyle } from '@/lib/status'
import type {
  Chain,
  ChainStatus,
  ExternalSubscription,
  ExternalSubscriptionMode,
  LinkGroup,
} from '@/lib/types'
import { useAppDialog } from '@/lib/app-dialog'
import { humanizeBytes } from '@/lib/format'
import { useOperationProgress } from '@/lib/operation-progress-context'

import './groups.css'

const EXTERNAL_MODE_LABELS: Record<ExternalSubscriptionMode, string> = {
  stack: '叠加',
  merge: '并入',
  nodes: '仅节点',
}

function ModeSelect({
  value,
  disabled,
  onChange,
}: {
  value: ExternalSubscriptionMode
  disabled?: boolean
  onChange: (m: ExternalSubscriptionMode) => void
}) {
  return (
    <Select
      value={value}
      disabled={disabled}
      items={(Object.keys(EXTERNAL_MODE_LABELS) as ExternalSubscriptionMode[]).map((mode) => ({
        value: mode,
        label: EXTERNAL_MODE_LABELS[mode],
      }))}
      onValueChange={(v) => v && onChange(v as ExternalSubscriptionMode)}
    >
      <SelectTrigger className="h-7 w-24 text-xs">
        <SelectValue />
      </SelectTrigger>
      <SelectContent>
        {(Object.keys(EXTERNAL_MODE_LABELS) as ExternalSubscriptionMode[]).map((mode) => (
          <SelectItem key={mode} value={mode}>
            {EXTERNAL_MODE_LABELS[mode]}
          </SelectItem>
        ))}
      </SelectContent>
    </Select>
  )
}

export default function Groups() {
  const { confirm } = useAppDialog()
  const { showOperation } = useOperationProgress()
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
    if (
      !(await confirm({
        title: '删除链路分组',
        description: `确定删除「${g.name}」吗？`,
        confirmLabel: '删除',
        destructive: true,
      }))
    )
      return
    try {
      const { observeId } = await api.linkGroupDelete(g.id)
      if (observeId) showOperation({ observeId })
      void load()
    } catch (err) {
      setError(errorMessage(err))
    }
  }

  return (
    <Page className="cg-page-in">
      <PageHeader title="链路分组" description="把链路与外部订阅编排到一起。" />
      {error && <Notice tone="danger">{error}</Notice>}
      <section className="cg-groups-board">
        <header className="cg-groups-board-head">
          <span className="cg-groups-board-meta">
            {groups === null ? '同步中…' : `${groups.length} 分组`}
          </span>
          <Button className="cg-groups-create" onClick={() => setEditing('new')}>
            <PlusIcon />
            新建链路分组
          </Button>
        </header>
        {groups === null ? (
          <LoadingState className="cg-groups-board-state" />
        ) : groups.length === 0 ? (
          <EmptyState
            className="cg-groups-board-state"
            title="暂无链路分组"
            description="创建链路分组，把链路与外部订阅编排到一起。"
          />
        ) : (
          <div className="cg-groups-rows">
            {groups.map((g) => (
              <div
                key={g.id}
                className="cg-group-row"
                style={
                  {
                    '--group-line-color': `var(--panel-line-${g.id % 9})`,
                  } as React.CSSProperties
                }
              >
                <div className="cg-group-row-main">
                  <span
                    className="cg-line-badge"
                    style={{ background: `var(--panel-line-${g.id % 9})` }}
                  >
                    G{g.id}
                  </span>
                  <span className="cg-group-row-name">{g.name}</span>
                  <span className="cg-status is-blue">{g.chain_count} 链路</span>
                  <span className="cg-status is-blue">
                    {g.external_subscription_count} 外部订阅
                  </span>
                </div>
                <div className="cg-group-row-actions">
                  <Button variant="outline" size="sm" onClick={() => setEditing(g)}>
                    编辑
                  </Button>
                  <Button
                    variant="outline"
                    size="sm"
                    title="删除链路分组"
                    onClick={() => onDelete(g)}
                  >
                    <Trash2Icon className="size-4" />
                  </Button>
                </div>
              </div>
            ))}
          </div>
        )}
      </section>
      {editing && (
        <LinkGroupDialog
          group={editing === 'new' ? null : editing}
          linkOptions={linkOptions}
          extSubs={extSubs}
          saving={saving}
          setSaving={setSaving}
          onClose={() => setEditing(null)}
          onSaved={() => {
            setEditing(null)
            void load()
          }}
        />
      )}
    </Page>
  )
}

function LinkGroupDialog({
  group,
  linkOptions,
  extSubs,
  saving,
  setSaving,
  onClose,
  onSaved,
}: {
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
  const { showOperation } = useOperationProgress()

  function toggleChain(id: number, checked: boolean) {
    setChainSel((cur) => (checked ? [...cur, id] : cur.filter((x) => x !== id)))
  }

  async function onSave() {
    if (!name.trim()) {
      setError('分组名称不能为空')
      return
    }
    setSaving(true)
    setError('')
    try {
      const external_subscriptions = Object.entries(extSel).map(([sid, mode]) => ({
        subscription_id: Number(sid),
        mode,
      }))
      if (group) {
        const { observeId } = await api.linkGroupUpdate({
          id: group.id,
          name: name.trim(),
          chain_ids: chainSel,
          external_subscriptions,
        })
        if (observeId) showOperation({ observeId })
      } else {
        const { observeId } = await api.linkGroupCreate({
          name: name.trim(),
          chain_ids: chainSel,
          external_subscriptions,
        })
        if (observeId) showOperation({ observeId })
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
      <DialogContent className="max-h-[85vh] overflow-y-auto sm:max-w-2xl">
        <DialogHeader>
          <DialogTitle>{group ? '编辑链路分组' : '新建链路分组'}</DialogTitle>
          <DialogDescription>选择归入此分组的链路与外部订阅。</DialogDescription>
        </DialogHeader>
        <div className="space-y-3 min-w-0">
          <div>
            <Label>分组名称</Label>
            <Input
              value={name}
              onChange={(e) => setName(e.target.value)}
              placeholder="例如：旗舰线路"
            />
          </div>
          <div className="space-y-2">
            <Label>链路（仅支持带共享端点的链路）</Label>
            {linkOptions.length === 0 ? (
              <p className="cg-groups-hint">暂无链路，请先在「链路」页创建。</p>
            ) : (
              linkOptions.map((link) => (
                <label key={link.chainId} className="cg-groups-check-row">
                  <input
                    type="checkbox"
                    className="cg-groups-checkbox"
                    checked={chainSel.includes(link.chainId)}
                    onChange={(e) => toggleChain(link.chainId, e.target.checked)}
                  />
                  <span
                    className="cg-line-badge"
                    style={{ background: `var(--panel-line-${link.chainId % 9})` }}
                  >
                    L{link.chainId % 9}
                  </span>
                  <span className="cg-status is-blue">
                    {link.type === 'direct' ? '直连' : '中转'}
                  </span>
                  <span className="cg-groups-check-row-name">{link.name}</span>
                  <span className="cg-groups-check-row-detail ml-auto text-right">
                    {link.detail}
                  </span>
                  {link.status !== 'active' && (
                    <span className="cg-groups-check-row-detail">
                      （{chainStatusStyle[link.status as ChainStatus]?.label ?? link.status}）
                    </span>
                  )}
                </label>
              ))
            )}
          </div>
          <div className="space-y-2 border-t pt-3">
            <Label>外部订阅</Label>
            {extSubs.length === 0 ? (
              <p className="cg-groups-hint">暂无外部订阅，请先在「外部订阅」页添加。</p>
            ) : (
              extSubs.map((sub) => {
                const checked = extSel[sub.id] !== undefined
                return (
                  <label key={sub.id} className="cg-groups-check-row">
                    <input
                      type="checkbox"
                      className="cg-groups-checkbox"
                      checked={checked}
                      onChange={(e) =>
                        setExtSel((cur) => {
                          const next = { ...cur }
                          if (e.target.checked) next[sub.id] = 'stack'
                          else delete next[sub.id]
                          return next
                        })
                      }
                    />
                    <span className="cg-groups-check-row-name">{sub.name}</span>
                    <span className="cg-groups-check-row-detail">
                      {sub.total > 0
                        ? `${humanizeBytes(sub.total)} / 已用 ${humanizeBytes(sub.upload + sub.download)}`
                        : '额度未知'}
                    </span>
                    <span className="ml-auto">
                      <ModeSelect
                        value={checked ? extSel[sub.id] : 'stack'}
                        disabled={!checked}
                        onChange={(mode) => setExtSel((cur) => ({ ...cur, [sub.id]: mode }))}
                      />
                    </span>
                  </label>
                )
              })
            )}
          </div>
          {error && <Notice tone="danger">{error}</Notice>}
        </div>
        <DialogFooter>
          <Button onClick={onSave} disabled={saving}>
            {saving ? '保存中…' : '保存'}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}
