export function stripAnsiControl(value: string): string {
  // eslint-disable-next-line no-control-regex
  const ansiPattern = /[\x1B\x9B][[\]()#;?]*(?:(?:(?:[a-zA-Z\d]*(?:;[a-zA-Z\d]*)*)?\x07)|(?:(?:\d{1,4}(?:;\d{0,4})*)?[\dA-PR-TZcf-nq-uy=><~]))/g
  return value.replace(ansiPattern, '').replace(/\r\n?/g, '\n')
}

export function formatArgs(args: string): string {
  try {
    return JSON.stringify(JSON.parse(args), null, 2)
  } catch {
    return args
  }
}

export function summarizeArgs(args: string): string {
  const raw = args.trim()
  if (!raw) return ''
  try {
    return compactSummary(summaryFromValue(JSON.parse(raw)))
  } catch {
    return compactSummary(raw)
  }
}

function summaryFromValue(value: unknown): string {
  if (value == null) return ''
  if (typeof value === 'string') return value
  if (typeof value === 'number' || typeof value === 'boolean') return String(value)
  if (Array.isArray(value)) return value.map(summaryFromValue).filter(Boolean).join(' ')
  if (typeof value !== 'object') return ''

  const record = value as Record<string, unknown>
  const preferredKeys = [
    'command', 'cmd', 'query', 'url', 'target', 'input', 'prompt',
    'text', 'content', 'path', 'pattern', 'selector', 'code', 'args',
  ]
  for (const key of preferredKeys) {
    const summary = summaryFromValue(record[key])
    if (summary) return summary
  }
  for (const item of Object.values(record)) {
    const summary = summaryFromValue(item)
    if (summary) return summary
  }
  try { return JSON.stringify(value) } catch { return '' }
}

function compactSummary(value: string): string {
  return value.replace(/\s+/g, ' ').trim()
}
