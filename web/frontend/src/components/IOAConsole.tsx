import { useCallback, useEffect, useMemo, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { Clock3, GitBranch, Link2, MessageSquare, Network, RefreshCw, UserRound } from 'lucide-react'
import {
  GraphPanel,
  HandoffCard,
  MessageContent,
  MessageFrontMatter,
  detectContentType,
  messageTitle,
  type ForumThread,
  type HandoffContent,
  type IoaMessageRecord,
} from '@cyber/ioa'
import {
  Badge,
  Button,
  EmptyState,
  Spinner,
} from '@cyber/ui'
import { cn } from '@cyber/theme'
import { getIOAOverview, type IOAMessage, type IOAOverview, type IOASpace } from '../api'
import { usePolling } from '../hooks/usePolling'
import type { IOAConsoleTarget } from '../lib/ioa-navigation'
import { ConsoleDrawer } from './layout/ConsoleDrawer'

interface IOAConsoleProps {
  open: boolean
  onClose: () => void
  initialSpaceID?: IOAConsoleTarget['spaceID']
  initialMessageID?: IOAConsoleTarget['messageID']
}

const EMPTY_OVERVIEW: IOAOverview = { nodes: [], spaces: [], messages: [] }

export default function IOAConsole({ open, onClose, initialSpaceID, initialMessageID }: IOAConsoleProps) {
  const { t } = useTranslation('ioa')
  const [overview, setOverview] = useState<IOAOverview>(EMPTY_OVERVIEW)
  const [loading, setLoading] = useState(false)
  const [error, setError] = useState('')
  const [activeSpaceID, setActiveSpaceID] = useState(initialSpaceID ?? '')
  const [selectedMessageID, setSelectedMessageID] = useState(initialMessageID ?? '')

  const refresh = useCallback(async () => {
    if (!open) return
    setLoading(true)
    try {
      const live = await getIOAOverview()
      setOverview(live)
      setError('')
    } catch (err) {
      setError(err instanceof Error ? err.message : t('loadFailed'))
    } finally {
      setLoading(false)
    }
  }, [open, t])

  useEffect(() => {
    if (open) void refresh()
  }, [open, refresh])
  usePolling(() => { void refresh() }, 3000, open)

  const spaces = useMemo(
    () => overview.spaces.filter(space => overview.messages.some(message => message.space_id === space.id)),
    [overview.messages, overview.spaces],
  )
  useEffect(() => {
    if (!spaces.length) {
      setActiveSpaceID('')
      return
    }
    if (spaces.some(space => space.id === activeSpaceID)) return
    const messageSpaceID = initialMessageID
      ? overview.messages.find(message => message.id === initialMessageID)?.space_id
      : undefined
    const preferredSpaceID = initialSpaceID || messageSpaceID
    setActiveSpaceID(
      preferredSpaceID && spaces.some(space => space.id === preferredSpaceID)
        ? preferredSpaceID
        : spaces[0].id,
    )
  }, [activeSpaceID, initialMessageID, initialSpaceID, overview.messages, spaces])

  const activeSpace = spaces.find(space => space.id === activeSpaceID) ?? spaces[0]
  const activeMessages = useMemo(
    () => activeSpace ? overview.messages.filter(message => message.space_id === activeSpace.id) : [],
    [activeSpace, overview.messages],
  )

  useEffect(() => {
    if (!activeMessages.length) {
      setSelectedMessageID('')
      return
    }
    if (!activeMessages.some(message => message.id === selectedMessageID)) {
      setSelectedMessageID(activeMessages[activeMessages.length - 1].id)
    }
  }, [activeMessages, selectedMessageID])

  const thread = useMemo(
    () => activeSpace ? buildSpaceThread(activeSpace, activeMessages) : null,
    [activeMessages, activeSpace],
  )
  const selectedMessage = activeMessages.find(message => message.id === selectedMessageID) ?? activeMessages[0]
  const sender = selectedMessage ? overview.nodes.find(node => node.id === selectedMessage.sender) : undefined

  return (
    <ConsoleDrawer
      open={open}
      onClose={onClose}
      icon={Network}
      title={t('title')}
      description={(
        <span>
          {t('description')}
          {' · '}
          <a
            href="https://github.com/chainreactors/ioa"
            target="_blank"
            rel="noreferrer"
            className="font-medium text-primary hover:underline"
          >
            chainreactors/ioa
          </a>
        </span>
      )}
      titleMeta={(
        <>
          <span className="flex items-center gap-1 text-[10px] text-muted-foreground">
            <span className={cn('h-1.5 w-1.5 rounded-full', error ? 'bg-destructive' : 'bg-success')} />
            {error ? t('degraded') : t('live')}
          </span>
        </>
      )}
      actions={(
        <>
          <div className="hidden items-center gap-3 text-[11px] text-muted-foreground sm:flex">
            <span className="inline-flex items-center gap-1"><GitBranch className="h-3 w-3" />{spaces.length}</span>
            <span className="inline-flex items-center gap-1"><MessageSquare className="h-3 w-3" />{overview.messages.length}</span>
          </div>
          <Button
            variant="ghost"
            size="icon-xs"
            onClick={() => void refresh()}
            title={t('refresh')}
            className="text-muted-foreground"
          >
            <RefreshCw className={cn('h-3.5 w-3.5', loading && 'animate-spin')} />
          </Button>
        </>
      )}
      bodyClassName="flex flex-col"
    >
        <div className="shrink-0 border-b border-border/70 bg-muted/10 px-3 py-2">
          <div className="flex min-w-0 items-center gap-2 overflow-x-auto" role="tablist" aria-label={t('spaces')}>
            {spaces.map(space => {
              const count = overview.messages.filter(message => message.space_id === space.id).length
              const active = space.id === activeSpace?.id
              return (
                <button
                  key={space.id}
                  type="button"
                  role="tab"
                  aria-selected={active}
                  onClick={() => setActiveSpaceID(space.id)}
                  className={cn(
                    'flex h-9 shrink-0 items-center gap-2 rounded-lg border px-3 text-xs transition-colors',
                    active
                      ? 'border-primary/40 bg-primary/10 font-semibold text-primary shadow-sm'
                      : 'border-border/70 bg-card text-muted-foreground hover:border-primary/25 hover:text-foreground',
                  )}
                >
                  <GitBranch className="h-3.5 w-3.5" />
                  <span>{space.name || space.id}</span>
                  <span className="rounded-md bg-muted px-1.5 py-0.5 font-mono text-[10px] text-muted-foreground">{count}</span>
                </button>
              )
            })}
          </div>
        </div>

        <main className="grid min-h-0 flex-1 gap-3 overflow-y-auto p-3 xl:grid-cols-[minmax(0,1fr)_minmax(260px,300px)] xl:overflow-hidden">
          <div className="min-h-[clamp(420px,60vh,720px)] min-w-0 overflow-hidden xl:min-h-0">
            {loading && !thread ? (
              <div className="flex h-full items-center justify-center gap-2 text-sm text-muted-foreground">
                <Spinner className="h-4 w-4" />
                {t('loading')}
              </div>
            ) : thread ? (
              <GraphPanel
                thread={thread}
                selectedMessageId={selectedMessageID}
                onSelectMessage={setSelectedMessageID}
                mode="dialog"
                title="IOA"
              />
            ) : (
              <div className="flex h-full items-center justify-center rounded-lg border border-border bg-card">
                <EmptyState icon={MessageSquare} title={t('noMessages')} description={t('noMessagesHint')} />
              </div>
            )}
          </div>

          <MessageInspector
            message={selectedMessage}
            senderName={sender?.name || sender?.id}
            onSelectMessage={setSelectedMessageID}
          />
        </main>
    </ConsoleDrawer>
  )
}

function buildSpaceThread(space: IOASpace, messages: IOAMessage[]): ForumThread {
  const root = messages[0] as IoaMessageRecord
  return {
    id: `space:${space.id}`,
    spaceId: space.id,
    spaceName: space.name || space.id,
    root,
    messages: messages as IoaMessageRecord[],
    title: space.name || space.id,
    contentType: 'space',
    targets: [],
    createdAt: root?.created_at,
    latestAt: messages[messages.length - 1]?.created_at,
    messageCount: messages.length,
    pendingCount: 0,
  }
}

function MessageInspector({
  message,
  senderName,
  onSelectMessage,
}: {
  message?: IOAMessage
  senderName?: string
  onSelectMessage: (messageID: string) => void
}) {
  const { t } = useTranslation('ioa')
  if (!message) {
    return (
      <aside className="hidden min-h-0 items-center justify-center rounded-lg border border-border bg-card text-sm text-muted-foreground xl:flex">
        {t('selectMessage')}
      </aside>
    )
  }

  const title = messageTitle(message.content) || message.content_type || message.id
  const kind = detectContentType(message.content, message.content_type)
  const refs = message.refs?.messages ?? []
  const handoff = kind === 'handoff' ? message.content as HandoffContent : null

  return (
    <aside className="min-h-0 overflow-y-auto rounded-lg border border-border bg-card">
      <div className="border-b border-border bg-muted/20 px-4 py-3">
        <div className="flex items-center justify-between gap-2">
          <Badge variant="outline" className="rounded-md">{kind}</Badge>
          <span className="font-mono text-[10px] text-muted-foreground">{message.id}</span>
        </div>
        <h3 className="mt-2 text-sm font-semibold leading-snug text-foreground">{title}</h3>
        {refs.length > 0 && (
          <div className="mt-2 border-t border-border/70 pt-2">
            <div className="mb-1.5 text-[9px] font-semibold uppercase tracking-wide text-muted-foreground">refs.messages</div>
            <div className="flex flex-wrap gap-1.5">
              {refs.map(ref => (
                <button
                  key={ref}
                  type="button"
                  onClick={() => onSelectMessage(ref)}
                  aria-label={t('openReference', { id: ref })}
                  title={ref}
                  className="inline-flex max-w-full items-center gap-1 rounded-md border border-primary/35 bg-background/80 px-1.5 py-1 font-mono text-[10px] font-medium text-primary transition-colors hover:border-primary/60 hover:bg-primary/10 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-primary/40"
                >
                  <Link2 className="h-3 w-3 shrink-0" />
                  <span className="truncate">{ref}</span>
                </button>
              ))}
            </div>
          </div>
        )}
        <MessageFrontMatter
          content={handoff ? { title: handoff.title } : message.content}
          meta={message.meta}
          className="mt-2"
        />
      </div>
      <div className="space-y-4 p-4">
        <div className="grid gap-2 text-xs">
          <div className="flex items-center gap-2 text-muted-foreground">
            <UserRound className="h-3.5 w-3.5" />
            <span>{t('sender')}</span>
            <span className="ml-auto font-medium text-foreground">{senderName || message.sender}</span>
          </div>
          <div className="flex items-center gap-2 text-muted-foreground">
            <Clock3 className="h-3.5 w-3.5" />
            <span>{t('createdAt')}</span>
            <span className="ml-auto font-mono text-[10px] text-foreground">{formatMessageTime(message.created_at)}</span>
          </div>
          <div className="flex items-center gap-2 text-muted-foreground">
            <GitBranch className="h-3.5 w-3.5" />
            <span>{t('references')}</span>
            <span className="ml-auto font-mono text-foreground">{refs.length}</span>
          </div>
        </div>

        <div className="border-t border-border pt-4">
          {handoff ? (
            <HandoffCard message={handoff.message} />
          ) : (
            <MessageContent
              content={message.content}
              meta={message.meta}
              showFrontMatter={false}
              showType={false}
            />
          )}
        </div>
      </div>
    </aside>
  )
}

function formatMessageTime(value: string) {
  const timestamp = Date.parse(value)
  if (!Number.isFinite(timestamp)) return value || '—'
  return new Intl.DateTimeFormat(undefined, {
    month: 'short', day: '2-digit', hour: '2-digit', minute: '2-digit', second: '2-digit',
  }).format(timestamp)
}
