import { useCallback, useEffect, useMemo, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { Box, FileUp, MessageSquare, Monitor, Network } from 'lucide-react'
import { CSTXTable } from '@cyber/cstx'
import { Button } from '@cyber/ui'
import { cn } from '@cyber/theme'
import type { SCONode } from '@cyber/cstx-easm'
import type { MentionPopupApi } from '@/viewer'
import type { IOAMessage, IOANode } from '../api'

// The @-mention popup, Cursor-style: a category rail (CSTX assets / IOA nodes +
// messages / File) sits above a per-category picker. `query` is the text typed
// after "@" — it filters the active category, and the count badge on every tab
// updates against it so an operator can see which category actually has hits.
interface MentionPickerProps extends MentionPopupApi {
  nodes: SCONode[]
  ioaNodes: IOANode[]
  ioaMessages: IOAMessage[]
}

type Category = 'cstx' | 'ioa' | 'file'

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

function ioaNodeLabel(n: IOANode): string {
  return n.name || n.id
}

// IOA message content is a free-form record; prefer a human title, then the
// declared content type, then the id. This is both the row label and the token
// spliced into the composer (readable text, consistent with asset labels).
function ioaMessageLabel(m: IOAMessage): string {
  const c = m.content || {}
  for (const key of ['title', 'subject', 'name', 'summary']) {
    const v = c[key]
    if (typeof v === 'string' && v.trim()) return v.trim()
  }
  return m.content_type || m.id
}

export default function MentionPicker({
  query,
  onSelect,
  onDismiss,
  onAttach,
  nodes,
  ioaNodes,
  ioaMessages,
}: MentionPickerProps) {
  const { t } = useTranslation('chat')
  const q = query.trim().toLowerCase()

  const filteredNodes = useMemo(() => {
    if (!q) return nodes
    return nodes.filter((n) => scoLabel(n).toLowerCase().includes(q) || n.cstx_type.toLowerCase().includes(q))
  }, [nodes, q])

  const filteredIoaNodes = useMemo(() => {
    if (!q) return ioaNodes
    return ioaNodes.filter((n) => ioaNodeLabel(n).toLowerCase().includes(q) || (n.description || '').toLowerCase().includes(q))
  }, [ioaNodes, q])

  const filteredIoaMessages = useMemo(() => {
    if (!q) return ioaMessages
    return ioaMessages.filter((m) => ioaMessageLabel(m).toLowerCase().includes(q) || (m.sender || '').toLowerCase().includes(q))
  }, [ioaMessages, q])

  const cstxCount = filteredNodes.length
  const ioaCount = filteredIoaNodes.length + filteredIoaMessages.length

  // A category is offered when it has something to show: assets present, any IOA
  // node/message present, or (File) the host composer accepts attachments.
  const categories = useMemo(() => {
    const list: { id: Category; label: string; icon: typeof Box; count?: number }[] = []
    if (nodes.length) list.push({ id: 'cstx', label: t('mention.cstx'), icon: Box, count: cstxCount })
    if (ioaNodes.length || ioaMessages.length) list.push({ id: 'ioa', label: t('mention.ioa'), icon: Network, count: ioaCount })
    if (onAttach) list.push({ id: 'file', label: t('mention.file'), icon: FileUp })
    return list
  }, [nodes.length, ioaNodes.length, ioaMessages.length, onAttach, cstxCount, ioaCount, t])

  const [active, setActive] = useState<Category>(() => categories[0]?.id ?? 'cstx')
  const [userPicked, setUserPicked] = useState(false)

  // Keep the active tab valid as availability shifts (e.g. IOA drains).
  useEffect(() => {
    if (categories.length && !categories.some((c) => c.id === active)) {
      setActive(categories[0].id)
    }
  }, [categories, active])

  // Cursor-like "type to find anything": until the operator taps a tab, follow
  // the query to the first content category that actually matches, so typing an
  // IOA node name while parked on CSTX doesn't dead-end on an empty list.
  useEffect(() => {
    if (userPicked || !q) return
    const activeHasHits = (active === 'cstx' && cstxCount > 0) || (active === 'ioa' && ioaCount > 0)
    if (activeHasHits) return
    const next = categories.find((c) => (c.id === 'cstx' && cstxCount > 0) || (c.id === 'ioa' && ioaCount > 0))
    if (next && next.id !== active) setActive(next.id)
  }, [q, userPicked, active, cstxCount, ioaCount, categories])

  const pickTab = useCallback((id: Category) => {
    setActive(id)
    setUserPicked(true)
  }, [])

  const rows = useMemo(() => filteredNodes.map(flatten), [filteredNodes])

  const handleCstxAction = useCallback((action: string, payload?: Record<string, unknown>) => {
    if (action === 'batchAction' && payload?.action === 'confirm') {
      const selected = payload.selectedRows as Record<string, unknown>[]
      if (selected?.length) {
        onSelect(selected.map((r) => (r.name as string) || (r.cstx_id as string) || ''))
      }
    }
  }, [onSelect])

  return (
    <div
      className="flex max-h-[52vh] flex-col overflow-hidden"
      onMouseDown={(e) => e.preventDefault()}
    >
      <div className="flex items-center gap-1 border-b border-border/60 px-2 py-1.5">
        {categories.map(({ id, label, icon: Icon, count }) => (
          <button
            key={id}
            type="button"
            onClick={() => pickTab(id)}
            className={cn(
              'inline-flex items-center gap-1.5 rounded-md px-2 py-1 text-xs font-medium transition-colors',
              id === active
                ? 'bg-primary/10 text-primary'
                : 'text-muted-foreground hover:bg-muted/60 hover:text-foreground',
            )}
          >
            <Icon className="h-3.5 w-3.5" aria-hidden="true" />
            <span>{label}</span>
            {typeof count === 'number' && (
              <span className={cn('font-mono text-[10px] tabular-nums', id === active ? 'text-primary/80' : 'text-muted-foreground/70')}>
                {count}
              </span>
            )}
          </button>
        ))}
        <Button
          variant="ghost"
          size="xs"
          onClick={onDismiss}
          className="ml-auto text-xs text-muted-foreground"
        >
          Esc
        </Button>
      </div>

      <div className="min-h-0 flex-1 overflow-auto">
        {active === 'cstx' && (
          <CSTXTable
            data={{ rows, total: rows.length }}
            loading={{ rows: false }}
            errors={{ rows: null }}
            colSpan={4}
            config={{
              enableSearch: false,
              enableSorting: true,
              // No pager: list every match in one scrollable view (the popup body
              // already caps height and scrolls). This is a quick pick-and-insert
              // surface, so an operator scans and selects without clicking through
              // pages. `layout: 'cards'` renders each asset as a card of all its
              // populated fields — every field readable in the narrow popup, no
              // horizontal scroll, nothing traded away to fit.
              layout: 'cards',
              enablePagination: false,
              enableRowSelection: true,
              enableColoredTypes: true,
              typeFilterKey: 'cstx_type',
              rowIdKey: 'cstx_id',
              compact: true,
              columnsExclude: EXCLUDE,
              batchActions: [{ id: 'confirm', label: t('mention.insert'), icon: 'Check' }],
            }}
            onAction={handleCstxAction}
          />
        )}

        {active === 'ioa' && (
          <IoaList
            nodes={filteredIoaNodes}
            messages={filteredIoaMessages}
            onSelect={(target) => onSelect([target])}
          />
        )}

        {active === 'file' && onAttach && (
          <div className="flex flex-col items-center gap-3 px-4 py-8 text-center">
            <span className="grid h-11 w-11 place-items-center rounded-full bg-accent text-primary">
              <FileUp className="h-5 w-5" aria-hidden="true" />
            </span>
            <p className="max-w-xs text-xs text-muted-foreground">{t('mention.fileHint')}</p>
            <Button size="sm" variant="outline" onClick={onAttach} className="gap-1.5">
              <FileUp className="h-3.5 w-3.5" />
              {t('mention.chooseFile')}
            </Button>
          </div>
        )}
      </div>
    </div>
  )
}

function IoaList({
  nodes,
  messages,
  onSelect,
}: {
  nodes: IOANode[]
  messages: IOAMessage[]
  onSelect: (target: string) => void
}) {
  const { t } = useTranslation('chat')
  if (!nodes.length && !messages.length) {
    return <div className="px-3 py-6 text-center text-xs text-muted-foreground">{t('mention.empty')}</div>
  }
  return (
    <div className="py-1">
      {nodes.length > 0 && (
        <IoaSection title={t('mention.nodes')}>
          {nodes.map((n) => (
            <IoaRow
              key={n.id}
              icon={<Monitor className="h-3.5 w-3.5 shrink-0 text-ai" aria-hidden="true" />}
              label={ioaNodeLabel(n)}
              hint={n.description || n.id}
              onClick={() => onSelect(ioaNodeLabel(n))}
            />
          ))}
        </IoaSection>
      )}
      {messages.length > 0 && (
        <IoaSection title={t('mention.messages')}>
          {messages.map((m) => (
            <IoaRow
              key={m.id}
              icon={<MessageSquare className="h-3.5 w-3.5 shrink-0 text-primary" aria-hidden="true" />}
              label={ioaMessageLabel(m)}
              hint={m.sender}
              onClick={() => onSelect(ioaMessageLabel(m))}
            />
          ))}
        </IoaSection>
      )}
    </div>
  )
}

function IoaSection({ title, children }: { title: string; children: React.ReactNode }) {
  return (
    <div className="mb-1">
      <div className="px-3 py-1 text-[10px] font-semibold uppercase tracking-wide text-muted-foreground/70">{title}</div>
      {children}
    </div>
  )
}

function IoaRow({
  icon,
  label,
  hint,
  onClick,
}: {
  icon: React.ReactNode
  label: string
  hint?: string
  onClick: () => void
}) {
  return (
    <button
      type="button"
      onClick={onClick}
      className="flex w-full items-center gap-2.5 px-3 py-1.5 text-left text-xs transition-colors hover:bg-accent"
    >
      {icon}
      <span className="min-w-0 flex-1 truncate font-medium text-foreground">{label}</span>
      {hint && <span className="max-w-[45%] shrink-0 truncate font-mono text-[10px] text-muted-foreground/70">{hint}</span>}
    </button>
  )
}
