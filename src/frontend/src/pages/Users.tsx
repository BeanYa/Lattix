import { useCallback, useEffect, useMemo, useState, type FormEvent } from 'react'
import {
  BanIcon,
  CalendarClockIcon,
  CircleCheckIcon,
  ExternalLinkIcon,
  PlusIcon,
  QrCodeIcon,
  Trash2Icon,
  UsersIcon,
} from 'lucide-react'

import { CopyButton } from '@/components/CopyButton'
import { QRDialog } from '@/components/QRDialog'
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
import { buildLinkOptions } from '@/lib/links'
import { useTimezone } from '@/lib/timezone'
import type { Chain, SubUser, XrayNode } from '@/lib/types'

/** toLocalInput 把 RFC3339 时间转成 datetime-local 输入框所需的本地格式（yyyy-MM-ddTHH:mm）。 */
function toLocalInput(t: string): string {
  const d = new Date(t)
  const pad = (n: number) => String(n).padStart(2, '0')
  return `${d.getFullYear()}-${pad(d.getMonth() + 1)}-${pad(d.getDate())}T${pad(d.getHours())}:${pad(d.getMinutes())}`
}

/** localInputToRFC3339 把 datetime-local 值转成 RFC3339（UTC）；空串返回 null。 */
function localInputToRFC3339(v: string): string | null {
  if (!v) {
    return null
  }
  const d = new Date(v)
  return Number.isNaN(d.getTime()) ? null : d.toISOString()
}

export default function Users() {
  const { timezone } = useTimezone()
  const { confirm } = useAppDialog()
  const [users, setUsers] = useState<SubUser[]>([])
  const [nodes, setNodes] = useState<XrayNode[]>([])
  const [chains, setChains] = useState<Chain[]>([])
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState('')
  const [deleting, setDeleting] = useState<number | null>(null)
  const [toggling, setToggling] = useState<number | null>(null)

  const [open, setOpen] = useState(false)
  const [name, setName] = useState('')
  const [expiresAt, setExpiresAt] = useState('')
  const [creating, setCreating] = useState(false)
  const [createError, setCreateError] = useState('')
  const [created, setCreated] = useState<SubUser | null>(null)
  const [createLinkSel, setCreateLinkSel] = useState<number[]>([])

  const [expiryTarget, setExpiryTarget] = useState<SubUser | null>(null)
  const [expiryValue, setExpiryValue] = useState('')
  const [expirySaving, setExpirySaving] = useState(false)
  const [expiryError, setExpiryError] = useState('')

  const [assignTarget, setAssignTarget] = useState<SubUser | null>(null)
  const [assignSelection, setAssignSelection] = useState<number[]>([])
  const [assignSaving, setAssignSaving] = useState(false)
  const [assignError, setAssignError] = useState('')
  const [qrText, setQrText] = useState('')

  const load = useCallback((silent = false) => {
    const options = silent ? { display: 'silent' as const } : undefined
    Promise.all([api.users(options), api.nodes(options), api.chains(options)])
      .then(([u, n, c]) => {
        setUsers(u)
        setNodes(n)
        setChains(c)
      })
      .catch((err) => setError(errorMessage(err)))
      .finally(() => setLoading(false))
  }, [])

  useEffect(() => {
    load()
    const timer = setInterval(() => load(true), 5000)
    return () => clearInterval(timer)
  }, [load])

  const onOpenChange = (next: boolean) => {
    setOpen(next)
    if (!next) {
      setName('')
      setExpiresAt('')
      setCreateError('')
      setCreated(null)
      setCreateLinkSel([])
    }
  }

  const onToggleCreateLink = (id: number, checked: boolean) => {
    setCreateLinkSel((cur) => (checked ? [...cur, id] : cur.filter((x) => x !== id)))
  }

  const linkOptions = useMemo(() => buildLinkOptions(nodes, chains), [chains, nodes])

  const onCreate = async (e: FormEvent) => {
    e.preventDefault()
    setCreateError('')
    setCreating(true)
    try {
      const res = await api.createUser(name.trim(), localInputToRFC3339(expiresAt), createLinkSel)
      setCreated(res)
      load()
    } catch (err) {
      setCreateError(errorMessage(err))
    } finally {
      setCreating(false)
    }
  }

  const onOpenExpiry = (u: SubUser) => {
    setExpiryTarget(u)
    setExpiryValue(u.expires_at ? toLocalInput(u.expires_at) : '')
    setExpiryError('')
  }

  const onSaveExpiry = async (clear: boolean) => {
    if (!expiryTarget) {
      return
    }
    setExpiryError('')
    setExpirySaving(true)
    try {
      await api.updateUserExpiry(expiryTarget.id, clear ? null : localInputToRFC3339(expiryValue))
      setExpiryTarget(null)
      load()
    } catch (err) {
      setExpiryError(errorMessage(err))
    } finally {
      setExpirySaving(false)
    }
  }

  const onDelete = async (user: SubUser) => {
    if (!(await confirm({
      title: '删除用户',
      description: `确认删除用户「${user.name}」？删除后其订阅链接将失效。`,
      confirmLabel: '删除用户',
      destructive: true,
    }))) {
      return
    }
    setDeleting(user.id)
    try {
      await api.deleteUser(user.id)
      load()
    } catch (err) {
      setError(errorMessage(err))
    } finally {
      setDeleting(null)
    }
  }

  const onToggleDisabled = async (user: SubUser) => {
    setToggling(user.id)
    try {
      await api.setUserDisabled(user.id, !user.disabled)
      load()
    } catch (err) {
      setError(errorMessage(err))
    } finally {
      setToggling(null)
    }
  }

  const onOpenAssign = (u: SubUser) => {
    setAssignTarget(u)
    setAssignSelection(u.node_ids)
    setAssignError('')
  }

  const onToggleNode = (id: number, checked: boolean) => {
    setAssignSelection((cur) => (checked ? [...cur, id] : cur.filter((x) => x !== id)))
  }

  const onSaveAssign = async () => {
    if (!assignTarget) {
      return
    }
    setAssignError('')
    setAssignSaving(true)
    try {
      await api.setUserNodes(assignTarget.id, assignSelection)
      setAssignTarget(null)
      load()
    } catch (err) {
      setAssignError(errorMessage(err))
    } finally {
      setAssignSaving(false)
    }
  }

  return (
    <div className="space-y-4">
      <div className="flex items-center justify-between">
        <h1 className="text-xl font-semibold">用户</h1>
        <Button onClick={() => setOpen(true)}>
          <PlusIcon />
          创建用户
        </Button>
      </div>

      {error && <p className="text-sm text-destructive">{error}</p>}

      {!loading && users.length === 0 ? (
        <div className="flex min-h-64 flex-col items-center justify-center rounded-xl border border-dashed text-center">
          <UsersIcon className="size-8 text-muted-foreground" />
          <p className="mt-3 text-sm text-muted-foreground">暂无用户，点击右上角“创建用户”开始</p>
        </div>
      ) : null}
      <div className={!loading && users.length === 0 ? 'hidden' : 'rounded-lg border'}>
        <Table>
          <TableHeader>
            <TableRow>
              <TableHead>姓名</TableHead>
              <TableHead>链路</TableHead>
              <TableHead>流量</TableHead>
              <TableHead>有效期</TableHead>
              <TableHead>订阅链接</TableHead>
              <TableHead>创建时间</TableHead>
              <TableHead>操作</TableHead>
            </TableRow>
          </TableHeader>
          <TableBody>
            {loading ? (
              <TableRow>
                <TableCell colSpan={7} className="text-center text-muted-foreground">
                  加载中…
                </TableCell>
              </TableRow>
            ) : (
              users.map((u) => (
                <TableRow key={u.id}>
                  <TableCell className="font-medium">
                    <div className="flex items-center gap-2">
                      {u.name}
                      {u.disabled && <Badge variant="destructive">已停用</Badge>}
                      {u.expired && <Badge variant="destructive">已到期</Badge>}
                    </div>
                  </TableCell>
                  <TableCell>
                    {u.node_ids.length === 0 ? (
                      <span className="text-muted-foreground">未分配</span>
                    ) : (
                      `${u.node_ids.filter((id) => linkOptions.some((link) => link.nodeId === id)).length} / ${linkOptions.length}`
                    )}
                  </TableCell>
                  <TableCell className="text-xs whitespace-nowrap">
                    {u.traffic ? `↑${humanizeBytes(u.traffic.up)} ↓${humanizeBytes(u.traffic.down)}` : '-'}
                  </TableCell>
                  <TableCell className="text-xs whitespace-nowrap">
                    {u.expires_at ? formatDateTime(u.expires_at, timezone) : <span className="text-muted-foreground">长期</span>}
                  </TableCell>
                  <TableCell>
                    <div className="flex max-w-md items-center gap-2">
                      <span className="truncate font-mono text-xs text-muted-foreground" title={u.sub_url}>
                        {u.sub_url}
                      </span>
                      <CopyButton text={u.sub_url} />
                      <span title="复制分享链接订阅（vless:// 等链接集合）">
                        <CopyButton text={u.sub_links_url} />
                      </span>
                      <Button variant="ghost" size="icon" title="订阅二维码" onClick={() => setQrText(u.sub_url)}>
                        <QrCodeIcon />
                      </Button>
                      <Button
                        variant="ghost"
                        size="icon"
                        title="打开订阅落地页（浏览器）"
                        onClick={() => window.open(u.sub_url, '_blank', 'noopener')}
                      >
                        <ExternalLinkIcon />
                      </Button>
                    </div>
                  </TableCell>
                  <TableCell>{formatDateTime(u.created_at, timezone)}</TableCell>
                  <TableCell className="space-x-2">
                    <Button variant="outline" size="sm" onClick={() => onOpenAssign(u)}>
                      分配链路
                    </Button>
                    <Button variant="outline" size="sm" title="修改有效期" onClick={() => onOpenExpiry(u)}>
                      <CalendarClockIcon />
                      有效期
                    </Button>
                    <Button
                      variant="outline"
                      size="sm"
                      title={u.disabled ? '启用（恢复链路下发与订阅）' : '停用（立即停权，订阅链路清空）'}
                      disabled={toggling === u.id}
                      onClick={() => onToggleDisabled(u)}
                    >
                      {u.disabled ? <CircleCheckIcon /> : <BanIcon />}
                      {toggling === u.id ? '处理中…' : u.disabled ? '启用' : '停用'}
                    </Button>
                    <Button
                      variant="destructive"
                      size="sm"
                      disabled={deleting === u.id}
                      onClick={() => onDelete(u)}
                    >
                      <Trash2Icon />
                      {deleting === u.id ? '删除中…' : '删除'}
                    </Button>
                  </TableCell>
                </TableRow>
              ))
            )}
          </TableBody>
        </Table>
      </div>

      <Dialog open={open} onOpenChange={onOpenChange}>
        <DialogContent className="max-h-[85vh] overflow-y-auto">
          <DialogHeader>
            <DialogTitle>创建用户</DialogTitle>
            <DialogDescription>
              {created ? '用户已创建，请将订阅链接发给用户。' : '输入姓名创建用户。'}
            </DialogDescription>
          </DialogHeader>
          {created ? (
            <div className="space-y-3">
              <div className="space-y-2">
                <Label>订阅链接</Label>
                <div className="rounded-lg border border-primary/30 bg-primary/5 p-3 text-xs break-all">
                  {created.sub_url}
                </div>
              </div>
              <DialogFooter showCloseButton>
                <CopyButton text={created.sub_url} />
              </DialogFooter>
            </div>
          ) : (
            <form onSubmit={onCreate} className="space-y-4">
              <div className="space-y-2">
                <Label htmlFor="name">姓名</Label>
                <Input
                  id="name"
                  value={name}
                  onChange={(e) => setName(e.target.value)}
                  placeholder="例如：张三"
                  required
                  autoFocus
                />
              </div>
              <div className="space-y-2">
                <Label htmlFor="expires-at">有效期（可选，留空为长期）</Label>
                <Input
                  id="expires-at"
                  type="datetime-local"
                  value={expiresAt}
                  onChange={(e) => setExpiresAt(e.target.value)}
                />
              </div>
              <div className="space-y-2">
                <Label>分配链路（可选）</Label>
                {linkOptions.length === 0 ? (
                  <p className="text-sm text-muted-foreground">暂无链路，请先在「链路」页创建。</p>
                ) : (
                  linkOptions.map((link) => (
                    <label key={link.nodeId} className="flex cursor-pointer items-center gap-2 rounded-md border p-2 text-sm">
                      <input
                        type="checkbox"
                        className="size-4 accent-primary"
                        checked={createLinkSel.includes(link.nodeId)}
                        onChange={(e) => onToggleCreateLink(link.nodeId, e.target.checked)}
                      />
                      <Badge variant="secondary">{link.type === 'direct' ? '直连' : '中转'}</Badge>
                      <span>{link.name}</span>
                      <span className="text-xs text-muted-foreground">{link.detail}</span>
                    </label>
                  ))
                )}
              </div>
              {createError && <p className="text-sm text-destructive">{createError}</p>}
              <DialogFooter>
                <Button type="submit" disabled={creating || !name.trim()}>
                  {creating ? '创建中…' : '创建'}
                </Button>
              </DialogFooter>
            </form>
          )}
        </DialogContent>
      </Dialog>
      <QRDialog text={qrText} open={qrText !== ''} onClose={() => setQrText('')} />

      <Dialog open={expiryTarget !== null} onOpenChange={(next) => !next && setExpiryTarget(null)}>
        <DialogContent className="max-w-sm">
          <DialogHeader>
            <DialogTitle>修改有效期</DialogTitle>
            <DialogDescription>
              设置「{expiryTarget?.name}」的到期时刻；到期后自动停权（订阅保留但链路为空），
              延长或清除有效期会恢复其链路。
            </DialogDescription>
          </DialogHeader>
          <div className="space-y-2">
            <Label htmlFor="edit-expires-at">有效期（留空并保存即为长期）</Label>
            <Input
              id="edit-expires-at"
              type="datetime-local"
              value={expiryValue}
              onChange={(e) => setExpiryValue(e.target.value)}
            />
          </div>
          {expiryError && <p className="text-sm text-destructive">{expiryError}</p>}
          <DialogFooter>
            {expiryTarget?.expires_at && (
              <Button variant="outline" disabled={expirySaving} onClick={() => onSaveExpiry(true)}>
                清除有效期
              </Button>
            )}
            <Button disabled={expirySaving} onClick={() => onSaveExpiry(false)}>
              {expirySaving ? '保存中…' : '保存'}
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>

      <Dialog open={assignTarget !== null} onOpenChange={(next) => !next && setAssignTarget(null)}>
        <DialogContent className="max-h-[85vh] overflow-y-auto">
          <DialogHeader>
            <DialogTitle>分配链路</DialogTitle>
            <DialogDescription>
              勾选「{assignTarget?.name}」可使用的链路；未勾选的链路不会出现在其订阅中，
              保存后即时下发变更（默认全关，§16）。
            </DialogDescription>
          </DialogHeader>
          <div className="space-y-2">
            {linkOptions.length === 0 ? (
              <p className="text-sm text-muted-foreground">暂无链路，请先在「链路」页创建。</p>
            ) : (
              linkOptions.map((link) => (
                <label key={link.nodeId} className="flex cursor-pointer items-center gap-2 rounded-md border p-2 text-sm">
                  <input
                    type="checkbox"
                    className="size-4 accent-primary"
                    checked={assignSelection.includes(link.nodeId)}
                    onChange={(e) => onToggleNode(link.nodeId, e.target.checked)}
                  />
                  <Badge variant="secondary">{link.type === 'direct' ? '直连' : '中转'}</Badge>
                  <span>{link.name}</span>
                  <span className="text-xs text-muted-foreground">{link.detail}</span>
                  {link.status !== 'active' && (
                    <span className="text-xs text-muted-foreground">（{link.status}）</span>
                  )}
                </label>
              ))
            )}
          </div>
          {assignError && <p className="text-sm text-destructive">{assignError}</p>}
          <DialogFooter>
            <Button onClick={onSaveAssign} disabled={assignSaving}>
              {assignSaving ? '保存中…' : '保存'}
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>
    </div>
  )
}
