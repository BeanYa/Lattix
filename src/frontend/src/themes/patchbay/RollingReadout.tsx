import { useLayoutEffect, useRef } from 'react'

import { animateRackFeedback } from './motion'

/** Counter-inspired instrument feedback: show actual readings, never interpolated telemetry. */
export function RollingReadout({ value }: { value: string }) {
  const previous = useRef(value)
  const outgoing = useRef<HTMLSpanElement>(null)
  const incoming = useRef<HTMLSpanElement>(null)

  useLayoutEffect(() => {
    const before = previous.current
    previous.current = value
    if (before === value || !outgoing.current || !incoming.current) return
    outgoing.current.textContent = before
    const stopOld = animateRackFeedback(outgoing.current, [
      { transform: 'translateY(0)', opacity: 1 },
      { transform: 'translateY(-100%)', opacity: 0 },
    ])
    const stopNew = animateRackFeedback(incoming.current, [
      { transform: 'translateY(100%)', opacity: 0 },
      { transform: 'translateY(0)', opacity: 1 },
    ])
    return () => {
      stopOld()
      stopNew()
    }
  }, [value])

  return (
    <span className="pb-readout">
      <span className="sr-only">{value}</span>
      <span className="pb-readout-window" aria-hidden="true">
        <span className="pb-readout-old" ref={outgoing} />
        <span className="pb-readout-current" ref={incoming}>
          {value}
        </span>
      </span>
    </span>
  )
}
