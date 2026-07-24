import { useCallback, useEffect, useMemo, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { Box, Import, RefreshCw, Upload } from 'lucide-react'
import { listSCONodes, getSupportedArtifacts, importSCOData } from '../api'
import type { SCONode } from '@cyber/cstx-easm'
import { CSTXTable } from '@cyber/cstx'
import { CstxImportDialog, type ImportFileEntry, type ArtifactOption } from '@cyber/cstx'
import {
  Badge,
  Button,
  EmptyState,
  Sheet,
  SheetContent,
  SheetDescription,
  SheetTitle,
  Spinner,
  Tabs,
  TabsList,
  TabsTrigger,
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

const TYPE_ORDER = ['ip', 'cidr', 'domain', 'port', 'app', 'url', 'framework', 'endpoint', 'vuln']

function compareAssetTypes(left: string, right: string) {
  const leftIndex = TYPE_ORDER.indexOf(left)
  const rightIndex = TYPE_ORDER.indexOf(right)
  if (leftIndex >= 0 || rightIndex >= 0) {
    if (leftIndex < 0) return 1
    if (rightIndex < 0) return -1
    return leftIndex - rightIndex
  }
  return left.localeCompare(right)
}

export default function AssetPanel({ open, onClose, onSendToChat }: AssetPanelProps) {
  const { t } = useTranslation('assets')
  const importLabels = useMemo(() => ({
    title: t('importDialog.title'),
    description: t('importDialog.description'),
    dropTitle: t('importDialog.dropTitle'),
    dropDescription: t('importDialog.dropDescription'),
    dropHint: t('importDialog.dropHint'),
    browseText: t('importDialog.browseText'),
    loadingText: t('importDialog.loadingText'),
    artifactCount: t('importDialog.artifactCount', { count: '{{count}}' }),
    artifactLoading: t('importDialog.artifactLoading'),
    configLabel: t('importDialog.configLabel'),
    filesPending: t('importDialog.filesPending', { count: '{{count}}' }),
    fileColumn: t('importDialog.fileColumn'),
    precheckColumn: t('importDialog.precheckColumn'),
    detectionColumn: t('importDialog.detectionColumn'),
    summaryColumn: t('importDialog.summaryColumn'),
    artifactColumn: t('importDialog.artifactColumn'),
    formatColumn: t('importDialog.formatColumn'),
    actionColumn: t('importDialog.actionColumn'),
    structurePass: t('importDialog.structurePass'),
    structureFail: t('importDialog.structureFail'),
    needsType: t('importDialog.needsType'),
    rawArtifact: t('importDialog.rawArtifact'),
    formatPrefix: t('importDialog.formatPrefix'),
    preDetectedPrefix: t('importDialog.preDetectedPrefix'),
    matchedPrefix: t('importDialog.matchedPrefix'),
    artifactPlaceholder: t('importDialog.artifactPlaceholder'),
    artifactOptional: t('importDialog.artifactOptional'),
    removeFile: t('importDialog.removeFile'),
    invalidFile: t('importDialog.invalidFile', { name: '{{name}}', message: '{{message}}' }),
    missingArtifactType: t('importDialog.missingArtifactType', { name: '{{name}}' }),
    detectionSnapshot: t('importDialog.detectionSnapshot'),
    detectionBundle: t('importDialog.detectionBundle'),
    detectionResult: t('importDialog.detectionResult'),
    detectionResults: t('importDialog.detectionResults'),
    detectionArtifact: t('importDialog.detectionArtifact'),
    detectionUnknown: t('importDialog.detectionUnknown'),
    detectionArchive: t('importDialog.detectionArchive'),
    detectionConflict: t('importDialog.detectionConflict'),
    cancel: t('importDialog.cancel'),
    submit: t('importDialog.submit'),
    submitting: t('importDialog.submitting'),
  }), [t])
  const [nodes, setNodes] = useState<SCONode[]>([])
  const [loading, setLoading] = useState(false)
  const [error, setError] = useState<string | null>(null)
  const [importOpen, setImportOpen] = useState(false)
  const [artifactOptions, setArtifactOptions] = useState<ArtifactOption[]>([])
  const [artifactsLoading, setArtifactsLoading] = useState(false)
  const [dragOver, setDragOver] = useState(false)
  const [droppedFiles, setDroppedFiles] = useState<File[]>([])
  const [activeType, setActiveType] = useState('all')

  const handleDrop = useCallback((e: React.DragEvent) => {
    e.preventDefault()
    setDragOver(false)
    const files = Array.from(e.dataTransfer.files)
    if (files.length === 0) return
    setDroppedFiles(files)
    setImportOpen(true)
  }, [])

  const handleImportOpenChange = useCallback((next: boolean) => {
    setImportOpen(next)
    if (!next) setDroppedFiles([])
  }, [])

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

  const loadArtifacts = useCallback(async () => {
    setArtifactsLoading(true)
    try {
      const arts = await getSupportedArtifacts()
      setArtifactOptions((arts ?? []).map((a) => ({ value: a, label: a })))
    } catch { /* non-critical */ }
    finally { setArtifactsLoading(false) }
  }, [])

  useEffect(() => {
    if (open) void load()
  }, [open, load])

  useEffect(() => {
    if ((importOpen || dragOver) && artifactOptions.length === 0) void loadArtifacts()
  }, [importOpen, dragOver, artifactOptions.length, loadArtifacts])

  const rows = useMemo(() => nodes.map(flattenSCO), [nodes])
  const typeCounts = useMemo(() => {
    const counts = new Map<string, number>()
    for (const node of nodes) {
      const type = node.cstx_type || 'unknown'
      counts.set(type, (counts.get(type) ?? 0) + 1)
    }
    return counts
  }, [nodes])
  const assetTypes = useMemo(
    () => [...typeCounts.keys()].sort(compareAssetTypes),
    [typeCounts],
  )
  const visibleRows = useMemo(
    () => activeType === 'all' ? rows : rows.filter((row) => row.cstx_type === activeType),
    [activeType, rows],
  )

  useEffect(() => {
    if (activeType !== 'all' && !typeCounts.has(activeType)) setActiveType('all')
  }, [activeType, typeCounts])

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

  const handleImportSubmit = useCallback(async (entries: ImportFileEntry[]) => {
    for (const entry of entries) {
      await importSCOData(entry.file, entry.artifactType)
    }
    void load()
  }, [load])

  return (
    <>
      <Sheet open={open} onOpenChange={(next) => { if (!next) onClose() }}>
        <SheetContent
          side="right"
          className="flex w-full flex-col gap-0 border-l border-border/70 bg-card p-0 sm:max-w-[min(96rem,94vw)]"
          onDragOver={(e: React.DragEvent) => { e.preventDefault(); setDragOver(true) }}
          onDragLeave={(e: React.DragEvent) => { if (!e.currentTarget.contains(e.relatedTarget as Node)) setDragOver(false) }}
          onDrop={handleDrop}
        >
          {dragOver && (
            <div className="pointer-events-none absolute inset-0 z-20 flex items-center justify-center bg-primary/5 backdrop-blur-[1px]">
              <div className="flex items-center gap-2 rounded-xl border-2 border-dashed border-primary bg-card/90 px-6 py-4 text-sm font-medium text-primary shadow-lg">
                <Upload className="h-5 w-5" />
                {t('import')}
              </div>
            </div>
          )}

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
                size="xs"
                onClick={() => setImportOpen(true)}
                className="gap-1.5 text-muted-foreground"
              >
                <Import className="h-3.5 w-3.5" />
                {t('import')}
              </Button>
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
              <div className="flex min-h-0 flex-1 flex-col overflow-hidden">
                <Tabs value={activeType} onValueChange={setActiveType} className="shrink-0">
                  <div className="overflow-x-auto border-b border-border/60 px-4">
                    <TabsList className="h-auto min-w-max justify-start rounded-none bg-transparent p-0">
                      <AssetTypeTab value="all" label={t('allTypes')} count={rows.length} />
                      {assetTypes.map((type) => (
                        <AssetTypeTab
                          key={type}
                          value={type}
                          label={t(`types.${type}`, { defaultValue: type.toUpperCase() })}
                          count={typeCounts.get(type) ?? 0}
                        />
                      ))}
                    </TabsList>
                  </div>
                </Tabs>
                <div className="min-h-0 flex-1 overflow-auto">
                <CSTXTable
                  key={activeType}
                  data={{ rows: visibleRows, total: visibleRows.length }}
                  loading={{ rows: loading }}
                  errors={{ rows: null }}
                  colSpan={4}
                  config={{
                    enableSearch: true,
                    enableFieldSearch: true,
                    enableSorting: true,
                    // No pager: render every asset of the active type in one
                    // scrollable list (the panel body caps height and scrolls) so a
                    // small pool isn't hidden behind a "1-N of N" bar. Very large
                    // imports scroll rather than page.
                    enablePagination: false,
                    enableColumnResize: true,
                    enableRowSelection: true,
                    enableColoredTypes: true,
                    enableExport: true,
                    exportFormats: ['csv'],
                    columnSelector: true,
                    typeFilterKey: 'cstx_type',
                    rowIdKey: 'cstx_id',
                    compact: true,
                    // Classic table. The mixed "All" tab is a union of every asset
                    // type's schema, so type-specific columns (Port, URL, Frameworks…)
                    // are blank for most rows and push the table past the panel width.
                    // sparseColumnThreshold hides columns filled for fewer than half of
                    // the current tab's rows by default; sparseMinColumns then keeps the
                    // ~8 most-populated columns visible so the "All" tab lands on a
                    // useful overview (name, host, ip, url, port…) that fits without a
                    // horizontal scroll — never collapsing to a near-empty table. Each
                    // single-type tab, where its columns are fully populated, still
                    // shows them all. Nothing is removed: hidden columns stay one click
                    // away in the column selector.
                    sparseColumnThreshold: 0.5,
                    sparseMinColumns: 8,
                    columnsExclude: EXCLUDE_COLUMNS,
                    batchActions: BATCH_ACTIONS,
                  }}
                  onAction={handleAction}
                />
                </div>
              </div>
            )}
          </div>
        </SheetContent>
      </Sheet>

      <CstxImportDialog
        open={importOpen}
        onOpenChange={handleImportOpenChange}
        onSubmit={handleImportSubmit}
        artifactOptions={artifactOptions}
        artifactOptionsLoading={artifactsLoading}
        labels={importLabels}
        droppedFiles={droppedFiles}
      />
    </>
  )
}

function AssetTypeTab({ value, label, count }: { value: string; label: string; count: number }) {
  return (
    <TabsTrigger
      value={value}
      className="-mb-px mr-5 h-10 gap-1.5 rounded-none border-b-2 border-transparent bg-transparent px-0 py-0 text-xs shadow-none last:mr-0 data-[state=active]:border-primary data-[state=active]:bg-transparent data-[state=active]:text-foreground data-[state=active]:shadow-none"
    >
      <span>{label}</span>
      <span className="font-mono text-[10px] tabular-nums text-muted-foreground">{count}</span>
    </TabsTrigger>
  )
}

export function assetMentionables(nodes: SCONode[]): { target: string; label?: string; source?: string }[] {
  return nodes.map((n) => ({
    target: scoLabel(n),
    label: scoLabel(n),
    source: n.cstx_type,
  }))
}
