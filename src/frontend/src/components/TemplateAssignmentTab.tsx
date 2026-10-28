import { useEffect, useMemo, useState } from 'react'
import { ChevronDownIcon, ChevronUpIcon, ClipboardCheckIcon, XIcon } from 'lucide-react'

import { EmptyState, Notice } from '@/components/PagePrimitives'
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
import { useOperationProgress } from '@/lib/operation-progress-context'
import type {
  SubUser,
  SubscriptionDefaultConfig,
  SubscriptionRuleCategory,
  SubscriptionTemplate,
} from '@/lib/types'

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

export function TemplateAssignmentTab({
  users,
  templates,
  categories,
  onChanged,
}: TemplateAssignmentTabProps) {
  const { showOperation } = useOperationProgress()
  const [selected, setSelected] = useState<Set<number>>(new Set())
  const [dialogOpen, setDialogOpen] = useState(false)
  const [dialogSource, setDialogSource] = useState<'batch' | 'group'>('batch')
  const [ruleMode, setRuleMode] = useState<'suggested' | 'template'>('suggested')
  const [preset, setPreset] = useState<PresetId>('balanced')
  const [selectedCategories, setSelectedCategories] = useState<string[]>([])
  const [categoryOpen, setCategoryOpen] = useState(true)
  const [templateId, setTemplateId] = useState('')
  const [forced, setForced] = useState(false)
  const [regionGroupsEnabled, setRegionGroupsEnabled] = useState(true)
  const [saving, setSaving] = useState(false)
  const [error, setError] = useState('')
  // 已指派组卡片内的勾选（`${cardKey}:${userId}`），用于组内批量修改模板/取消覆盖。
  const [groupChecked, setGroupChecked] = useState<Set<string>>(new Set())
  // 默认级配置（全局兜底：面板级与用户级均缺席时生效）。编辑器内联在卡片上，
  // 保存按钮仅在配置相对已保存值发生变化时可用。
  const [defaultCfg, setDefaultCfg] = useState<SubscriptionDefaultConfig | null>(null)
  const [defaultMode, setDefaultMode] = useState<'suggested' | 'template'>('suggested')
  const [defaultCategories, setDefaultCategories] = useState<string[]>([])
  const [defaultTemplateId, setDefaultTemplateId] = useState('')
  const [defaultSaving, setDefaultSaving] = useState(false)
  const [defaultError, setDefaultError] = useState('')

  useEffect(() => {
    api
      .subscriptionTemplateDefault({ display: 'silent' })
      .then((cfg) => {
        setDefaultCfg(cfg)
        syncDefaultEditor(cfg)
      })
      .catch(() => setDefaultCfg({ suggested_categories: [], portable_template_id: '' }))
  }, [])

  const categoryById = useMemo(
    () => new Map(categories.map((category) => [category.id, category])),
    [categories],
  )

  const categoriesForPreset = (next: Exclude<PresetId, 'custom'>): string[] => {
    if (next === 'comprehensive') return categories.map((category) => category.id)
    return categories
      .filter((category) => (next === 'minimal' ? category.in_minimal : category.in_balanced))
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

  const unassigned = users.filter(
    (user) =>
      !SLOTS.some(([assignedField]) => user.routing[assignedField] as string) &&
      (user.routing.assigned_suggested_categories ?? []).length === 0,
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

  const toggleGroupChecked = (cardKey: string, id: number, checked: boolean) => {
    setGroupChecked((cur) => {
      const next = new Set(cur)
      const composite = `${cardKey}:${id}`
      if (checked) {
        next.add(composite)
      } else {
        next.delete(composite)
      }
      return next
    })
  }

  const groupCheckedUsers = (cardKey: string, ids: number[]) =>
    ids.filter((id) => groupChecked.has(`${cardKey}:${id}`))

  const clearGroupChecked = (cardKey: string, ids: number[]) => {
    setGroupChecked((cur) => {
      const next = new Set(cur)
      for (const id of ids) next.delete(`${cardKey}:${id}`)
      return next
    })
  }

  const toggleCategory = (id: string, checked: boolean) => {
    setSelectedCategories((cur) => (checked ? [...cur, id] : cur.filter((item) => item !== id)))
  }

  const resetDialogDefaults = () => {
    setError('')
    setRuleMode('suggested')
    setPreset('balanced')
    setSelectedCategories(categoriesForPreset('balanced'))
    setCategoryOpen(true)
    setTemplateId('')
    setForced(false)
    setRegionGroupsEnabled(true)
  }

  const openDialog = () => {
    resetDialogDefaults()
    setDialogSource('batch')
    setDialogOpen(true)
  }

  // 组卡片右上角「修改模板」：复用指派弹窗，预选该组勾选的用户。
  const openGroupDialog = (userIds: number[]) => {
    resetDialogDefaults()
    setDialogSource('group')
    setSelected(new Set(userIds))
    setDialogOpen(true)
  }

  const assign = async () => {
    if (selected.size === 0) return
    if (ruleMode === 'template' && !templateId) return
    if (ruleMode === 'suggested' && selectedCategories.length === 0) return
    setSaving(true)
    setError('')
    try {
      const target =
        ruleMode === 'template'
          ? { template_id: templateId }
          : { suggested_categories: selectedCategories }
      const { observeId } = await api.assignSubscriptionTemplate(
        [...selected],
        target,
        forced,
        regionGroupsEnabled,
      )
      if (observeId) showOperation({ observeId })
      setDialogOpen(false)
      setSelected(new Set())
      onChanged()
    } catch (err) {
      setError(errorMessage(err))
    } finally {
      setSaving(false)
    }
  }

  const unassign = async (
    user: SubUser,
    target: { template_id?: string; suggested_categories?: string[] },
  ) => {
    setError('')
    try {
      const { observeId } = await api.unassignSubscriptionTemplate([user.id], target)
      if (observeId) showOperation({ observeId })
      onChanged()
    } catch (err) {
      setError(errorMessage(err))
    }
  }

  const unassignTemplate = (user: SubUser, id: string) => unassign(user, { template_id: id })
  const unassignSuggested = (user: SubUser, ids: string[]) =>
    unassign(user, { suggested_categories: ids })

  // 取消覆盖：清除选中用户对应槽位的强制标记（强制级 → 面板级），指派保留为兜底。
  const unforceUsers = async (
    userIds: number[],
    target: { template_id?: string; suggested_categories?: string[] },
    cardKey: string,
  ) => {
    setError('')
    try {
      const { observeId } = await api.unforceSubscriptionTemplate(userIds, target)
      if (observeId) showOperation({ observeId })
      clearGroupChecked(cardKey, userIds)
      onChanged()
    } catch (err) {
      setError(errorMessage(err))
    }
  }

  const templateOf = (id: string) => templates.find((template) => template.id === id)

  const syncDefaultEditor = (cfg: SubscriptionDefaultConfig) => {
    if ((cfg.suggested_categories ?? []).length > 0) {
      setDefaultMode('suggested')
      setDefaultCategories([...cfg.suggested_categories])
      setDefaultTemplateId('')
    } else if (cfg.portable_template_id) {
      setDefaultMode('template')
      setDefaultCategories([])
      setDefaultTemplateId(cfg.portable_template_id)
    } else {
      setDefaultMode('suggested')
      setDefaultCategories([])
      setDefaultTemplateId('')
    }
  }

  // 编辑器当前值折算为待保存配置，与已保存值比较得出 dirty。
  const defaultDirty = useMemo(() => {
    if (!defaultCfg) return false
    const draftCategories = defaultMode === 'suggested' ? defaultCategories : []
    const draftPortable = defaultMode === 'template' ? defaultTemplateId : ''
    const savedCategories = defaultCfg.suggested_categories ?? []
    if (draftCategories.length !== savedCategories.length) return true
    if (draftCategories.some((id, index) => id !== savedCategories[index])) return true
    return draftPortable !== defaultCfg.portable_template_id
  }, [defaultCfg, defaultMode, defaultCategories, defaultTemplateId])

  const defaultDraft = (): SubscriptionDefaultConfig =>
    defaultMode === 'suggested'
      ? { suggested_categories: defaultCategories, portable_template_id: '' }
      : { suggested_categories: [], portable_template_id: defaultTemplateId }

  const saveDefault = async () => {
    setDefaultSaving(true)
    setDefaultError('')
    try {
      const { observeId } = await api.saveSubscriptionTemplateDefault(defaultDraft())
      if (observeId) showOperation({ observeId })
      const { suggested_categories, portable_template_id } = await api.subscriptionTemplateDefault()
      const saved = { suggested_categories, portable_template_id }
      setDefaultCfg(saved)
      syncDefaultEditor(saved)
    } catch (err) {
      setDefaultError(errorMessage(err))
    } finally {
      setDefaultSaving(false)
    }
  }

  return (
    <div className="space-y-4">
      <div className="cg-card cg-assign-group is-dashed">
        <div className="cg-assign-group-head">
          <span className="cg-assign-group-name">默认级配置</span>
          <span className="cg-status">最低优先级</span>
          <span className="cg-assign-group-actions">
            {defaultError && <span className="cg-status is-red">{defaultError}</span>}
            <Button
              size="xs"
              onClick={saveDefault}
              disabled={!defaultDirty || defaultSaving}
              title={defaultDirty ? '保存默认级配置' : '配置未修改'}
            >
              {defaultSaving ? '保存中…' : '保存默认级'}
            </Button>
          </span>
        </div>
        <div className="mt-2 space-y-2">
          <div className="flex flex-wrap items-center gap-2">
            <Button
              type="button"
              size="xs"
              variant={defaultMode === 'suggested' ? 'default' : 'outline'}
              onClick={() => setDefaultMode('suggested')}
            >
              建议规则
            </Button>
            <Button
              type="button"
              size="xs"
              variant={defaultMode === 'template' ? 'default' : 'outline'}
              onClick={() => setDefaultMode('template')}
            >
              使用模板
            </Button>
            {defaultMode === 'template' ? (
              <span className="cg-hint">
                {defaultTemplateId
                  ? `模板「${templateOf(defaultTemplateId)?.name ?? defaultTemplateId}」`
                  : '未选择模板（= 清除默认级）'}
              </span>
            ) : defaultCategories.length === 0 ? (
              <span className="cg-hint">未选分组（= 清除默认级）</span>
            ) : (
              <span className="cg-hint">已选 {defaultCategories.length} 个分组</span>
            )}
          </div>
          {defaultMode === 'suggested' ? (
            <div className="grid gap-2 sm:grid-cols-2 lg:grid-cols-4">
              {categories.map((category) => (
                <label key={category.id} className="cg-check-row">
                  <input
                    type="checkbox"
                    className="cg-checkbox"
                    checked={defaultCategories.includes(category.id)}
                    onChange={(event) =>
                      setDefaultCategories((cur) =>
                        event.target.checked
                          ? [...cur, category.id]
                          : cur.filter((item) => item !== category.id),
                      )
                    }
                  />
                  <span aria-hidden="true">{category.icon}</span>
                  <span className="min-w-0 break-words">{category.label}</span>
                </label>
              ))}
            </div>
          ) : (
            <div className="max-w-md">
              <Select
                value={defaultTemplateId}
                onValueChange={(id) => setDefaultTemplateId(id ?? '')}
              >
                <SelectTrigger className="w-full">
                  <SelectValue placeholder="选择模板（清空 = 清除默认级）" />
                </SelectTrigger>
                <SelectContent>
                  {(['portable', 'acl4ssr'] as const).map((kind) =>
                    assignable
                      .filter((template) => template.kind === kind && template.content)
                      .map((template) => (
                        <SelectItem key={template.id} value={template.id}>
                          {template.name}（主策略）
                        </SelectItem>
                      )),
                  )}
                </SelectContent>
              </Select>
            </div>
          )}
          <p className="cg-hint">
            初始默认值：仅当面板未为用户配置模板（面板级/强制级均无）且用户未在订阅页自选（用户级为空）时生效。
          </p>
        </div>
      </div>

      <div className="cg-card cg-assign-panel">
        <div className="min-w-0 flex-1">
          <Label>指派用户（勾选后批量指派模板）</Label>
          <div className="cg-assign-users">
            {users.length === 0 ? (
              <p className="cg-hint p-2">暂无用户</p>
            ) : (
              users.map((user) => (
                <label key={user.id} className="cg-assign-user-row">
                  <input
                    type="checkbox"
                    className="cg-checkbox"
                    checked={selected.has(user.id)}
                    onChange={(event) => toggle(user.id, event.target.checked)}
                  />
                  <span className="truncate">{user.name}</span>
                  {user.disabled && <span className="cg-status is-red">已停用</span>}
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
          const cardKey = `s:${key}`
          const checkedIds = groupCheckedUsers(
            cardKey,
            groupUsers.map((user) => user.id),
          )
          return (
            <div key={key} className="cg-card cg-assign-group">
              <div className="cg-assign-group-head">
                <span className="cg-assign-group-name">建议规则 · {suggestedTitle(ids)}</span>
                <span className="cg-status is-blue">主策略</span>
                <span className="cg-assign-group-count">{groupUsers.length} 个用户</span>
                {checkedIds.length > 0 && (
                  <span className="cg-assign-group-actions">
                    <Button size="xs" onClick={() => openGroupDialog(checkedIds)}>
                      修改模板（{checkedIds.length}）
                    </Button>
                    <Button
                      size="xs"
                      variant="outline"
                      title="取消强制覆盖：指派保留为面板级默认，用户自选立即生效"
                      onClick={() =>
                        unforceUsers(checkedIds, { suggested_categories: ids }, cardKey)
                      }
                    >
                      取消覆盖（{checkedIds.length}）
                    </Button>
                  </span>
                )}
              </div>
              <div className="cg-assign-chips">
                {groupUsers.map((user) => (
                  <span key={user.id} className="cg-pill">
                    <input
                      type="checkbox"
                      className="cg-checkbox"
                      aria-label={`勾选 ${user.name}`}
                      checked={groupChecked.has(`${cardKey}:${user.id}`)}
                      onChange={(event) =>
                        toggleGroupChecked(cardKey, user.id, event.target.checked)
                      }
                    />
                    {user.name}
                    {user.routing.assign_forced_portable && (
                      <span className="cg-status is-red">强制</span>
                    )}
                    {user.routing.region_groups_enabled === false && (
                      <span className="cg-status">地区分组关闭</span>
                    )}
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
            const cardKey = `t:${id}`
            const checkedIds = groupCheckedUsers(
              cardKey,
              entries.map((entry) => entry.user.id),
            )
            return (
              <div key={id} className="cg-card cg-assign-group">
                <div className="cg-assign-group-head">
                  <span className="cg-assign-group-name">{template?.name ?? id}</span>
                  {template && (
                    <span className="cg-status is-blue">{KIND_LABELS[template.kind]}</span>
                  )}
                  <span className="cg-assign-group-count">{entries.length} 个用户</span>
                  {checkedIds.length > 0 && (
                    <span className="cg-assign-group-actions">
                      <Button size="xs" onClick={() => openGroupDialog(checkedIds)}>
                        修改模板（{checkedIds.length}）
                      </Button>
                      <Button
                        size="xs"
                        variant="outline"
                        title="取消强制覆盖：指派保留为面板级默认，用户自选立即生效"
                        onClick={() => unforceUsers(checkedIds, { template_id: id }, cardKey)}
                      >
                        取消覆盖（{checkedIds.length}）
                      </Button>
                    </span>
                  )}
                </div>
                <div className="cg-assign-chips">
                  {entries.map(({ user, forced: isForced }) => (
                    <span key={user.id} className="cg-pill">
                      <input
                        type="checkbox"
                        className="cg-checkbox"
                        aria-label={`勾选 ${user.name}`}
                        checked={groupChecked.has(`${cardKey}:${user.id}`)}
                        onChange={(event) =>
                          toggleGroupChecked(cardKey, user.id, event.target.checked)
                        }
                      />
                      {user.name}
                      {isForced && <span className="cg-status is-red">强制</span>}
                      {user.routing.region_groups_enabled === false && (
                        <span className="cg-status">地区分组关闭</span>
                      )}
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
          <div className="cg-card cg-assign-group is-dashed">
            <div className="cg-assign-group-head">
              <span className="cg-assign-group-name">未指派</span>
              <span className="cg-assign-group-count">{unassigned.length} 个用户</span>
            </div>
            <div className="cg-assign-chips">
              {unassigned.map((user) => (
                <span key={user.id} className="cg-pill">
                  {user.name}
                  {user.disabled && <span className="cg-status is-red">已停用</span>}
                </span>
              ))}
            </div>
          </div>
        )}
        {assignedUsers.size === 0 && unassigned.length === 0 && users.length === 0 ? (
          <EmptyState
            icon={<ClipboardCheckIcon />}
            title="暂无用户"
            description="先创建用户，再指派模板"
          />
        ) : null}
        {assignedUsers.size === 0 && users.length > 0 ? (
          <EmptyState
            icon={<ClipboardCheckIcon />}
            title="暂无模板指派"
            description="勾选上方用户后指派模板"
          />
        ) : null}
      </div>

      <Dialog open={dialogOpen} onOpenChange={(next) => !next && setDialogOpen(false)}>
        <DialogContent className="max-w-lg">
          <DialogHeader>
            <DialogTitle>{dialogSource === 'group' ? '修改模板' : '指派模板'}</DialogTitle>
            <DialogDescription>
              为选中的 {selected.size} 个用户
              {dialogSource === 'group' ? '更换' : '指派'}
              模板；未强制时用户自选优先，强制后覆盖用户配置。
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
                    <SelectTrigger className="w-full">
                      <SelectValue />
                    </SelectTrigger>
                    <SelectContent>
                      {PRESET_OPTIONS.map(([id, label]) => (
                        <SelectItem key={id} value={id}>
                          {label}
                        </SelectItem>
                      ))}
                    </SelectContent>
                  </Select>
                  <p className="cg-hint">
                    {preset === 'custom' && '自定义选择需要的规则类别'}
                    {preset === 'minimal' && '已自动选择基础规则，可以手动调整'}
                    {preset === 'balanced' && '已自动选择常用规则，可以手动调整'}
                    {preset === 'comprehensive' && '已自动选择所有规则，可以手动调整'}
                  </p>
                </div>
                <details
                  open={categoryOpen}
                  onToggle={(event) => setCategoryOpen(event.currentTarget.open)}
                  className="cg-routing-details"
                >
                  <summary className="flex cursor-pointer list-none items-center gap-2">
                    <span>生效分组</span>
                    <span className="cg-hint">已选择 {selectedCategories.length} 个类别</span>
                    <span className="ml-auto">
                      {categoryOpen ? (
                        <ChevronUpIcon className="size-4" />
                      ) : (
                        <ChevronDownIcon className="size-4" />
                      )}
                    </span>
                  </summary>
                  <div className="mt-2 grid gap-2 sm:grid-cols-2 lg:grid-cols-3">
                    {categories.map((category) => (
                      <label key={category.id} className="cg-check-row">
                        <input
                          type="checkbox"
                          className="cg-checkbox"
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
                  <SelectTrigger className="w-full">
                    <SelectValue placeholder="选择模板" />
                  </SelectTrigger>
                  <SelectContent>
                    {(['portable', 'acl4ssr', 'mihomo', 'singbox', 'quanx'] as const).map(
                      (kind) => {
                        const items = assignable.filter((template) => template.kind === kind)
                        if (items.length === 0) return null
                        return items.map((template) => (
                          <SelectItem key={template.id} value={template.id}>
                            {template.name}（{KIND_LABELS[kind]}）
                          </SelectItem>
                        ))
                      },
                    )}
                  </SelectContent>
                </Select>
                {assignable.length === 0 && (
                  <p className="cg-hint">模板缓存为空时可先指派建议规则。</p>
                )}
              </div>
            )}

            <div className="space-y-1">
              <label className="cg-check-row">
                <input
                  type="checkbox"
                  role="switch"
                  className="cg-checkbox"
                  checked={regionGroupsEnabled}
                  onChange={(event) => setRegionGroupsEnabled(event.target.checked)}
                  aria-describedby="assignment-region-groups-hint"
                  disabled={saving}
                />
                开启地区分组
              </label>
              <p id="assignment-region-groups-hint" className="cg-hint">
                按节点地区自动添加分组（含无地区分组）；关闭后保留节点和来源分组。
              </p>
            </div>
            <label className="cg-check-row">
              <input
                type="checkbox"
                className="cg-checkbox"
                checked={forced}
                onChange={(event) => setForced(event.target.checked)}
              />
              强制覆盖用户自选（指派后用户自选选项失效，显示跟随指派）
            </label>
          </div>
          {error && <Notice tone="danger">{error}</Notice>}
          <DialogFooter>
            <Button
              onClick={assign}
              disabled={
                saving ||
                selected.size === 0 ||
                (ruleMode === 'template' ? !templateId : selectedCategories.length === 0)
              }
            >
              {saving ? '指派中…' : '指派'}
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>
    </div>
  )
}
