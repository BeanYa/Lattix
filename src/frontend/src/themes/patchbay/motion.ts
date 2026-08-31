// Short, interruptible feedback only. Persistent signal motion remains in CSS.
export const RACK_EASE = 'cubic-bezier(0.16, 1, 0.3, 1)'

export function hopFeedbackDelay(index: number) {
  return Math.min(Math.max(index, 0) * 45, 135)
}

export function nextChainIndex(key: string, index: number, count: number): number | null {
  if (!count) return null
  if (key === 'Home') return 0
  if (key === 'End') return count - 1
  if (key === 'ArrowDown' || key === 'ArrowRight') return Math.min(index + 1, count - 1)
  if (key === 'ArrowUp' || key === 'ArrowLeft') return Math.max(index - 1, 0)
  return null
}

export function animateRackFeedback(
  element: HTMLElement,
  frames: Keyframe[],
  options: KeyframeAnimationOptions = {},
): () => void {
  const preference = window.matchMedia('(prefers-reduced-motion: reduce)')
  const rect = element.getBoundingClientRect()
  if (
    preference.matches ||
    document.hidden ||
    !element.animate ||
    rect.bottom <= 0 ||
    rect.top >= window.innerHeight ||
    rect.right <= 0 ||
    rect.left >= window.innerWidth
  ) {
    return () => {}
  }
  const animation = element.animate(frames, { duration: 260, easing: RACK_EASE, ...options })
  const clean = () => {
    preference.removeEventListener('change', stop)
    document.removeEventListener('visibilitychange', onVisibility)
  }
  const stop = () => {
    animation.cancel()
    clean()
  }
  const onVisibility = () => {
    if (document.hidden) stop()
  }
  preference.addEventListener('change', stop)
  document.addEventListener('visibilitychange', onVisibility)
  void animation.finished.then(clean, clean)
  return stop
}
