import { useCallback, useMemo, useState } from 'react'
import { Check } from 'lucide-react'
import { CSTXTable } from '@cyber/cstx'
import { Button } from '@cyber/ui'
import type { SCONode } from '@cyber/cstx-easm'
import type { MentionPopupApi } from '@/viewer'

interface AssetMentionPickerProps extends MentionPopupApi {
  nodes: SCONode[]
}

const EXCLUDE = ['cstx_id', '_raw', '_ip', '_port', '_cidr', '_root_domain']

function scoLabel(n: SCONode): string {
  const r = n as unknown as Record<string, unknown>
  return (
    (r.ip as string) || (r.host as string) || (r.url as string) ||
    (r.name as string) || (r.value as string) || (r.cidr as string) ||
    (r.fingerprint as string) || (r.hostname as string) ||
    (r.cstx_id as string) || ''
  )
}

function flatten(node: SCONode): Record<string, unknown> {
  const { cstx_type, cstx_id, ...rest } = node as unknown as Record<string, unknown> & { cstx_type: string; cstx_id: string }
  return { cstx_type, cstx_id, name: scoLabel(node), ...rest, _raw: node }
}

export default function AssetMentionPicker({ query, onSelect, onDismiss, nodes }: AssetMentionPickerProps) {
  const [pendingIds, setPendingIds] = useState<string[]>([])

  const filtered = useMemo(() => {
    if (!query) return nodes
    const q = query.toLowerCase()
    return nodes.filter((n) => scoLabel(n).toLowerCase().includes(q) || n.cstx_type.toLowerCase().includes(q))
  }, [nodes, query])

  const rows = useMemo(() => filtered.map(flatten), [filtered])

  const handleAction = useCallback((action: string, payload?: Record<string, unknown>) => {
    if (action === 'batchAction' && payload?.action === 'confirm') {
      const selected = payload.selectedRows as Record<string, unknown>[]
      if (selected?.length) {
        onSelect(selected.map((r) => (r.name as string) || (r.cstx_id as string) || ''))
      }
    }
  }, [onSelect])

  return (
    <div
      className="max-h-[50vh] overflow-hidden"
      onMouseDown={(e) => e.preventDefault()}
    >
      <div className="flex items-center justify-between border-b border-border/60 px-3 py-1.5">
        <span className="text-xs font-medium text-muted-foreground">
          {filtered.length} assets{query ? ` matching "${query}"` : ''}
        </span>
        <Button
          variant="ghost"
          size="xs"
          onClick={onDismiss}
          className="text-xs text-muted-foreground"
        >
          Esc
        </Button>
      </div>
      <div className="max-h-[calc(50vh-2.5rem)] overflow-auto">
        <CSTXTable
          data={{ rows, total: rows.length }}
          loading={{ rows: false }}
          errors={{ rows: null }}
          colSpan={4}
          config={{
            enableSearch: false,
            enableSorting: true,
            enablePagination: true,
            enableRowSelection: true,
            enableColoredTypes: true,
            typeFilterKey: 'cstx_type',
            rowIdKey: 'cstx_id',
            compact: true,
            pageSize: 20,
            columnsExclude: EXCLUDE,
            paginationMode: 'client',
            batchActions: [{ id: 'confirm', label: 'Insert selected', icon: 'Check' }],
          }}
          onAction={handleAction}
        />
      </div>
    </div>
  )
}
