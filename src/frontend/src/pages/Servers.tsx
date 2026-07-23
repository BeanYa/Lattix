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
import type { CreateServerResponse, Server } from '@/lib/types'

function formatTime(t: string | null): string {
  return t ? new Date(t).toLocaleString() : '-'
}

export default function Servers() {
  const [servers, setServers] = useState<Server[]>([])
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState('')

  const [open, setOpen] = useState(false)
  const [alias, setAlias] = useState('')
  const [creating, setCreating] = useState(false)
  const [createError, setCreateError] = useState('')
  const [created, setCreated] = useState<CreateServerResponse | null>(null)

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
      setCreateError('')
      setCreated(null)
    }
  }

  const onCreate = async (e: FormEvent) => {
    e.preventDefault()
    setCreateError('')
    setCreating(true)
    try {
      const res = await api.createServer(alias.trim())
      setCreated(res)
      load()
    } catch (err) {
      setCreateError(errorMessage(err))
    } finally {
      setCreating(false)
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
              <TableHead>xray 版本</TableHead>
              <TableHead>最近在线</TableHead>
              <TableHead>创建时间</TableHead>
            </TableRow>
          </TableHeader>
          <TableBody>
            {loading ? (
              <TableRow>
                <TableCell colSpan={5} className="text-center text-muted-foreground">
                  加载中…
                </TableCell>
              </TableRow>
            ) : servers.length === 0 ? (
              <TableRow>
                <TableCell colSpan={5} className="text-center text-muted-foreground">
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
                  <TableCell>{s.xray_version ?? '-'}</TableCell>
                  <TableCell>{formatTime(s.last_seen_at)}</TableCell>
                  <TableCell>{formatTime(s.created_at)}</TableCell>
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
            <DialogDescription>
              {created ? '服务器已创建，请在目标机器上执行下面的安装命令。' : '输入别名创建服务器。'}
            </DialogDescription>
          </DialogHeader>
          {created ? (
            <div className="space-y-3">
              <div className="space-y-2">
                <Label>安装命令</Label>
                <pre className="max-h-40 overflow-auto rounded-lg bg-muted p-3 text-xs break-all whitespace-pre-wrap">
                  {created.install_command}
                </pre>
              </div>
              <DialogFooter showCloseButton>
                <CopyButton text={created.install_command} />
              </DialogFooter>
            </div>
          ) : (
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
              {createError && <p className="text-sm text-destructive">{createError}</p>}
              <DialogFooter>
                <Button type="submit" disabled={creating || !alias.trim()}>
                  {creating ? '创建中…' : '创建'}
                </Button>
              </DialogFooter>
            </form>
          )}
        </DialogContent>
      </Dialog>
    </div>
  )
}
