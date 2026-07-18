import { useEffect, useRef } from 'react'

/**
 * Run `fn` every `intervalMs`, but pause while the tab is hidden and fire one
 * immediate catch-up when it returns to the foreground. A backgrounded tab
 * stops hitting the hub entirely, so idle/hidden tabs cost nothing (× however
 * many the operator has open).
 *
 * `fn` is held in a ref so passing an inline closure does NOT tear down and
 * recreate the interval on every render — only `intervalMs` / `enabled` restart
 * the loop. Callers keep their own initial-load effect; this hook owns only the
 * recurring, visibility-gated poll.
 */
export function usePolling(fn: () => void, intervalMs: number, enabled = true): void {
  const fnRef = useRef(fn)
  useEffect(() => {
    fnRef.current = fn
  })

  useEffect(() => {
    if (!enabled) return
    let timer: ReturnType<typeof setInterval> | null = null

    const tick = () => {
      if (document.visibilityState === 'visible') fnRef.current()
    }
    const start = () => {
      if (timer === null) timer = setInterval(tick, intervalMs)
    }
    const stop = () => {
      if (timer !== null) {
        clearInterval(timer)
        timer = null
      }
    }
    const onVisibility = () => {
      if (document.visibilityState === 'visible') {
        fnRef.current() // catch up immediately on return to the foreground
        start()
      } else {
        stop()
      }
    }

    if (document.visibilityState === 'visible') start()
    document.addEventListener('visibilitychange', onVisibility)
    return () => {
      stop()
      document.removeEventListener('visibilitychange', onVisibility)
    }
  }, [intervalMs, enabled])
}
