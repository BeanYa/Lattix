// @vitest-environment happy-dom
import { act } from 'react'
import { createRoot, type Root } from 'react-dom/client'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from './dialog'

describe('ui/dialog', () => {
  let container: HTMLDivElement
  let root: Root | undefined

  beforeEach(() => {
    container = document.createElement('div')
    document.body.appendChild(container)
  })

  afterEach(() => {
    act(() => root?.unmount())
    root = undefined
    container.remove()
  })

  it('closes on backdrop click', async () => {
    const onOpenChange = vi.fn()
    await act(async () => {
      root = createRoot(container)
      root.render(
        <Dialog open onOpenChange={onOpenChange}>
          <DialogContent>
            <DialogHeader>
              <DialogTitle>标题</DialogTitle>
              <DialogDescription>描述</DialogDescription>
            </DialogHeader>
            <div style={{ height: '200vh' }} />
          </DialogContent>
        </Dialog>,
      )
    })
    const backdrop = document.querySelector('[data-slot="dialog-overlay"]')
    expect(backdrop).toBeTruthy()
    await act(async () => {
      backdrop!.dispatchEvent(new MouseEvent('click', { bubbles: true, cancelable: true }))
    })
    expect(onOpenChange).toHaveBeenCalled()
    expect(onOpenChange.mock.calls[0][0]).toBe(false)
  })

  it('long-content dialogs keep the close button fixed outside the scroll area', async () => {
    await act(async () => {
      root = createRoot(container)
      root.render(
        <Dialog open onOpenChange={vi.fn()}>
          <DialogContent className="flex max-h-[90vh] flex-col overflow-hidden">
            <DialogHeader>
              <DialogTitle>标题</DialogTitle>
            </DialogHeader>
            <div className="min-h-0 flex-1 overflow-y-auto">
              <div style={{ height: '200vh' }} />
            </div>
            <DialogFooter>
              <button type="button">关闭</button>
            </DialogFooter>
          </DialogContent>
        </Dialog>,
      )
    })
    const popup = document.querySelector('[data-slot="dialog-content"]') as HTMLElement
    const close = document.querySelector('[data-slot="dialog-close"]') as HTMLElement
    expect(popup).toBeTruthy()
    expect(close).toBeTruthy()
    // 弹窗本身不再是滚动容器，绝对定位的 X 固定在右上角；滚动发生在内部滚动体上。
    expect(popup.classList.contains('overflow-hidden')).toBe(true)
    expect(popup.classList.contains('overflow-y-auto')).toBe(false)
  })
})
