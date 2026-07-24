import { useEffect, useState } from 'react'
import QRCode from 'qrcode'

import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogHeader,
  DialogTitle,
} from '@/components/ui/dialog'

// QRDialog 展示内容的二维码（订阅链接移动端扫码导入，§14）。
export function QRDialog({ text, open, onClose }: { text: string; open: boolean; onClose: () => void }) {
  const [dataURL, setDataURL] = useState('')

  useEffect(() => {
    if (!open || !text) {
      return
    }
    QRCode.toDataURL(text, { margin: 2, width: 320 })
      .then(setDataURL)
      .catch(() => setDataURL(''))
  }, [open, text])

  return (
    <Dialog open={open} onOpenChange={(next) => !next && onClose()}>
      <DialogContent className="max-w-sm">
        <DialogHeader>
          <DialogTitle>订阅二维码</DialogTitle>
          <DialogDescription>使用移动端客户端扫码导入订阅。</DialogDescription>
        </DialogHeader>
        <div className="flex justify-center">
          {dataURL ? (
            <img src={dataURL} alt="订阅二维码" className="rounded-lg border" />
          ) : (
            <p className="text-sm text-muted-foreground">生成中…</p>
          )}
        </div>
      </DialogContent>
    </Dialog>
  )
}
