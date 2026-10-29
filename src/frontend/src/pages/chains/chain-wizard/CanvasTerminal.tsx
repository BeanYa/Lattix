import { ArrowRightIcon, GlobeIcon } from 'lucide-react'
import { cn } from '@/lib/utils'
import { INTERNET_R } from './chain-canvas'
import { useWorldDrag } from './use-world-drag'

export function CanvasTerminal({
  kind,
  position,
  connected,
  connecting,
  toWorld,
  onMove,
  onPortClick,
}: {
  kind: 'ingress' | 'internet'
  position: { x: number; y: number }
  connected: boolean
  connecting: boolean
  toWorld: (x: number, y: number) => { x: number; y: number }
  onMove: (x: number, y: number) => void
  onPortClick: () => void
}) {
  const drag = useWorldDrag(position, toWorld, onMove)
  const ingress = kind === 'ingress'
  const label = ingress ? '入网流量' : 'Internet'
  const Icon = ingress ? ArrowRightIcon : GlobeIcon
  return (
    <div
      className={cn('cw-internet', ingress && 'cw-ingress', drag.dragging && 'is-dragging')}
      style={{ left: position.x - INTERNET_R, top: position.y - INTERNET_R }}
      {...drag.handlers}
    >
      <div className="cw-internet-ball" style={{ width: INTERNET_R * 2, height: INTERNET_R * 2 }}>
        <Icon aria-hidden />
      </div>
      <span className="cw-internet-label">{label}</span>
      <button
        type="button"
        className={cn(
          'cw-port',
          ingress ? 'cw-ingress-port' : 'is-internet',
          connected && 'is-connected',
          connecting && 'is-target',
        )}
        aria-label={ingress ? '入网流量出口端口' : 'Internet 入口端口'}
        title={
          ingress
            ? '点击连接或重新连接入口服务器'
            : connecting
              ? '点击完成连接'
              : '先点击上游出口开始连接'
        }
        onClick={(event) => {
          event.stopPropagation()
          onPortClick()
        }}
      />
    </div>
  )
}
