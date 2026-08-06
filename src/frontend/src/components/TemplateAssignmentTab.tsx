import { useMemo, useState } from 'react'
import { ChevronDownIcon, ChevronUpIcon, ClipboardCheckIcon, XIcon } from 'lucide-react'

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
import type { SubUser, SubscriptionRuleCategory, SubscriptionTemplate } from '@/lib/types'

const SLOTS = [
  ['assigned_portable_template_id', 'assign_forced_portable'],
  ['assigned_mihomo_template_id', 'assign_forced_mihomo'],
  ['assigned_singbox_template_id', 'assign_forced_singbox'],
  ['assigned_quanx_template_id', 'assign_forced_quanx'],
] as const

const PRESET_OPTIONS = [
  ['custom', '自定义'],
  ['minimal', '极简规则'],
  ['balanced', '均衡规则（推荐）'],
  ['comprehensive', '完整规则'],
] as const

type PresetId = (typeof PRESET_OPTIONS)[number][0]

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
  categories: SubscriptionRuleCategory[]
  onChanged: () => void
}

export function TemplateAssignmentTab({ users, templates, categories, onChanged }: TemplateAssignmentTabProps) {
  const [selected, setSelected] = useState<Set<number>>(new Set())
  const [dialogOpen, setDialogOpen] = useState(false)
  const [ruleMode, setRuleMode] = useState<'suggested' | 'template'>('suggested')
  const [preset, setPreset] = useState<PresetId>('balanced')
  const [selectedCategories, setSelectedCategories] = useState<string[]>([])
  const [categoryOpen, setCategoryOpen] = useState(true)
  const [templateId, setTemplateId] = useState('')
  const [forced, setForced] = useState(false)
  const [saving, setSaving] = useState(false)
  const [error, setError] = useState('')

  const categoryById = useMemo(() => new Map(categories.map((category) => [category.id, category])), [categories])

  const categoriesForPreset = (next: Exclude<PresetId, 'custom'>): string[] => {
    if (next === 'comprehensive') return categories.map((category) => category.id)
    return categories
      .filter((category) => next === 'minimal' ? category.in_minimal : category.in_balanced)
      .map((category) => category.id)
  }

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

  const suggestedBySet = useMemo(() => {
    const groups = new Map<string, SubUser[]>()
    for (const user of users) {
      const ids = user.routing.assigned_suggested_categories ?? []
      if (ids.length === 0) continue
      const key = JSON.stringify(ids)
      const list = groups.get(key) ?? []
      list.push(user)
      groups.set(key, list)
    }
    return groups
  }, [users])

  const suggestedTitle = (ids: string[]) => {
    const labels = ids.map((id) => {
      const category = categoryById.get(id)
      return category ? `${category.icon} ${category.label}` : id
    })
    return labels.length === 0 ? '未指定分组' : labels.join('、')
  }

  const unassigned = users.filter((user) =>
    !SLOTS.some(([assignedField]) => user.routing[assignedField] as string)
    && (user.routing.assigned_suggested_categories ?? []).length === 0,
  )

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

  const toggleCategory = (id: string, checked: boolean) => {
    setSelectedCategories((cur) => (checked ? [...cur, id] : cur.filter((item) => item !== id)))
  }

  const openDialog = () => {
    setError('')
    setRuleMode('suggested')
    setPreset('balanced')
    setSelectedCategories(categoriesForPreset('balanced'))
    setCategoryOpen(true)
    setTemplateId('')
    setForced(false)
    setDialogOpen(true)
  }

  const assign = async () => {
    if (selected.size === 0) return
    if (ruleMode === 'template' && !templateId) return
    if (ruleMode === 'suggested' && selectedCategories.length === 0) return
    setSaving(true)
    setError('')
    try {
      const target = ruleMode === 'template'
        ? { template_id: templateId }
        : { suggested_categories: selectedCategories }
      await api.assignSubscriptionTemplate([...selected], target, forced)
      setDialogOpen(false)
      setSelected(new Set())
      onChanged()
    } catch (err) {
      setError(errorMessage(err))
    } finally {
      setSaving(false)
    }
  }

  const unassign = async (user: SubUser, target: { template_id?: string; suggested_categories?: string[] }) => {
    setError('')
    try {
      await api.unassignSubscriptionTemplate([user.id], target)
      onChanged()
    } catch (err) {
      setError(errorMessage(err))
    }
  }

  const unassignTemplate = (user: SubUser, id: string) => unassign(user, { template_id: id })
  const unassignSuggested = (user: SubUser, ids: string[]) => unassign(user, { suggested_categories: ids })

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
        <Button onClick={openDialog} disabled={selected.size === 0}>
          <ClipboardCheckIcon />
          指派模板（{selected.size}）
        </Button>
      </div>

      {error && <Notice tone="danger">{error}</Notice>}

      <div className="space-y-3">
        {[...suggestedBySet.entries()].map(([key, groupUsers]) => {
          const ids = JSON.parse(key) as string[]
          return (
            <div key={key} className="rounded-lg border p-3">
              <div className="flex items-center gap-2">
                <span className="font-medium">建议规则 · {suggestedTitle(ids)}</span>
                <Badge variant="secondary">主策略</Badge>
                <span className="ml-auto text-xs text-muted-foreground">{groupUsers.length} 个用户</span>
              </div>
              <div className="mt-2 flex flex-wrap gap-2">
                {groupUsers.map((user) => (
                  <span key={user.id} className="inline-flex items-center gap-1 rounded-md border px-2 py-1 text-sm">
                    {user.name}
                    {user.routing.assign_forced_portable && <Badge variant="destructive">强制</Badge>}
                    <Button
                      variant="ghost"
                      size="icon-xs"
                      title="取消该用户的建议规则指派"
                      onClick={() => unassignSuggested(user, ids)}
                    >
                      <XIcon />
                    </Button>
                  </span>
                ))}
              </div>
            </div>
          )
        })}
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
                        onClick={() => unassignTemplate(user, id)}
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
        <DialogContent className="max-w-lg">
          <DialogHeader>
            <DialogTitle>指派模板</DialogTitle>
            <DialogDescription>
              为选中的 {selected.size} 个用户指派模板；未强制时用户自选优先，强制后用户自选失效。
            </DialogDescription>
          </DialogHeader>
          <div className="space-y-3">
            <div className="flex gap-2">
              <Button
                type="button"
                variant={ruleMode === 'suggested' ? 'default' : 'outline'}
                className="flex-1"
                onClick={() => setRuleMode('suggested')}
              >
                建议规则
              </Button>
              <Button
                type="button"
                variant={ruleMode === 'template' ? 'default' : 'outline'}
                className="flex-1"
                onClick={() => setRuleMode('template')}
              >
                使用模板
              </Button>
            </div>

            {ruleMode === 'suggested' ? (
              <>
                <div className="space-y-2">
                  <Label>规则选择</Label>
                  <Select
                    value={preset}
                    onValueChange={(value) => {
                      if (!value) return
                      const next = value as PresetId
                      setPreset(next)
                      if (next !== 'custom') setSelectedCategories(categoriesForPreset(next))
                    }}
                  >
                    <SelectTrigger className="w-full"><SelectValue /></SelectTrigger>
                    <SelectContent>
                      {PRESET_OPTIONS.map(([id, label]) => <SelectItem key={id} value={id}>{label}</SelectItem>)}
                    </SelectContent>
                  </Select>
                  <p className="text-xs text-muted-foreground">
                    {preset === 'custom' && '自定义选择需要的规则类别'}
                    {preset === 'minimal' && '已自动选择基础规则，可以手动调整'}
                    {preset === 'balanced' && '已自动选择常用规则，可以手动调整'}
                    {preset === 'comprehensive' && '已自动选择所有规则，可以手动调整'}
                  </p>
                </div>
                <details
                  open={categoryOpen}
                  onToggle={(event) => setCategoryOpen(event.currentTarget.open)}
                  className="rounded-md border p-3"
                >
                  <summary className="flex cursor-pointer list-none items-center gap-2 text-sm font-medium">
                    <span>生效分组</span>
                    <span className="text-xs text-muted-foreground">已选择 {selectedCategories.length} 个类别</span>
                    <span className="ml-auto">
                      {categoryOpen ? <ChevronUpIcon className="size-4" /> : <ChevronDownIcon className="size-4" />}
                    </span>
                  </summary>
                  <div className="mt-2 grid gap-2 sm:grid-cols-2 lg:grid-cols-3">
                    {categories.map((category) => (
                      <label key={category.id} className="flex min-w-0 cursor-pointer items-center gap-2 rounded-md border px-3 py-2 text-sm">
                        <input
                          type="checkbox"
                          className="size-4 shrink-0 accent-primary"
                          checked={selectedCategories.includes(category.id)}
                          onChange={(event) => toggleCategory(category.id, event.target.checked)}
                        />
                        <span aria-hidden="true">{category.icon}</span>
                        <span className="min-w-0 break-words">{category.label}</span>
                      </label>
                    ))}
                  </div>
                </details>
              </>
            ) : (
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
                  <p className="text-xs text-muted-foreground">模板缓存为空时可先指派建议规则。</p>
                )}
              </div>
            )}

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
            <Button
              onClick={assign}
              disabled={saving || selected.size === 0 || (ruleMode === 'template' ? !templateId : selectedCategories.length === 0)}
            >
              {saving ? '指派中…' : '指派'}
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>
    </div>
  )
}
