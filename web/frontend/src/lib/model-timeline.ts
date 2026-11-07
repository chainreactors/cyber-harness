import type { AOPEvent } from '@/viewer'

// Usage closes a logical model call, including its retry attempts. Repeated
// llm_request events before that receipt reuse the same assistant message ID;
// splitting them would detach the final usage from the retried response.
export function modelTimelineEvents(events: AOPEvent[]): AOPEvent[] {
  const pending = new Set<string>()
  return events.filter(event => {
    const key = JSON.stringify([event.sessionId, event.turnId, event.emitter])
    if (event.payload.case === 'usage') pending.delete(key)
    if (event.payload.case !== 'status' || event.payload.value.state !== 'llm_request') return true
    if (pending.has(key)) return false
    pending.add(key)
    return true
  })
}
