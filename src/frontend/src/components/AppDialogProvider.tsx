import {
  useCallback,
  useRef,
  useState,
  type ReactNode,
} from 'react'

import { Button } from '@/components/ui/button'
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from '@/components/ui/dialog'
import {
  AppDialogContext,
  type ConfirmOptions,
  type NoticeOptions,
} from '@/lib/app-dialog'

type ActiveDialog =
  | ({ type: 'confirm' } & ConfirmOptions)
  | ({ type: 'notice' } & NoticeOptions)

export function AppDialogProvider({ children }: { children: ReactNode }) {
  const [activeDialog, setActiveDialog] = useState<ActiveDialog | null>(null)
  const resolverRef = useRef<((confirmed: boolean) => void) | null>(null)

  const openDialog = useCallback((dialog: ActiveDialog) => {
    resolverRef.current?.(false)
    setActiveDialog(dialog)
    return new Promise<boolean>((resolve) => {
      resolverRef.current = resolve
    })
  }, [])

  const confirm = useCallback(
    (options: ConfirmOptions) => openDialog({ type: 'confirm', ...options }),
    [openDialog],
  )

  const notify = useCallback(
    async (options: NoticeOptions) => {
      await openDialog({ type: 'notice', ...options })
    },
    [openDialog],
  )

  const settle = useCallback((confirmed: boolean) => {
    const resolve = resolverRef.current
    resolverRef.current = null
    setActiveDialog(null)
    resolve?.(confirmed)
  }, [])

  return (
    <AppDialogContext.Provider value={{ confirm, notify }}>
      {children}
      <Dialog open={activeDialog !== null} onOpenChange={(open) => !open && settle(false)}>
        <DialogContent showCloseButton={false}>
          <DialogHeader>
            <DialogTitle>{activeDialog?.title}</DialogTitle>
            <DialogDescription className="whitespace-pre-line">
              {activeDialog?.description}
            </DialogDescription>
          </DialogHeader>
          <DialogFooter>
            {activeDialog?.type === 'confirm' && (
              <Button variant="outline" onClick={() => settle(false)}>
                {activeDialog.cancelLabel ?? '取消'}
              </Button>
            )}
            <Button
              autoFocus
              variant={activeDialog?.type === 'confirm' && activeDialog.destructive ? 'destructive' : 'default'}
              onClick={() => settle(true)}
            >
              {activeDialog?.type === 'confirm'
                ? (activeDialog.confirmLabel ?? '确认')
                : (activeDialog?.closeLabel ?? '知道了')}
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>
    </AppDialogContext.Provider>
  )
}
