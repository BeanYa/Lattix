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
  const [generated, setGenerated] = useState<{ text: string; dataURL: string } | null>(null)

  useEffect(() => {
    let cancelled = false
    if (!open || !text) {
      setGenerated(null)
      return
    }
    QRCode.toDataURL(text, { margin: 2, width: 320 })
      .then((dataURL) => {
        if (!cancelled) setGenerated({ text, dataURL })
      })
      .catch(() => {
        if (!cancelled) setGenerated(null)
      })
    return () => {
      cancelled = true
    }
  }, [open, text])

  const dataURL = generated?.text === text ? generated.dataURL : ''

  return (
    <Dialog open={open} onOpenChange={(next) => !next && onClose()}>
      <DialogContent className="max-w-sm">
        <DialogHeader>
          <DialogTitle>订阅二维码</DialogTitle>
          <DialogDescription>使用移动端客户端扫码导入订阅。</DialogDescription>
        </DialogHeader>
        <div className="cg-qr-box">
          {dataURL ? (
            <img src={dataURL} alt="订阅二维码" className="cg-qr-img" />
          ) : (
            <p className="cg-hint">生成中…</p>
          )}
        </div>
      </DialogContent>
    </Dialog>
  )
}
