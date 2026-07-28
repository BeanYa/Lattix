import { createContext, useContext } from 'react'

export type ConfirmOptions = {
  title: string
  description: string
  confirmLabel?: string
  cancelLabel?: string
  destructive?: boolean
}

export type NoticeOptions = {
  title: string
  description: string
  closeLabel?: string
}

export type AppDialogContextValue = {
  confirm: (options: ConfirmOptions) => Promise<boolean>
  notify: (options: NoticeOptions) => Promise<void>
}

export const AppDialogContext = createContext<AppDialogContextValue | null>(null)

export function useAppDialog(): AppDialogContextValue {
  const context = useContext(AppDialogContext)
  if (!context) {
    throw new Error('useAppDialog must be used within AppDialogProvider')
  }
  return context
}
