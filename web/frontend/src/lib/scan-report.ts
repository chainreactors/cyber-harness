import type { SCONode } from '@cyber/cstx-easm'

export function buildCSTXMarkdownReport(scanID: string, nodes: readonly SCONode[], language: 'en' | 'zh'): string {
  const grouped = new Map<string, SCONode[]>()
  for (const node of nodes) {
    const type = node.cstx_type || 'unknown'
    const values = grouped.get(type) ?? []
    values.push(node)
    grouped.set(type, values)
  }

  const chinese = language === 'zh'
  const lines = [
    `# ${chinese ? '扫描报告' : 'Scan Report'}`,
    '',
    `- ${chinese ? '扫描' : 'Scan'}: \`${escapeCode(scanID)}\``,
    `- ${chinese ? '事实节点' : 'Fact nodes'}: ${nodes.length}`,
    '',
    `## ${chinese ? '概览' : 'Overview'}`,
    '',
    `| ${chinese ? '类型' : 'Type'} | ${chinese ? '数量' : 'Count'} |`,
    '| --- | ---: |',
  ]
  for (const [type, values] of [...grouped].sort(([left], [right]) => left.localeCompare(right))) {
    lines.push(`| ${escapeTable(type)} | ${values.length} |`)
  }
  if (grouped.size === 0) lines.push(`| ${chinese ? '无' : 'None'} | 0 |`)

  for (const [type, values] of [...grouped].sort(([left], [right]) => left.localeCompare(right))) {
    lines.push('', `## ${type} (${values.length})`, '')
    for (const node of values) lines.push(`- ${describeNode(node)}`)
  }
  return `${lines.join('\n')}\n`
}

function describeNode(node: SCONode): string {
  const record = node as unknown as Record<string, unknown>
  const details = Object.entries(record).flatMap(([field, value]) => {
    if (field === 'cstx_id' || field === 'cstx_type') return []
    if (value === undefined || value === null || value === '' || typeof value === 'object') return []
    return [`${field}=\`${escapeCode(String(value))}\``]
  })
  return details.length > 0 ? details.join(', ') : `\`${escapeCode(node.cstx_id)}\``
}

function escapeCode(value: string): string {
  return value.replace(/`/g, '\\`').replace(/\n/g, ' ')
}

function escapeTable(value: string): string {
  return value.replace(/\|/g, '\\|').replace(/\n/g, ' ')
}
