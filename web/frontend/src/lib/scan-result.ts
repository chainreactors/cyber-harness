import type { SCOResultModel } from '@cyber/cstx-easm'

export type { SCOResultModel, SCOHostGroup, SCOPortNode, SCOMetrics } from '@cyber/cstx-easm'

export type BadgeTone = 'muted' | 'cyan' | 'yellow' | 'green' | 'red'

export function statusCodeTone(status?: string): BadgeTone {
  const code = Number(status)
  if (!Number.isFinite(code)) {
    return 'muted'
  }
  if (code >= 500) return 'red'
  if (code >= 400) return 'yellow'
  if (code >= 200 && code < 400) return 'green'
  return 'muted'
}

// --- AI Findings helpers ---

export const PRIORITY_ORDER = ['critical', 'high', 'medium', 'low', 'info'] as const
export type FindingPriority = (typeof PRIORITY_ORDER)[number]

export type FindingItem = {
  id: string
  kind: 'vuln' | 'weakpass' | 'fingerprint' | 'note' | 'other'
  priority: FindingPriority
  title: string
  target: string
  description?: string
  source?: string
  status?: string
  tags: string[]
  detail?: string
}

/**
 * A finding's target is "navigable" only when it is an absolute http(s) URL we
 * can open in a browser tab. Bare host:port / service targets (e.g. an ssh
 * service on :22) return null so the UI never renders a link that 404s or
 * points at a non-web port. Lets findings click through to the live target.
 */
export function findingTargetURL(target?: string): string | null {
  const parsed = parseURL(target)
  if (!parsed) return null
  if (parsed.protocol !== 'http:' && parsed.protocol !== 'https:') return null
  return parsed.href
}

// Scan results no longer travel as an inline output.Result — the hub persists
// them as SCO nodes keyed by scan_id. Findings are derived from the vuln nodes
// in the SCO model the scan's nodes build.
export function buildFindingsFromSCO(model: SCOResultModel): FindingItem[] {
  const findings: FindingItem[] = []
  const seen = new Set<string>()

  for (const host of model.hosts) {
    for (const node of host.ports) {
      for (const vuln of node.vulns) {
        const target = vuln.url || (vuln.ip && vuln.port ? `${vuln.ip}:${vuln.port}` : vuln.ip || host.ip.ip || '')
        const title = vuln.name || vuln.vuln_id || vuln.value || 'Finding'
        const id = `vuln:${vuln.cstx_id || `${target}:${title}`}`
        if (seen.has(id)) continue
        seen.add(id)
        findings.push({
          id,
          kind: vuln.username ? 'weakpass' : 'vuln',
          priority: normalizePriority(vuln.severity),
          title,
          target,
          description: vuln.vuln_id && vuln.vuln_id !== title ? vuln.vuln_id : undefined,
          tags: vuln.tags || [],
          detail: [vuln.request, vuln.response].filter(Boolean).join('\n\n') || undefined,
        })
      }
    }
  }

  findings.sort((a, b) => PRIORITY_ORDER.indexOf(a.priority) - PRIORITY_ORDER.indexOf(b.priority))
  return findings
}

function normalizePriority(priority?: string): FindingPriority {
  const p = (priority || '').toLowerCase()
  if (PRIORITY_ORDER.includes(p as FindingPriority)) return p as FindingPriority
  return 'info'
}

function parseURL(value?: string) {
  if (!value) {
    return null
  }
  try {
    return new URL(value)
  } catch {
    return null
  }
}
