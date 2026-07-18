import type { BadgeTone, FindingPriority } from './scan-result'

/*
  Single source of truth for severity / status / AI color rendering.
  Everything routes through theme tokens — never raw palette (text-red-600…):
    • severity  → warm spectrum tokens (critical→destructive … info→info)
    • status    → semantic tokens (running→primary, ok→success, …)
    • AI actor  → the violet `ai` role
  See [[aiscan-web-redesign-direction]].
*/

/** BadgeTone (used by AssetResultView's Badge) → token classes. */
export const badgeToneClass: Record<BadgeTone, string> = {
  muted: 'bg-background/60 text-muted-foreground',
  cyan: 'bg-primary/10 text-primary',
  yellow: 'bg-warning/12 text-warning',
  green: 'bg-success/12 text-success',
  red: 'bg-destructive/12 text-destructive',
}

export interface SeverityTone {
  /** foreground text */
  text: string
  /** faint surface fill */
  bg: string
  /** hairline border */
  border: string
  /** solid swatch (severity dot / meter) */
  dot: string
  /** combined chip: bg + text + border */
  chip: string
}

/** Severity → role tokens. critical=destructive, high=caution, medium=warning, low=success, info=info. */
export const severityTone: Record<FindingPriority, SeverityTone> = {
  critical: { text: 'text-destructive', bg: 'bg-destructive/12', border: 'border-destructive/30', dot: 'bg-destructive', chip: 'border-destructive/30 bg-destructive/12 text-destructive' },
  high: { text: 'text-caution', bg: 'bg-caution/12', border: 'border-caution/30', dot: 'bg-caution', chip: 'border-caution/30 bg-caution/12 text-caution' },
  medium: { text: 'text-warning', bg: 'bg-warning/12', border: 'border-warning/30', dot: 'bg-warning', chip: 'border-warning/30 bg-warning/12 text-warning' },
  low: { text: 'text-success', bg: 'bg-success/12', border: 'border-success/30', dot: 'bg-success', chip: 'border-success/30 bg-success/12 text-success' },
  info: { text: 'text-info', bg: 'bg-info/12', border: 'border-info/30', dot: 'bg-info', chip: 'border-info/30 bg-info/12 text-info' },
}

/** The AI/agent role colour, for authorship + AI-emitted output. */
export const aiTone = {
  text: 'text-ai',
  dot: 'bg-ai',
  chip: 'border-ai/30 bg-ai/12 text-ai',
} as const

/**
 * Colourise a scan-log line by its content. Keyword groups mirror the kinds of
 * output aiscan emits (errors, findings, AI steps, web/service, summaries).
 */
export function logLineTone(line: string): string {
  const l = line.toLowerCase()
  if (/error|failed|fatal|panic|refused/.test(l)) return 'text-destructive'
  if (/vuln|critical|\bcve-|exploit/.test(l)) return 'text-destructive'
  if (/risk|weakpass|warn|medium/.test(l)) return 'text-caution'
  if (/\bai\b|verif|sniper|deep|analy|reason|llm/.test(l)) return aiTone.text
  if (/fingerprint|tech|stack/.test(l)) return 'text-warning'
  if (/web|service|http|port open|\b200\b/.test(l)) return 'text-success'
  if (/summary|complete|done|finished/.test(l)) return 'text-primary'
  return 'text-foreground/75'
}
