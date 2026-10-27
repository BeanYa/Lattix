import { useState } from 'react'
import { QrCodeIcon } from 'lucide-react'

import { CopyButton } from '@/components/CopyButton'
import { QRDialog } from '@/components/QRDialog'
import { Notice } from '@/components/PagePrimitives'
import { Button } from '@/components/ui/button'
import {
  Dialog,
  DialogContent,
  DialogDescription,
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

import type { ShareLinkController } from './use-share-link'

/** 链路分享链接导出对话框（视图）；状态与数据加载在 useShareLink。 */
export function ShareLinkDialog({ controller }: { controller: ShareLinkController }) {
  const { target, users, userId, link, loading, error, close, selectUser } = controller
  const [qrOpen, setQrOpen] = useState(false)
  return (
    <>
      <Dialog open={target !== null} onOpenChange={(next) => !next && close()}>
        <DialogContent className="max-h-[85vh] overflow-y-auto sm:max-w-xl">
          <DialogHeader>
            <DialogTitle>导出分享链接</DialogTitle>
            <DialogDescription>
              「{target?.name}」按所选用户的订阅凭据生成连接链接（vless:// 等协议
              URI），与订阅交付一致。
            </DialogDescription>
          </DialogHeader>
          <div className="space-y-4">
            <div className="space-y-2">
              <Label htmlFor="share-link-user">订阅用户</Label>
              <Select
                value={userId === null ? undefined : String(userId)}
                items={users.map((u) => ({ value: String(u.id), label: u.name }))}
                onValueChange={(value) => selectUser(Number(value))}
              >
                <SelectTrigger id="share-link-user" className="w-64">
                  <SelectValue placeholder={users.length === 0 ? '无可用用户' : '选择用户'} />
                </SelectTrigger>
                <SelectContent>
                  {users.map((u) => (
                    <SelectItem key={u.id} value={String(u.id)}>
                      {u.name}
                    </SelectItem>
                  ))}
                </SelectContent>
              </Select>
            </div>
            {error ? <Notice tone="danger">{error}</Notice> : null}
            {loading ? <p className="cg-chain-dialog-note">生成中…</p> : null}
            {!loading && link ? (
              <div className="space-y-2">
                <div className="flex flex-wrap items-center gap-2 text-sm">
                  <span className="cg-chain-tag">{link.protocol}</span>
                  <span className="cg-muted">
                    {link.address}:{link.port}
                  </span>
                </div>
                <textarea
                  className="cg-textarea font-mono text-xs break-all"
                  rows={4}
                  readOnly
                  value={link.link}
                  onClick={(e) => e.currentTarget.select()}
                  aria-label="分享链接"
                />
                <div className="flex items-center gap-2">
                  <CopyButton text={link.link} />
                  <Button
                    type="button"
                    variant="outline"
                    size="sm"
                    onClick={() => setQrOpen(true)}
                    disabled={!link.link}
                  >
                    <QrCodeIcon />
                    二维码
                  </Button>
                </div>
              </div>
            ) : null}
          </div>
        </DialogContent>
      </Dialog>
      <QRDialog text={link?.link ?? ''} open={qrOpen} onClose={() => setQrOpen(false)} />
    </>
  )
}
