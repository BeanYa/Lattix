import { useCallback, useEffect, useState, type FormEvent } from 'react'
import { PlusIcon } from 'lucide-react'

import { CopyButton } from '@/components/CopyButton'
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
import type { Server } from '@/lib/types'

function formatTime(t: string | null): string {
  return t ? new Date(t).toLocaleString() : '-'
}

export default function Servers() {
  const [servers, setServers] = useState<Server[]>([])
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState('')

  const [open, setOpen] = useState(false)
  const [alias, setAlias] = useState('')
  const [address, setAddress] = useState('')
  const [xrayVersion, setXrayVersion] = useState('latest')
  const [creating, setCreating] = useState(false)
  const [createError, setCreateError] = useState('')
  const [cmdView, setCmdView] = useState<{ title: string; command: string } | null>(null)
  const [deleteTarget, setDeleteTarget] = useState<Server | null>(null)
  const [deleting, setDeleting] = useState(false)

  const load = useCallback(() => {
    api
      .servers()
      .then(setServers)
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
      setAlias('')
      setAddress('')
      setXrayVersion('latest')
      setCreateError('')
    }
  }

  const onCreate = async (e: FormEvent) => {
    e.preventDefault()
    setCreateError('')
    setCreating(true)
    try {
      const res = await api.createServer(alias.trim(), address, xrayVersion)
      onOpenChange(false)
      setCmdView({ title: '服务器已创建，请在目标机器上执行安装命令', command: res.install_command })
      load()
    } catch (err) {
      setCreateError(errorMessage(err))
    } finally {
      setCreating(false)
    }
  }

  // 未安装（从未上线）→ 重新获取安装命令；已安装 → 凭证刷新（旧凭证立即失效）。
  const onRotateToken = async (s: Server) => {
    const installed = s.last_seen_at !== null
    if (
      installed &&
      !window.confirm('刷新后该服务器的旧凭证（含长期凭证）立即失效，agent 重连前需重新执行安装命令。继续？')
    ) {
      return
    }
    try {
      const res = await api.rotateServerToken(s.id)
      setCmdView({
        title: installed ? '凭证已刷新，请重新执行安装命令' : '安装命令（bootstrap token 已刷新）',
        command: res.install_command,
      })
    } catch (err) {
      setError(errorMessage(err))
    }
  }

  const onDelete = async (purge: 'xray' | 'agent') => {
    if (!deleteTarget) {
      return
    }
    setDeleting(true)
    try {
      await api.deleteServer(deleteTarget.id, purge)
      setDeleteTarget(null)
      load()
    } catch (err) {
      setError(errorMessage(err))
    } finally {
      setDeleting(false)
    }
  }

  return (
    <div className="space-y-4">
      <div className="flex items-center justify-between">
        <h1 className="text-xl font-semibold">服务器</h1>
        <Button onClick={() => setOpen(true)}>
          <PlusIcon />
          添加服务器
        </Button>
      </div>

      {error && <p className="text-sm text-destructive">加载失败：{error}</p>}

      <div className="rounded-lg border">
        <Table>
          <TableHeader>
            <TableRow>
              <TableHead>别名</TableHead>
              <TableHead>状态</TableHead>
              <TableHead>地址</TableHead>
              <TableHead>xray 版本</TableHead>
              <TableHead>最近在线</TableHead>
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
            ) : servers.length === 0 ? (
              <TableRow>
                <TableCell colSpan={7} className="text-center text-muted-foreground">
                  暂无服务器，点击右上角「添加服务器」开始
                </TableCell>
              </TableRow>
            ) : (
              servers.map((s) => (
                <TableRow key={s.id}>
                  <TableCell className="font-medium">{s.alias}</TableCell>
                  <TableCell>
                    {s.online ? (
                      <Badge variant="outline" className="border-green-200 bg-green-50 text-green-700">
                        在线
                      </Badge>
                    ) : (
                      <Badge variant="outline" className="border-gray-200 bg-gray-50 text-gray-500">
                        离线
                      </Badge>
                    )}
                  </TableCell>
                  <TableCell>{s.address || '-'}</TableCell>
                  <TableCell>{s.xray_version ?? '-'}</TableCell>
                  <TableCell>{formatTime(s.last_seen_at)}</TableCell>
                  <TableCell>{formatTime(s.created_at)}</TableCell>
                  <TableCell className="space-x-2">
                    <Button variant="outline" size="sm" onClick={() => onRotateToken(s)}>
                      {s.last_seen_at ? '凭证刷新' : '安装命令'}
                    </Button>
                    <Button variant="outline" size="sm" onClick={() => setDeleteTarget(s)}>
                      删除
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
            <DialogTitle>添加服务器</DialogTitle>
            <DialogDescription>输入别名创建服务器。</DialogDescription>
          </DialogHeader>
          <form onSubmit={onCreate} className="space-y-4">
            <div className="space-y-2">
              <Label htmlFor="alias">别名</Label>
              <Input
                id="alias"
                value={alias}
                onChange={(e) => setAlias(e.target.value)}
                placeholder="例如：hk-01"
                required
                autoFocus
              />
            </div>
            <div className="space-y-2">
              <Label htmlFor="address">公网地址</Label>
              <Input
                id="address"
                value={address}
                onChange={(e) => setAddress(e.target.value)}
                placeholder="留空按 agent 拨入地址自动学习"
              />
            </div>
            <div className="space-y-2">
              <Label htmlFor="xrayVersion">xray 版本</Label>
              <Input
                id="xrayVersion"
                value={xrayVersion}
                onChange={(e) => setXrayVersion(e.target.value)}
                placeholder="latest 或具体版本号（如 v26.3.27）"
              />
            </div>
            {createError && <p className="text-sm text-destructive">{createError}</p>}
            <DialogFooter>
              <Button type="submit" disabled={creating || !alias.trim()}>
                {creating ? '创建中…' : '创建'}
              </Button>
            </DialogFooter>
          </form>
        </DialogContent>
      </Dialog>

      <Dialog open={cmdView !== null} onOpenChange={(next) => !next && setCmdView(null)}>
        <DialogContent>
          <DialogHeader>
            <DialogTitle>安装命令</DialogTitle>
            <DialogDescription>{cmdView?.title}</DialogDescription>
          </DialogHeader>
          <div className="space-y-2">
            <pre className="max-h-40 overflow-auto rounded-lg bg-muted p-3 text-xs break-all whitespace-pre-wrap">
              {cmdView?.command}
            </pre>
          </div>
          <DialogFooter showCloseButton>
            <CopyButton text={cmdView?.command ?? ''} />
          </DialogFooter>
        </DialogContent>
      </Dialog>

      <Dialog open={deleteTarget !== null} onOpenChange={(next) => !next && setDeleteTarget(null)}>
        <DialogContent>
          <DialogHeader>
            <DialogTitle>删除服务器</DialogTitle>
            <DialogDescription>
              {deleteTarget?.online
                ? `确定删除「${deleteTarget.alias}」？将向 agent 发送卸载命令并删除记录，请选择卸载范围。`
                : `确定删除「${deleteTarget?.alias}」？当前离线，仅删除记录；该机上的 agent 需手动清理。`}
            </DialogDescription>
          </DialogHeader>
          <DialogFooter>
            {deleteTarget?.online ? (
              <>
                <Button variant="outline" disabled={deleting} onClick={() => onDelete('agent')}>
                  仅卸载 agent
                </Button>
                <Button variant="destructive" disabled={deleting} onClick={() => onDelete('xray')}>
                  连同 xray 卸载
                </Button>
              </>
            ) : (
              <Button variant="destructive" disabled={deleting} onClick={() => onDelete('xray')}>
                删除记录
              </Button>
            )}
          </DialogFooter>
        </DialogContent>
      </Dialog>
    </div>
  )
}
