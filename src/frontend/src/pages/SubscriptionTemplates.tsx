import { useCallback, useEffect, useState, type FormEvent } from 'react'
import {
  CopyIcon,
  EyeIcon,
  FileCode2Icon,
  PlusIcon,
  RefreshCwIcon,
  Trash2Icon,
} from 'lucide-react'

import { EmptyState, Notice, Page, PageHeader, Surface } from '@/components/PagePrimitives'
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
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from '@/components/ui/table'
import { api, errorMessage } from '@/lib/api'
import { useAppDialog } from '@/lib/app-dialog'
import { formatDateTime } from '@/lib/format'
import { useOperationProgress } from '@/lib/operation-progress-context'
import { useTimezone } from '@/lib/timezone'
import type { SubscriptionTemplate } from '@/lib/types'

const kinds: Array<{ value: SubscriptionTemplate['kind']; label: string }> = [
  { value: 'portable', label: 'Lattix 中立 YAML' },
  { value: 'acl4ssr', label: 'ACL4SSR INI' },
  { value: 'mihomo', label: 'Mihomo YAML' },
  { value: 'singbox', label: 'Sing-box JSON' },
  { value: 'quanx', label: 'Quantumult X CONF' },
]

export default function SubscriptionTemplates() {
  const { timezone } = useTimezone()
  const { confirm } = useAppDialog()
  const { showOperation } = useOperationProgress()
  const [templates, setTemplates] = useState<SubscriptionTemplate[]>([])
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState('')
  const [refreshing, setRefreshing] = useState('')
  const [editing, setEditing] = useState<SubscriptionTemplate | null>(null)
  const [preview, setPreview] = useState<SubscriptionTemplate | null>(null)
  const [open, setOpen] = useState(false)
  const [name, setName] = useState('')
  const [kind, setKind] = useState<SubscriptionTemplate['kind']>('portable')
  const [sourceURL, setSourceURL] = useState('')
  const [content, setContent] = useState('')
  const [license, setLicense] = useState('')
  const [saving, setSaving] = useState(false)

  const load = useCallback(async () => {
    try {
      setTemplates(await api.subscriptionTemplates())
    } catch (err) {
      setError(errorMessage(err))
    } finally {
      setLoading(false)
    }
  }, [])

  useEffect(() => { void load() }, [load])

  const resetForm = () => {
    setEditing(null)
    setName('')
    setKind('portable')
    setSourceURL('')
    setContent('')
    setLicense('')
  }

  const beginEdit = (template?: SubscriptionTemplate) => {
    resetForm()
    if (template) {
      setEditing(template)
      setName(template.name)
      setKind(template.kind)
      setSourceURL(template.source_url)
      setContent(template.content ?? '')
      setLicense(template.license)
    }
    setOpen(true)
  }

  const save = async (event: FormEvent) => {
    event.preventDefault()
    setSaving(true)
    setError('')
    try {
      await api.saveSubscriptionTemplate({
        id: editing?.id,
        name: name.trim(),
        kind,
        source_url: sourceURL.trim(),
        content,
        license: license.trim(),
      })
      setOpen(false)
      resetForm()
      await load()
    } catch (err) {
      setError(errorMessage(err))
    } finally {
      setSaving(false)
    }
  }

  const refresh = async (id = '') => {
    setRefreshing(id || 'all')
    setError('')
    try {
      const { observeId } = await api.refreshSubscriptionTemplates(id)
      if (observeId) showOperation({ observeId })
    } catch (err) {
      setError(errorMessage(err))
    } finally {
      await load()
      setRefreshing('')
    }
  }

  const clone = async (template: SubscriptionTemplate) => {
    try {
      const copy = await api.cloneSubscriptionTemplate(template.id)
      await load()
      beginEdit(copy)
    } catch (err) {
      setError(errorMessage(err))
    }
  }

  const remove = async (template: SubscriptionTemplate) => {
    if (!(await confirm({
      title: '删除模板',
      description: `确认删除「${template.name}」？`,
      confirmLabel: '删除模板',
      destructive: true,
    }))) return
    try {
      await api.deleteSubscriptionTemplate(template.id)
      await load()
    } catch (err) {
      setError(errorMessage(err))
    }
  }

  return (
    <Page>
      <PageHeader
        title="订阅模板"
        actions={(
          <div className="flex gap-2">
            <Button variant="outline" onClick={() => refresh()} disabled={Boolean(refreshing)}>
              <RefreshCwIcon className={refreshing === 'all' ? 'animate-spin' : undefined} />
              刷新全部
            </Button>
            <Button onClick={() => beginEdit()}><PlusIcon />新建模板</Button>
          </div>
        )}
      />
      {error ? <Notice tone="danger">{error}</Notice> : null}
      {!loading && templates.length === 0 ? (
        <EmptyState icon={<FileCode2Icon />} title="暂无模板" description="创建本地模板或添加公开 GitHub 文件" />
      ) : null}
      <Surface className={!loading && templates.length === 0 ? 'hidden' : undefined}>
        <Table>
          <TableHeader>
            <TableRow>
              <TableHead>模板</TableHead>
              <TableHead>类型</TableHead>
              <TableHead>来源</TableHead>
              <TableHead>缓存状态</TableHead>
              <TableHead>许可证</TableHead>
              <TableHead>操作</TableHead>
            </TableRow>
          </TableHeader>
          <TableBody>
            {templates.map((template) => (
              <TableRow key={template.id}>
                <TableCell>
                  <div className="font-medium">{template.name}</div>
                  <div className="max-w-sm truncate font-mono text-xs text-muted-foreground">{template.id}</div>
                </TableCell>
                <TableCell><Badge variant="secondary">{template.kind}</Badge></TableCell>
                <TableCell>
                  <div className="flex items-center gap-2">
                    <Badge variant={template.readonly ? 'outline' : 'secondary'}>{template.origin === 'github' ? 'GitHub' : '本地'}</Badge>
                    {template.readonly ? <span className="text-xs text-muted-foreground">只读</span> : null}
                  </div>
                </TableCell>
                <TableCell className="text-xs">
                  {template.last_error ? (
                    <span className="text-destructive" title={template.last_error}>刷新失败，沿用缓存</span>
                  ) : template.fetched_at ? formatDateTime(template.fetched_at, timezone) : template.content ? '本地内容' : '尚未缓存'}
                </TableCell>
                <TableCell className="text-xs">{template.license || '-'}</TableCell>
                <TableCell>
                  <div className="flex gap-1">
                    <Button variant="ghost" size="icon" title="模板预览" onClick={() => setPreview(template)}><EyeIcon /></Button>
                    {template.origin === 'github' ? (
                      <Button variant="ghost" size="icon" title="刷新" onClick={() => refresh(template.id)} disabled={Boolean(refreshing)}>
                        <RefreshCwIcon className={refreshing === template.id ? 'animate-spin' : undefined} />
                      </Button>
                    ) : null}
                    <Button variant="ghost" size="icon" title="克隆" onClick={() => clone(template)}><CopyIcon /></Button>
                    {!template.readonly ? (
                      <>
                        <Button variant="ghost" size="icon" title="编辑" onClick={() => beginEdit(template)}><FileCode2Icon /></Button>
                        <Button variant="ghost" size="icon" title="删除" onClick={() => remove(template)}><Trash2Icon /></Button>
                      </>
                    ) : null}
                  </div>
                </TableCell>
              </TableRow>
            ))}
          </TableBody>
        </Table>
      </Surface>

      <Dialog open={open} onOpenChange={(next) => { setOpen(next); if (!next) resetForm() }}>
        <DialogContent className="max-h-[90vh] sm:max-w-4xl overflow-y-auto">
          <DialogHeader>
            <DialogTitle>{editing ? '编辑模板' : '新建模板'}</DialogTitle>
            <DialogDescription>本地内容保存前会解析校验；GitHub 源仅接受公开的具体文件地址。</DialogDescription>
          </DialogHeader>
          <form onSubmit={save} className="space-y-4">
            <div className="grid gap-3 sm:grid-cols-2">
              <div className="space-y-2"><Label>名称</Label><Input value={name} onChange={(event) => setName(event.target.value)} required /></div>
              <div className="space-y-2">
                <Label>类型</Label>
                <Select value={kind} onValueChange={(value) => value && setKind(value as SubscriptionTemplate['kind'])}>
                  <SelectTrigger className="w-full"><SelectValue /></SelectTrigger>
                  <SelectContent>{kinds.map((item) => <SelectItem key={item.value} value={item.value}>{item.label}</SelectItem>)}</SelectContent>
                </Select>
              </div>
            </div>
            <div className="space-y-2">
              <Label>公开 GitHub 文件地址（留空则使用本地内容）</Label>
              <Input value={sourceURL} onChange={(event) => setSourceURL(event.target.value)} placeholder="https://github.com/owner/repo/blob/main/path/template.ini" />
            </div>
            {!sourceURL ? (
              <div className="space-y-2">
                <Label>模板内容</Label>
                <textarea
                  className="min-h-80 w-full resize-y rounded-md border bg-background p-3 font-mono text-xs outline-none focus-visible:ring-2 focus-visible:ring-ring"
                  value={content}
                  onChange={(event) => setContent(event.target.value)}
                  spellCheck={false}
                  required
                />
              </div>
            ) : null}
            <div className="space-y-2"><Label>许可证</Label><Input value={license} onChange={(event) => setLicense(event.target.value)} placeholder="例如 CC BY-SA 4.0" /></div>
            <DialogFooter><Button type="submit" disabled={saving || !name.trim()}>{saving ? '保存中…' : '保存'}</Button></DialogFooter>
          </form>
        </DialogContent>
      </Dialog>

      <Dialog open={preview !== null} onOpenChange={(next) => !next && setPreview(null)}>
        <DialogContent className="max-h-[90vh] sm:max-w-5xl overflow-y-auto">
          <DialogHeader><DialogTitle>模板预览</DialogTitle><DialogDescription>{preview?.name} · 未填充 Lattix 用户和链路数据</DialogDescription></DialogHeader>
          <pre className="max-h-[65vh] overflow-auto rounded-md border bg-muted/30 p-4 font-mono text-xs whitespace-pre-wrap break-words">{preview?.content || '尚未取得有效缓存'}</pre>
          <DialogFooter showCloseButton />
        </DialogContent>
      </Dialog>
    </Page>
  )
}
