import type { ReactNode } from 'react'
import { cn } from '@cyber/theme'

export interface AiPanelProps {
  /** Cortex-blue mono eyebrow — the analysis source (验证 / CVE 情报 / 研判). */
  label: ReactNode
  /** Optional trailing header control, e.g. a hide/collapse button. */
  action?: ReactNode
  /** On the outer box: margins, or `max-h-* overflow-auto` to scroll the whole panel. */
  className?: string
  /** On the body wrapper: `max-h-* overflow-auto` to scroll only the content. */
  bodyClassName?: string
  children: ReactNode
}

/**
 * The Cortex "AI 研判" panel — a Cortex-blue left-edged, ai-tinted box with a
 * mono eyebrow over an analysis body (markdown or text). The signature the
 * findings / asset views hand-rolled per call site (`border-l-4 border-l-ai
 * bg-ai/5` + `mono-label text-ai`). Kept aiscan-local because it leans on the
 * app-local `ai` role token, which isn't in the shared `@cyber/theme`.
 */
export function AiPanel({ label, action, className, bodyClassName, children }: AiPanelProps) {
  return (
    <div className={cn('rounded-md border-l-4 border-l-ai bg-ai/5 p-3', className)}>
      <div className="mb-1.5 flex items-center justify-between gap-2">
        <span className="mono-label text-ai">{label}</span>
        {action}
      </div>
      <div className={cn('text-muted-foreground', bodyClassName)}>{children}</div>
    </div>
  )
}
