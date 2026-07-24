import { useCallback, useEffect, useState, type FormEvent } from 'react'
import { PlusIcon, QrCodeIcon, Trash2Icon } from 'lucide-react'

import { CopyButton } from '@/components/CopyButton'
import { QRDialog } from '@/components/QRDialog'
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
import { humanizeBytes } from '@/lib/format'
import type { SubUser, XrayNode } from '@/lib/types'

function formatTime(t: string): string {
  return new Date(t).toLocaleString()
}

function nodeLabel(n: XrayNode): string {
  const port = n.realized_config?.port ?? n.port ?? '自动'
  return `${n.server_alias} · ${n.protocol} · ${port}`
}

export default function Users() {
  const [users, setUsers] = useState<SubUser[]>([])
  const [nodes, setNodes] = useState<XrayNode[]>([])
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState('')
  const [deleting, setDeleting] = useState<number | null>(null)

  const [open, setOpen] = useState(false)
  const [name, setName] = useState('')
  const [creating, setCreating] = useState(false)
  const [createError, setCreateError] = useState('')
  const [created, setCreated] = useState<SubUser | null>(null)

  const [assignTarget, setAssignTarget] = useState<SubUser | null>(null)
  const [assignSelection, setAssignSelection] = useState<number[]>([])
  const [assignSaving, setAssignSaving] = useState(false)
  const [assignError, setAssignError] = useState('')
  const [qrText, setQrText] = useState('')

  const load = useCallback(() => {
    Promise.all([api.users(), api.nodes()])
      .then(([u, n]) => {
        setUsers(u)
        setNodes(n)
      })
      .catch((err) => setError(errorMessage(err)))
      .finally(() => setLoading(false))
  }, [])

  useEffect(() => {
    load()
    const timer = setInterval(load, 5000)
    return () => clearInterval(timer)
  }, [load])

  const onOpenChange = (next: boolean) => {
    setOpen(next)
    if (!next) {
      setName('')
      setCreateError('')
      setCreated(null)
    }
  }

  const onCreate = async (e: FormEvent) => {
    e.preventDefault()
    setCreateError('')
    setCreating(true)
    try {
      const res = await api.createUser(name.trim())
      setCreated(res)
      load()
    } catch (err) {
      setCreateError(errorMessage(err))
    } finally {
      setCreating(false)
    }
  }

  const onDelete = async (user: SubUser) => {
    if (!window.confirm(`确认删除用户「${user.name}」？删除后其订阅链接将失效。`)) {
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

      <div className="rounded-lg border">
        <Table>
          <TableHeader>
            <TableRow>
              <TableHead>姓名</TableHead>
              <TableHead>节点</TableHead>
              <TableHead>流量</TableHead>
              <TableHead>订阅链接</TableHead>
              <TableHead>创建时间</TableHead>
              <TableHead>操作</TableHead>
            </TableRow>
          </TableHeader>
          <TableBody>
            {loading ? (
              <TableRow>
                <TableCell colSpan={6} className="text-center text-muted-foreground">
                  加载中…
                </TableCell>
              </TableRow>
            ) : users.length === 0 ? (
              <TableRow>
                <TableCell colSpan={6} className="text-center text-muted-foreground">
                  暂无用户
                </TableCell>
              </TableRow>
            ) : (
              users.map((u) => (
                <TableRow key={u.id}>
                  <TableCell className="font-medium">{u.name}</TableCell>
                  <TableCell>
                    {u.node_ids.length === 0 ? (
                      <span className="text-muted-foreground">未分配</span>
                    ) : (
                      `${u.node_ids.length} / ${nodes.length}`
                    )}
                  </TableCell>
                  <TableCell className="text-xs whitespace-nowrap">
                    {u.traffic ? `↑${humanizeBytes(u.traffic.up)} ↓${humanizeBytes(u.traffic.down)}` : '-'}
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
                    </div>
                  </TableCell>
                  <TableCell>{formatTime(u.created_at)}</TableCell>
                  <TableCell className="space-x-2">
                    <Button variant="outline" size="sm" onClick={() => onOpenAssign(u)}>
                      分配节点
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
        <DialogContent>
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

      <Dialog open={assignTarget !== null} onOpenChange={(next) => !next && setAssignTarget(null)}>
        <DialogContent className="max-h-[85vh] overflow-y-auto">
          <DialogHeader>
            <DialogTitle>分配节点</DialogTitle>
            <DialogDescription>
              勾选「{assignTarget?.name}」可使用的节点；未勾选的节点不会出现在其订阅中，
              保存后即时下发变更（默认全关，§16）。
            </DialogDescription>
          </DialogHeader>
          <div className="space-y-2">
            {nodes.length === 0 ? (
              <p className="text-sm text-muted-foreground">暂无节点，请先在「节点」页创建。</p>
            ) : (
              nodes.map((n) => (
                <label key={n.id} className="flex cursor-pointer items-center gap-2 rounded-md border p-2 text-sm">
                  <input
                    type="checkbox"
                    className="size-4 accent-primary"
                    checked={assignSelection.includes(n.id)}
                    onChange={(e) => onToggleNode(n.id, e.target.checked)}
                  />
                  <span>{nodeLabel(n)}</span>
                  {n.status !== 'active' && (
                    <span className="text-xs text-muted-foreground">（{n.status}）</span>
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
