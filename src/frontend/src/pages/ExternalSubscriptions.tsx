import { useCallback, useEffect, useState, type FormEvent } from 'react'
import {
  EyeIcon,
  FileCode2Icon,
  GlobeIcon,
  PlusIcon,
  RefreshCwIcon,
  Trash2Icon,
} from 'lucide-react'

import { EmptyState, LoadingState, Notice, Page, PageHeader } from '@/components/PagePrimitives'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import {
  Card,
  CardAction,
  CardContent,
  CardDescription,
  CardHeader,
  CardTitle,
} from '@/components/ui/card'
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
import { useTimezone } from '@/lib/timezone'
import type { ExternalChain, ExternalSubscription } from '@/lib/types'

export default function ExternalSubscriptions() {
  const { timezone } = useTimezone()
  const { confirm } = useAppDialog()
  const [subs, setSubs] = useState<ExternalSubscription[]>([])
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState('')
  const [syncing, setSyncing] = useState<number | null>(null)
  const [deleting, setDeleting] = useState<number | null>(null)

  const [open, setOpen] = useState(false)
  const [editing, setEditing] = useState<ExternalSubscription | null>(null)
  const [name, setName] = useState('')
  const [url, setUrl] = useState('')
  const [userAgent, setUserAgent] = useState('')
  const [skipCertVerify, setSkipCertVerify] = useState(false)
  const [autoUpdate, setAutoUpdate] = useState(true)
  const [updateIntervalHours, setUpdateIntervalHours] = useState('24')
  const [saving, setSaving] = useState(false)

  const [chainsTarget, setChainsTarget] = useState<ExternalSubscription | null>(null)
  const [chains, setChains] = useState<ExternalChain[]>([])
  const [chainsLoading, setChainsLoading] = useState(false)
  const [chainsError, setChainsError] = useState('')

  const load = useCallback(async () => {
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
    setUserAgent('')
    setSkipCertVerify(false)
    setAutoUpdate(true)
    setUpdateIntervalHours('24')
  }

  const beginEdit = (sub?: ExternalSubscription) => {
    resetForm()
    if (sub) {
      setEditing(sub)
      setName(sub.name)
      setUrl(sub.url)
      setUserAgent(sub.user_agent)
      setSkipCertVerify(sub.skip_cert_verify)
      setAutoUpdate(sub.auto_update)
      setUpdateIntervalHours(String(sub.update_interval_hours))
    }
    setOpen(true)
  }

  const save = async (event: FormEvent) => {
    event.preventDefault()
    setSaving(true)
    setError('')
    try {
      const body = {
        name: name.trim(),
        url: url.trim(),
        user_agent: userAgent.trim() || undefined,
        skip_cert_verify: skipCertVerify,
        auto_update: autoUpdate,
        update_interval_hours: Number(updateIntervalHours) || 24,
      }
      if (editing) {
        await api.updateExternalSubscription({ id: editing.id, ...body })
      } else {
        await api.createExternalSubscription(body)
      }
      setOpen(false)
      resetForm()
      await load()
    } catch (err) {
      setError(errorMessage(err))
    } finally {
      setSaving(false)
    }
  }

  const sync = async (sub: ExternalSubscription) => {
    setSyncing(sub.id)
    setError('')
    try {
      await api.syncExternalSubscription(sub.id)
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
      await api.deleteExternalSubscription(sub.id)
      await load()
    } catch (err) {
      setError(errorMessage(err))
    } finally {
      setDeleting(null)
    }
  }

  const openChains = async (sub: ExternalSubscription) => {
    setChainsTarget(sub)
    setChains([])
    setChainsError('')
    setChainsLoading(true)
    try {
      setChains(await api.externalSubscriptionChains(sub.id))
    } catch (err) {
      const message = errorMessage(err)
      setChainsError(message)
      setError(message)
    } finally {
      setChainsLoading(false)
    }
  }

  return (
    <Page>
      <PageHeader
        title="外部订阅"
        actions={(
          <Button onClick={() => beginEdit()}>
            <PlusIcon />
            新建订阅
          </Button>
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
            <Card key={sub.id} size="sm">
              <CardHeader className="border-b has-data-[slot=card-action]:grid-cols-1 sm:has-data-[slot=card-action]:grid-cols-[1fr_auto]">
                <CardTitle className="flex min-w-0 flex-wrap items-center gap-2">
                  <span className="truncate">{sub.name}</span>
                  {sub.format ? <Badge variant="secondary">{sub.format}</Badge> : null}
                </CardTitle>
                <CardDescription className="flex min-w-0 items-center gap-1.5 pl-10 text-xs">
                  <span className="truncate font-mono" title={sub.url}>{sub.url}</span>
                </CardDescription>
                <CardAction className="col-start-1 row-start-3 row-span-1 justify-self-start sm:col-start-2 sm:row-start-1 sm:row-span-2 sm:justify-self-end">
                  <div className="flex max-w-full flex-wrap gap-2 sm:justify-end">
                    <Button
                      variant="outline"
                      size="sm"
                      title="立即同步"
                      disabled={syncing === sub.id}
                      onClick={() => sync(sub)}
                    >
                      <RefreshCwIcon className={syncing === sub.id ? 'animate-spin' : undefined} />
                      {syncing === sub.id ? '同步中…' : '同步'}
                    </Button>
                    <Button variant="outline" size="sm" title="查看导入的节点" onClick={() => openChains(sub)}>
                      <EyeIcon />
                      节点
                    </Button>
                    <Button variant="ghost" size="icon-sm" title="编辑" onClick={() => beginEdit(sub)}>
                      <FileCode2Icon />
                    </Button>
                    <Button
                      variant="destructive"
                      size="sm"
                      disabled={deleting === sub.id}
                      onClick={() => remove(sub)}
                    >
                      <Trash2Icon />
                      {deleting === sub.id ? '删除中…' : '删除'}
                    </Button>
                  </div>
                </CardAction>
              </CardHeader>
              <CardContent className="grid gap-3 sm:grid-cols-2 lg:grid-cols-4">
                <div className="space-y-1">
                  <span className="block text-[11px] text-muted-foreground">节点数</span>
                  <span className="block font-medium tabular-nums">{sub.node_count}</span>
                </div>
                <div className="space-y-1">
                  <span className="block text-[11px] text-muted-foreground">流量（已用/总量）</span>
                  <span className="block font-medium tabular-nums">{humanizeBytes(sub.download)} / {humanizeBytes(sub.total)}</span>
                </div>
                <div className="space-y-1">
                  <span className="block text-[11px] text-muted-foreground">到期时间</span>
                  <span className="block">
                    {sub.expire ? formatDateTime(new Date(sub.expire * 1000).toISOString(), timezone) : '-'}
                  </span>
                </div>
                <div className="space-y-1">
                  <span className="block text-[11px] text-muted-foreground">上次同步</span>
                  <span className="block">
                    {sub.last_sync_at ? formatDateTime(sub.last_sync_at, timezone) : '-'}
                  </span>
                </div>
                {sub.last_error ? (
                  <Notice tone="danger" className="sm:col-span-2 lg:col-span-4">{sub.last_error}</Notice>
                ) : null}
              </CardContent>
            </Card>
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
              <Label htmlFor="ext-sub-ua">User-Agent</Label>
              <Input
                id="ext-sub-ua"
                value={userAgent}
                onChange={(event) => setUserAgent(event.target.value)}
                placeholder="clash-meta/2.4.0（留空使用默认）"
              />
            </div>
            <div className="grid gap-3 sm:grid-cols-2">
              <label className="flex cursor-pointer items-center gap-2 rounded-md border p-2 text-sm">
                <input
                  type="checkbox"
                  className="size-4 accent-primary"
                  checked={skipCertVerify}
                  onChange={(event) => setSkipCertVerify(event.target.checked)}
                />
                跳过证书校验
              </label>
              <label className="flex cursor-pointer items-center gap-2 rounded-md border p-2 text-sm">
                <input
                  type="checkbox"
                  className="size-4 accent-primary"
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

      <Dialog open={chainsTarget !== null} onOpenChange={(next) => !next && setChainsTarget(null)}>
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
                    <TableCell><Badge variant="secondary">{chain.protocol}</Badge></TableCell>
                    <TableCell className="font-mono text-xs">{chain.server}:{chain.port}</TableCell>
                    <TableCell><Badge variant="outline" className="font-mono">{chain.config_sha256.slice(0, 8)}</Badge></TableCell>
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
