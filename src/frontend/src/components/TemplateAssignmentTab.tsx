import { useMemo, useState } from 'react'
import { ClipboardCheckIcon, XIcon } from 'lucide-react'

import { EmptyState, Notice } from '@/components/PagePrimitives'
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
import { Label } from '@/components/ui/label'
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from '@/components/ui/select'
import { api, errorMessage } from '@/lib/api'
import type { SubUser, SubscriptionTemplate } from '@/lib/types'

const SLOTS = [
  ['assigned_portable_template_id', 'assign_forced_portable'],
  ['assigned_mihomo_template_id', 'assign_forced_mihomo'],
  ['assigned_singbox_template_id', 'assign_forced_singbox'],
  ['assigned_quanx_template_id', 'assign_forced_quanx'],
] as const

const KIND_LABELS: Record<SubscriptionTemplate['kind'], string> = {
  portable: '主策略',
  acl4ssr: '主策略',
  mihomo: 'Mihomo',
  singbox: 'Sing-box',
  quanx: 'Quantumult X',
}

interface TemplateAssignmentTabProps {
  users: SubUser[]
  templates: SubscriptionTemplate[]
  onChanged: () => void
}

export function TemplateAssignmentTab({ users, templates, onChanged }: TemplateAssignmentTabProps) {
  const [selected, setSelected] = useState<Set<number>>(new Set())
  const [dialogOpen, setDialogOpen] = useState(false)
  const [templateId, setTemplateId] = useState('')
  const [forced, setForced] = useState(false)
  const [saving, setSaving] = useState(false)
  const [error, setError] = useState('')

  const assignable = templates.filter((template) => template.content)
  const assignedUsers = useMemo(() => {
    const byTemplate = new Map<string, Array<{ user: SubUser; forced: boolean }>>()
    for (const user of users) {
      for (const [assignedField, forcedField] of SLOTS) {
        const id = user.routing[assignedField] as string
        if (!id) continue
        const entries = byTemplate.get(id) ?? []
        entries.push({ user, forced: Boolean(user.routing[forcedField]) })
        byTemplate.set(id, entries)
      }
    }
    return byTemplate
  }, [users])

  const unassigned = users.filter((user) => !SLOTS.some(([assignedField]) => user.routing[assignedField] as string))

  const toggle = (id: number, checked: boolean) => {
    setSelected((cur) => {
      const next = new Set(cur)
      if (checked) {
        next.add(id)
      } else {
        next.delete(id)
      }
      return next
    })
  }

  const openDialog = () => {
    setError('')
    setTemplateId('')
    setForced(false)
    setDialogOpen(true)
  }

  const assign = async () => {
    if (!templateId || selected.size === 0) return
    setSaving(true)
    setError('')
    try {
      await api.assignSubscriptionTemplate([...selected], templateId, forced)
      setDialogOpen(false)
      setSelected(new Set())
      onChanged()
    } catch (err) {
      setError(errorMessage(err))
    } finally {
      setSaving(false)
    }
  }

  const unassign = async (user: SubUser, id: string) => {
    setError('')
    try {
      await api.unassignSubscriptionTemplate([user.id], id)
      onChanged()
    } catch (err) {
      setError(errorMessage(err))
    }
  }

  const templateOf = (id: string) => templates.find((template) => template.id === id)

  return (
    <div className="space-y-4">
      <div className="flex flex-wrap items-end gap-3 rounded-lg border p-3">
        <div className="min-w-0 flex-1">
          <Label>指派用户（勾选后批量指派模板）</Label>
          <div className="mt-2 grid max-h-56 gap-1 overflow-y-auto rounded-md border p-2">
            {users.length === 0 ? (
              <p className="p-2 text-sm text-muted-foreground">暂无用户</p>
            ) : (
              users.map((user) => (
                <label key={user.id} className="flex cursor-pointer items-center gap-2 rounded px-2 py-1 text-sm hover:bg-muted/60">
                  <input
                    type="checkbox"
                    className="size-4 shrink-0 accent-primary"
                    checked={selected.has(user.id)}
                    onChange={(event) => toggle(user.id, event.target.checked)}
                  />
                  <span className="truncate">{user.name}</span>
                  {user.disabled && <Badge variant="destructive">已停用</Badge>}
                </label>
              ))
            )}
          </div>
        </div>
        <Button onClick={openDialog} disabled={selected.size === 0 || assignable.length === 0}>
          <ClipboardCheckIcon />
          指派模板（{selected.size}）
        </Button>
      </div>

      {error && <Notice tone="danger">{error}</Notice>}

      <div className="space-y-3">
        {[...assignedUsers.entries()]
          .sort(([a], [b]) => (templateOf(a)?.name ?? a).localeCompare(templateOf(b)?.name ?? b))
          .map(([id, entries]) => {
            const template = templateOf(id)
            return (
              <div key={id} className="rounded-lg border p-3">
                <div className="flex items-center gap-2">
                  <span className="font-medium">{template?.name ?? id}</span>
                  {template && <Badge variant="secondary">{KIND_LABELS[template.kind]}</Badge>}
                  <span className="ml-auto text-xs text-muted-foreground">{entries.length} 个用户</span>
                </div>
                <div className="mt-2 flex flex-wrap gap-2">
                  {entries.map(({ user, forced: isForced }) => (
                    <span key={user.id} className="inline-flex items-center gap-1 rounded-md border px-2 py-1 text-sm">
                      {user.name}
                      {isForced && <Badge variant="destructive">强制</Badge>}
                      <Button
                        variant="ghost"
                        size="icon-xs"
                        title="取消该用户的此模板指派"
                        onClick={() => unassign(user, id)}
                      >
                        <XIcon />
                      </Button>
                    </span>
                  ))}
                </div>
              </div>
            )
          })}
        {unassigned.length > 0 && (
          <div className="rounded-lg border border-dashed p-3">
            <div className="flex items-center gap-2">
              <span className="font-medium">未指派</span>
              <span className="ml-auto text-xs text-muted-foreground">{unassigned.length} 个用户</span>
            </div>
            <div className="mt-2 flex flex-wrap gap-2">
              {unassigned.map((user) => (
                <span key={user.id} className="inline-flex items-center gap-1 rounded-md border px-2 py-1 text-sm">
                  {user.name}
                  {user.disabled && <Badge variant="destructive">已停用</Badge>}
                </span>
              ))}
            </div>
          </div>
        )}
        {assignedUsers.size === 0 && unassigned.length === 0 && users.length === 0 ? (
          <EmptyState icon={<ClipboardCheckIcon />} title="暂无用户" description="先创建用户，再指派模板" />
        ) : null}
        {assignedUsers.size === 0 && users.length > 0 ? (
          <EmptyState icon={<ClipboardCheckIcon />} title="暂无模板指派" description="勾选上方用户后指派模板" />
        ) : null}
      </div>

      <Dialog open={dialogOpen} onOpenChange={(next) => !next && setDialogOpen(false)}>
        <DialogContent className="max-w-md">
          <DialogHeader>
            <DialogTitle>指派模板</DialogTitle>
            <DialogDescription>
              为选中的 {selected.size} 个用户指派模板；未强制时用户自选优先，强制后用户自选失效。
            </DialogDescription>
          </DialogHeader>
          <div className="space-y-3">
            <div className="space-y-2">
              <Label>模板（按类型分组）</Label>
              <Select value={templateId} onValueChange={(id) => id && setTemplateId(id)}>
                <SelectTrigger className="w-full"><SelectValue placeholder="选择模板" /></SelectTrigger>
                <SelectContent>
                  {(['portable', 'acl4ssr', 'mihomo', 'singbox', 'quanx'] as const).map((kind) => {
                    const items = assignable.filter((template) => template.kind === kind)
                    if (items.length === 0) return null
                    return items.map((template) => (
                      <SelectItem key={template.id} value={template.id}>
                        {template.name}（{KIND_LABELS[kind]}）
                      </SelectItem>
                    ))
                  })}
                </SelectContent>
              </Select>
              {assignable.length === 0 && (
                <p className="text-xs text-muted-foreground">暂无可用模板，请先在「订阅模板」页创建或刷新缓存。</p>
              )}
            </div>
            <label className="flex cursor-pointer items-center gap-2 rounded-md border px-3 py-2 text-sm">
              <input
                type="checkbox"
                className="size-4 shrink-0 accent-primary"
                checked={forced}
                onChange={(event) => setForced(event.target.checked)}
              />
              强制覆盖用户自选（指派后用户自选选项失效，显示跟随指派）
            </label>
          </div>
          {error && <p className="text-sm text-destructive">{error}</p>}
          <DialogFooter>
            <Button onClick={assign} disabled={saving || !templateId || selected.size === 0}>
              {saving ? '指派中…' : '指派'}
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>
    </div>
  )
}
