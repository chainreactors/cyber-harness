import { useCallback, useEffect, useMemo, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { Box, RefreshCw } from 'lucide-react'
import { listSCONodes } from '../api'
import type { SCONode } from '@cyber/cstx-easm'
import { CSTXTable } from '@cyber/cstx'
import {
  Badge,
  Button,
  EmptyState,
  Sheet,
  SheetContent,
  SheetDescription,
  SheetTitle,
  Spinner,
} from '@cyber/ui'
import { cn } from '@cyber/theme'

interface AssetPanelProps {
  open: boolean
  onClose: () => void
  onSendToChat?: (text: string) => void
}

const EXCLUDE_COLUMNS = [
  'cstx_id', '_raw', '_ip', '_port', '_cidr', '_root_domain',
]

function scoLabel(node: SCONode): string {
  const n = node as unknown as Record<string, unknown>
  return (
    (n.ip as string) ||
    (n.host as string) ||
    (n.url as string) ||
    (n.name as string) ||
    (n.value as string) ||
    (n.cidr as string) ||
    (n.fingerprint as string) ||
    (n.endpoint as string) ||
    (n.hostname as string) ||
    (n.icp as string) ||
    (n.cstx_id as string) ||
    ''
  )
}

function flattenSCO(node: SCONode): Record<string, unknown> {
  const { cstx_type, cstx_id, ...rest } = node as unknown as Record<string, unknown> & { cstx_type: string; cstx_id: string }
  return {
    cstx_type,
    cstx_id,
    name: scoLabel(node),
    ...rest,
    _raw: node,
  }
}

function formatRowsForChat(rows: Record<string, unknown>[]): string {
  const grouped: Record<string, Record<string, unknown>[]> = {}
  for (const r of rows) {
    const type = (r.cstx_type as string) || 'unknown'
    ;(grouped[type] ??= []).push(r)
  }
  const lines: string[] = []
  for (const [type, items] of Object.entries(grouped)) {
    lines.push(`[${type}]`)
    for (const item of items) {
      lines.push(`  ${(item.name as string) || (item.cstx_id as string) || ''}`)
    }
  }
  return lines.join('\n')
}

const BATCH_ACTIONS = [
  { id: 'sendToChat', label: 'Send to Chat', icon: 'MessageSquare' },
]

export default function AssetPanel({ open, onClose, onSendToChat }: AssetPanelProps) {
  const { t } = useTranslation('assets')
  const [nodes, setNodes] = useState<SCONode[]>([])
  const [loading, setLoading] = useState(false)
  const [error, setError] = useState<string | null>(null)

  const load = useCallback(async () => {
    setLoading(true)
    setError(null)
    try {
      const data = await listSCONodes({ limit: 5000 })
      setNodes(data)
    } catch (e) {
      setError(e instanceof Error ? e.message : String(e))
    } finally {
      setLoading(false)
    }
  }, [])

  useEffect(() => {
    if (open) void load()
  }, [open, load])

  const rows = useMemo(() => nodes.map(flattenSCO), [nodes])

  const handleAction = useCallback((action: string, payload?: Record<string, unknown>) => {
    if (action === 'cellClick' && payload?.value) {
      void navigator.clipboard?.writeText(String(payload.value))
    }
    if (action === 'batchAction' && payload?.action === 'sendToChat' && onSendToChat) {
      const selected = payload.selectedRows as Record<string, unknown>[]
      if (selected?.length) {
        onSendToChat(formatRowsForChat(selected))
        onClose()
      }
    }
  }, [onSendToChat, onClose])

  return (
    <Sheet open={open} onOpenChange={(next) => { if (!next) onClose() }}>
      <SheetContent
        side="right"
        className="flex w-full flex-col gap-0 border-l border-border/70 bg-card p-0 sm:max-w-5xl"
      >
        <div className="flex h-12 shrink-0 items-center justify-between border-b border-border/60 px-4 pr-12">
          <div className="flex min-w-0 items-center gap-3">
            <Box className="h-4 w-4 shrink-0 text-primary" />
            <div className="flex min-w-0 items-center gap-2">
              <SheetTitle className="text-sm font-medium text-foreground">{t('title')}</SheetTitle>
              <Badge variant="secondary" size="sm" className="py-0 font-mono font-normal">
                {nodes.length}
              </Badge>
            </div>
          </div>
          <div className="flex items-center gap-2">
            <Button
              variant="ghost"
              size="icon-xs"
              onClick={load}
              disabled={loading}
              className="text-muted-foreground"
            >
              <RefreshCw className={cn('h-3.5 w-3.5', loading && 'animate-spin')} />
            </Button>
          </div>
        </div>
        <SheetDescription className="sr-only">{t('openAssets')}</SheetDescription>

        <div className="flex min-h-0 flex-1 flex-col overflow-hidden">
          {error ? (
            <div className="flex flex-1 items-center justify-center p-8">
              <EmptyState compact title={error} />
            </div>
          ) : loading && nodes.length === 0 ? (
            <div className="flex flex-1 items-center justify-center">
              <Spinner size="md" />
            </div>
          ) : nodes.length === 0 ? (
            <div className="flex flex-1 items-center justify-center p-8">
              <EmptyState compact title={t('noAssets')} description={t('noAssetsHint')} />
            </div>
          ) : (
            <div className="min-h-0 flex-1 overflow-auto">
              <CSTXTable
                data={{ rows, total: rows.length }}
                loading={{ rows: loading }}
                errors={{ rows: null }}
                colSpan={4}
                config={{
                  enableSearch: true,
                  enableFieldSearch: true,
                  enableSorting: true,
                  enablePagination: true,
                  enableColumnResize: true,
                  enableRowSelection: true,
                  enableColoredTypes: true,
                  enableExport: true,
                  exportFormats: ['csv'],
                  columnSelector: true,
                  typeFilterKey: 'cstx_type',
                  rowIdKey: 'cstx_id',
                  compact: true,
                  pageSize: 50,
                  columnsExclude: EXCLUDE_COLUMNS,
                  paginationMode: 'client',
                  batchActions: BATCH_ACTIONS,
                }}
                onAction={handleAction}
              />
            </div>
          )}
        </div>
      </SheetContent>
    </Sheet>
  )
}

export function assetMentionables(nodes: SCONode[]): { target: string; label?: string; source?: string }[] {
  return nodes.map((n) => ({
    target: scoLabel(n),
    label: scoLabel(n),
    source: n.cstx_type,
  }))
}
