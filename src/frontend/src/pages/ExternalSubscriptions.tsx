import { useCallback, useEffect, useRef, useState, type FormEvent } from 'react'
import {
  EyeIcon,
  FileCode2Icon,
  GlobeIcon,
  PlusIcon,
  RefreshCwIcon,
  Trash2Icon,
} from 'lucide-react'

import { EmptyState, LoadingState, Notice, Page, PageHeader } from '@/components/PagePrimitives'
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
import { formatDateTime, humanizeBytes } from '@/lib/format'
import { useOperationProgress } from '@/lib/operation-progress-context'
import { useTimezone } from '@/lib/timezone'
import type { ExternalChain, ExternalSubscription } from '@/lib/types'

import './external-subscriptions.css'

// 订阅商通常按客户端 User-Agent 放行，不同服务商认可名单不同。
const UA_OPTIONS: Array<{ value: string; label: string }> = [
  { value: 'default', label: '默认（clash.meta/v2.0.0）' },
  { value: 'clash.meta/v2.0.0', label: 'clash.meta/v2.0.0' },
  { value: 'clash-meta/2.4.0', label: 'clash-meta/2.4.0' },
  { value: 'Clash for Windows/0.20.39', label: 'Clash for Windows/0.20.39' },
  { value: 'clash-verge/v2.1.2', label: 'clash-verge/v2.1.2' },
  { value: 'mihomo/1.18.10', label: 'mihomo/1.18.10' },
  { value: 'sing-box/1.11.0', label: 'sing-box/1.11.0' },
  { value: 'v2rayNG/1.8.16', label: 'v2rayNG/1.8.16' },
  { value: 'Shadowrocket/2.1.48', label: 'Shadowrocket/2.1.48' },
  { value: 'Stash/2.6.1', label: 'Stash/2.6.1' },
  { value: 'custom', label: '自定义…' },
]
const UA_PRESET_VALUES = new Set(UA_OPTIONS.map((option) => option.value))

export default function ExternalSubscriptions() {
  const { timezone } = useTimezone()
  const { confirm } = useAppDialog()
  const { showOperation } = useOperationProgress()
  const [subs, setSubs] = useState<ExternalSubscription[]>([])
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState('')
  const [syncing, setSyncing] = useState<number | null>(null)
  const [deleting, setDeleting] = useState<number | null>(null)

  const [open, setOpen] = useState(false)
  const [editing, setEditing] = useState<ExternalSubscription | null>(null)
  const [name, setName] = useState('')
  const [url, setUrl] = useState('')
  const [uaPreset, setUaPreset] = useState('default')
  const [customUA, setCustomUA] = useState('')
  const [skipCertVerify, setSkipCertVerify] = useState(false)
  const [autoUpdate, setAutoUpdate] = useState(true)
  const [updateIntervalHours, setUpdateIntervalHours] = useState('24')
  const [saving, setSaving] = useState(false)
  const [formError, setFormError] = useState('')

  const [chainsTarget, setChainsTarget] = useState<ExternalSubscription | null>(null)
  const [chains, setChains] = useState<ExternalChain[]>([])
  const [chainsLoading, setChainsLoading] = useState(false)
  const [chainsError, setChainsError] = useState('')
  // 记录当前打开的节点弹窗目标 id，避免过期请求的响应覆盖新目标
  const chainsTargetRef = useRef<number | null>(null)
  // 表单会话计数：每次打开弹窗自增，用于丢弃过期保存请求的响应
  const formSessionRef = useRef(0)

  const load = useCallback(async () => {
    setError('')
    try {
      setSubs(await api.externalSubscriptions())
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
    setUrl('')
    setUaPreset('default')
    setCustomUA('')
    setSkipCertVerify(false)
    setAutoUpdate(true)
    setUpdateIntervalHours('24')
    setFormError('')
  }

  const beginEdit = (sub?: ExternalSubscription) => {
    formSessionRef.current += 1
    resetForm()
    if (sub) {
      setEditing(sub)
      setName(sub.name)
      setUrl(sub.url)
      if (sub.user_agent && UA_PRESET_VALUES.has(sub.user_agent)) {
        setUaPreset(sub.user_agent)
      } else {
        setUaPreset('custom')
        setCustomUA(sub.user_agent ?? '')
      }
      setSkipCertVerify(sub.skip_cert_verify)
      setAutoUpdate(sub.auto_update)
      setUpdateIntervalHours(String(sub.update_interval_hours))
    }
    setOpen(true)
  }

  const save = async (event: FormEvent) => {
    event.preventDefault()
    const session = formSessionRef.current
    setSaving(true)
    setFormError('')
    try {
      const userAgent = uaPreset === 'default' ? undefined
        : uaPreset === 'custom' ? (customUA.trim() || undefined)
        : uaPreset
      const body = {
        name: name.trim(),
        url: url.trim(),
        user_agent: userAgent,
        skip_cert_verify: skipCertVerify,
        auto_update: autoUpdate,
        update_interval_hours: Number(updateIntervalHours) || 24,
      }
      if (editing) {
        const { observeId } = await api.updateExternalSubscription({ id: editing.id, ...body })
        if (observeId) showOperation({ observeId })
      } else {
        const { observeId } = await api.createExternalSubscription(body)
        if (observeId) showOperation({ observeId })
      }
      // 保存期间弹窗被关闭/重新打开：丢弃过期响应，避免误关新弹窗
      if (session !== formSessionRef.current) return
      setOpen(false)
      resetForm()
      await load()
    } catch (err) {
      if (session !== formSessionRef.current) return
      setFormError(errorMessage(err))
    } finally {
      if (session === formSessionRef.current) setSaving(false)
    }
  }

  const sync = async (sub: ExternalSubscription) => {
    setSyncing(sub.id)
    setError('')
    try {
      const { observeId } = await api.syncExternalSubscription(sub.id)
      if (observeId) showOperation({ observeId })
    } catch (err) {
      setError(errorMessage(err))
    } finally {
      setSyncing(null)
      await load()
    }
  }

  const remove = async (sub: ExternalSubscription) => {
    if (!(await confirm({
      title: '删除订阅',
      description: `确认删除「${sub.name}」？其导入的节点将一并移除。`,
      confirmLabel: '删除订阅',
      destructive: true,
    }))) return
    setDeleting(sub.id)
    try {
      const { observeId } = await api.deleteExternalSubscription(sub.id)
      if (observeId) showOperation({ observeId })
      await load()
    } catch (err) {
      setError(errorMessage(err))
    } finally {
      setDeleting(null)
    }
  }

  const openChains = async (sub: ExternalSubscription) => {
    chainsTargetRef.current = sub.id
    setChainsTarget(sub)
    setChains([])
    setChainsError('')
    setChainsLoading(true)
    try {
      const result = await api.externalSubscriptionChains(sub.id)
      if (chainsTargetRef.current !== sub.id) return
      setChains(result)
    } catch (err) {
      if (chainsTargetRef.current !== sub.id) return
      const message = errorMessage(err)
      setChainsError(message)
      setError(message)
    } finally {
      if (chainsTargetRef.current === sub.id) setChainsLoading(false)
    }
  }

  return (
    <Page className="cg-page-in">
      <div className="extsub-topline">
        <span className="cg-eyebrow">SUBSCRIPTION / EXTERNAL</span>
        <span className="cg-pill">{String(subs.length).padStart(2, '0')} SOURCES</span>
      </div>
      <PageHeader
        title="外部订阅"
        description="导入第三方订阅链接以汇聚节点，支持手动同步与按间隔自动更新。"
        actions={(
          <button type="button" className="cg-button is-primary" onClick={() => beginEdit()}>
            <PlusIcon />
            新建订阅
          </button>
        )}
      />
      {error ? <Notice tone="danger">{error}</Notice> : null}

      <div className="flex flex-col gap-3">
        {loading ? (
          <LoadingState />
        ) : subs.length === 0 ? (
          <EmptyState
            icon={<GlobeIcon />}
            title="暂无外部订阅"
            description="导入第三方订阅链接以汇聚节点"
          />
        ) : (
          subs.map((sub) => (
            <article key={sub.id} className="cg-card-raised extsub-card">
              <header className="extsub-card-head">
                <div className="extsub-card-title">
                  <div className="extsub-card-name">
                    <strong className="truncate">{sub.name}</strong>
                    {sub.format ? <span className="cg-status is-blue">{sub.format}</span> : null}
                  </div>
                  <span className="extsub-card-url" title={sub.url}>{sub.url}</span>
                </div>
                <div className="extsub-card-actions">
                  <button
                    type="button"
                    className="cg-button"
                    title="立即同步"
                    disabled={syncing === sub.id}
                    onClick={() => sync(sub)}
                  >
                    <RefreshCwIcon className={syncing === sub.id ? 'animate-spin' : undefined} />
                    {syncing === sub.id ? '同步中…' : '同步'}
                  </button>
                  <button type="button" className="cg-button" title="查看导入的节点" onClick={() => openChains(sub)}>
                    <EyeIcon />
                    节点
                  </button>
                  <button type="button" className="cg-icon-button" title="编辑" onClick={() => beginEdit(sub)}>
                    <FileCode2Icon />
                  </button>
                  <button
                    type="button"
                    className="cg-button extsub-danger"
                    disabled={deleting === sub.id}
                    onClick={() => remove(sub)}
                  >
                    <Trash2Icon />
                    {deleting === sub.id ? '删除中…' : '删除'}
                  </button>
                </div>
              </header>
              <div className="extsub-card-stats">
                <div className="extsub-stat">
                  <span className="cg-micro">节点数 / NODES</span>
                  <strong>{sub.node_count}</strong>
                </div>
                <div className="extsub-stat">
                  <span className="cg-micro">流量 USAGE / TOTAL</span>
                  <strong>{humanizeBytes(sub.download)} / {humanizeBytes(sub.total)}</strong>
                  <span className="extsub-traffic-bar" aria-hidden="true">
                    <i style={{ width: `${sub.total > 0 ? Math.min(100, Math.round((sub.download / sub.total) * 100)) : 0}%` }} />
                  </span>
                </div>
                <div className="extsub-stat">
                  <span className="cg-micro">到期时间 / EXPIRE</span>
                  <strong>{sub.expire ? formatDateTime(new Date(sub.expire * 1000).toISOString(), timezone) : '-'}</strong>
                </div>
                <div className="extsub-stat">
                  <span className="cg-micro">上次同步 / LAST SYNC</span>
                  <strong>{sub.last_sync_at ? formatDateTime(sub.last_sync_at, timezone) : '-'}</strong>
                </div>
              </div>
              {sub.last_error ? (
                <Notice tone="danger" className="extsub-error">{sub.last_error}</Notice>
              ) : null}
            </article>
          ))
        )}
      </div>

      <Dialog open={open} onOpenChange={(next) => { setOpen(next); if (!next) resetForm() }}>
        <DialogContent className="max-h-[90vh] sm:max-w-2xl overflow-y-auto">
          <DialogHeader>
            <DialogTitle>{editing ? '编辑订阅' : '新建订阅'}</DialogTitle>
            <DialogDescription>导入第三方订阅链接以汇聚节点；可手动同步，或按设定间隔自动同步。</DialogDescription>
          </DialogHeader>
          <form onSubmit={save} className="space-y-4">
            {formError ? <Notice tone="danger">{formError}</Notice> : null}
            <div className="grid gap-3 sm:grid-cols-2">
              <div className="space-y-2">
                <Label htmlFor="ext-sub-name">名称</Label>
                <Input id="ext-sub-name" value={name} onChange={(event) => setName(event.target.value)} required />
              </div>
              <div className="space-y-2">
                <Label htmlFor="ext-sub-url">URL</Label>
                <Input
                  id="ext-sub-url"
                  type="url"
                  value={url}
                  onChange={(event) => setUrl(event.target.value)}
                  placeholder="https://sub.example.com/a?token=..."
                  required
                />
              </div>
            </div>
            <div className="space-y-2">
              <Label>User-Agent</Label>
              <Select value={uaPreset} onValueChange={(value) => value && setUaPreset(value)}>
                <SelectTrigger className="w-full"><SelectValue /></SelectTrigger>
                <SelectContent>
                  {UA_OPTIONS.map((option) => (
                    <SelectItem key={option.value} value={option.value}>{option.label}</SelectItem>
                  ))}
                </SelectContent>
              </Select>
              {uaPreset === 'custom' ? (
                <Input
                  value={customUA}
                  onChange={(event) => setCustomUA(event.target.value)}
                  placeholder="clash.meta/v2.0.0（留空使用默认）"
                />
              ) : (
                <p className="text-xs text-muted-foreground">
                  订阅服务商常按客户端标识放行，选择不被放行的标识可能返回 406 等错误。
                </p>
              )}
            </div>
            <div className="grid gap-3 sm:grid-cols-2">
              <label className="extsub-check">
                <input
                  type="checkbox"
                  className="extsub-check-input"
                  checked={skipCertVerify}
                  onChange={(event) => setSkipCertVerify(event.target.checked)}
                />
                跳过证书校验
              </label>
              <label className="extsub-check">
                <input
                  type="checkbox"
                  className="extsub-check-input"
                  checked={autoUpdate}
                  onChange={(event) => setAutoUpdate(event.target.checked)}
                />
                自动同步
              </label>
            </div>
            <div className="space-y-2">
              <Label htmlFor="ext-sub-interval">同步间隔（小时）</Label>
              <Input
                id="ext-sub-interval"
                type="number"
                min={1}
                max={168}
                value={updateIntervalHours}
                onChange={(event) => setUpdateIntervalHours(event.target.value)}
                disabled={!autoUpdate}
              />
            </div>
            <DialogFooter>
              <Button type="submit" disabled={saving || !name.trim() || !url.trim()}>
                {saving ? '保存中…' : '保存'}
              </Button>
            </DialogFooter>
          </form>
        </DialogContent>
      </Dialog>

      <Dialog open={chainsTarget !== null} onOpenChange={(next) => {
        if (!next) {
          setChainsTarget(null)
          chainsTargetRef.current = null
        }
      }}>
        <DialogContent className="max-h-[90vh] sm:max-w-3xl overflow-y-auto">
          <DialogHeader>
            <DialogTitle>外部节点</DialogTitle>
            <DialogDescription>「{chainsTarget?.name}」导入的节点清单。</DialogDescription>
          </DialogHeader>
          {chainsError ? <Notice tone="danger">{chainsError}</Notice> : null}
          {chainsLoading ? (
            <p className="flex h-40 items-center justify-center text-sm text-muted-foreground">加载中…</p>
          ) : chains.length === 0 ? (
            <p className="flex h-40 items-center justify-center text-sm text-muted-foreground">暂无节点，先同步该订阅试试</p>
          ) : (
            <Table>
              <TableHeader>
                <TableRow>
                  <TableHead>名称</TableHead>
                  <TableHead>协议</TableHead>
                  <TableHead>服务器:端口</TableHead>
                  <TableHead>配置指纹</TableHead>
                </TableRow>
              </TableHeader>
              <TableBody>
                {chains.map((chain) => (
                  <TableRow key={chain.id}>
                    <TableCell className="font-medium">{chain.name}</TableCell>
                    <TableCell><span className="cg-status is-blue">{chain.protocol}</span></TableCell>
                    <TableCell className="font-mono text-xs">{chain.server}:{chain.port}</TableCell>
                    <TableCell><code className="extsub-sha">{chain.config_sha256.slice(0, 8)}</code></TableCell>
                  </TableRow>
                ))}
              </TableBody>
            </Table>
          )}
          <DialogFooter showCloseButton />
        </DialogContent>
      </Dialog>
    </Page>
  )
}
