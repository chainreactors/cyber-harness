import { memo, useCallback, useEffect, useMemo, useRef, useState, type ReactNode } from 'react'
import { useTranslation } from 'react-i18next'
import i18n from '../i18n'
import {
  AlertTriangle,
  CheckCircle2,
  FileText,
  GitBranch,
  Layers,
  Loader2,
  MessageSquare,
  Network,
  Radar,
  RefreshCw,
  Sparkles,
  Target,
  User,
  Wrench,
  X,
} from 'lucide-react'
import { cn } from '@cyber/theme'
import { Button, Callout, DisclosureCard, Tooltip, TooltipContent, TooltipTrigger } from '@cyber/ui'
import BrandMark from './brand/BrandMark'
import { MarkdownContent } from '@/markdown'
import {
  AssistantResponse,
  ChatInput,
  ChatThinking,
  MessageBubble as ChatMessageBubble,
  ToolCallDisplay as ChatToolCall,
  resolveTimelineRenderer,
  summarizeArgs,
  type ChatAttachment,
  type CommandHint,
  type ExtensionTimelineItem,
  type Mentionable,
} from '@/viewer'
import { fetchSessionCommands, uploadChatFile } from '../api'
import type { ChatMessage, ScanResult, SlashCommandSpec } from '../api'
import type { AssistantResponseState, TimelineItem } from '../hooks/useChatSession'
import InstrumentIdle from './InstrumentIdle'

// Inlined from the former timeline-mapper.ts — converts the hook's local
// TimelineItem variants into the viewer's ExtensionTimelineItem so the
// registered timeline renderers (scan cards, agent-joined chip) can render them.
function toExtensionItem(item: TimelineItem): ExtensionTimelineItem | null {
  switch (item.kind) {
    case 'scan_started':
    case 'scan_progress':
      return {
        id: item.id,
        kind: 'extension',
        timestamp: item.timestamp,
        extensionType: 'scan_started',
        data: { scanID: item.scanID || '', lines: item.scanLines || [] },
      }
    case 'scan_complete':
      return {
        id: item.id,
        kind: 'extension',
        timestamp: item.timestamp,
        extensionType: 'scan_complete',
        data: { scanID: item.scanID || '', result: item.scanResult },
      }
    case 'agent_joined':
      return {
        id: item.id,
        kind: 'extension',
        timestamp: item.timestamp,
        extensionType: 'agent_joined',
        data: { agentName: item.agentName || '' },
      }
    default:
      return null
  }
}

const workspaceClass = 'mx-auto w-full max-w-[96rem] px-4 sm:px-5 lg:px-6'
const contentOffsetClass = 'xl:ml-[6.75rem]'
const threadOffsetClass = 'xl:mr-[6.75rem]'

interface Props {
  timeline: TimelineItem[]
  scanResults: Map<string, ScanResult>
  isThinking: boolean
  isBusy: boolean
  error: string
  hasActiveSession: boolean
  activeSessionID: string | null
  mentionables?: Mentionable[]
  injectText?: { text: string; nonce: number }
  agentOffline?: boolean
  agentName?: string
  onSend: (content: string, opts?: { persist?: boolean; evalCriteria?: string; evalMaxRounds?: number }) => void
  onPause: () => void
  onClearError: () => void
}

export default function ChatPanel({
  timeline,
  scanResults,
  isThinking,
  isBusy,
  error,
  activeSessionID,
  hasActiveSession,
  mentionables,
  injectText,
  agentOffline,
  agentName,
  onSend,
  onPause,
  onClearError,
}: Props) {
  const { t, i18n } = useTranslation('chat')
  const scrollRef = useRef<HTMLDivElement>(null)
  const bottomRef = useRef<HTMLDivElement>(null)
  const footerRef = useRef<HTMLDivElement>(null)
  const stickRef = useRef(true)
  const jumpRef = useRef(false)
  const hasAssistantResponse = timeline.some((item) => item.kind === 'assistant_response')
  // The right rail carries IOA thread notes, which only a fraction of turns emit.
  // Reserve its 6rem column only when the transcript actually has one — otherwise
  // the empty rail just steals horizontal space from the conversation.
  const hasThreadNotes = useMemo(
    () => timeline.some((item) => describeIOAThreadItem(item, t) !== null),
    [timeline, t],
  )
  const inputFormClass = cn(contentOffsetClass, hasThreadNotes && threadOffsetClass)
  const [persist, setPersist] = useState(false)
  // Goal mode: describe done-when criteria in natural language and let an
  // independent evaluator judge completion each round, re-driving the agent
  // until it passes or the round budget is spent. (evalMaxRounds is the whole
  // budget — it subsumes the old standalone "fixed turns" mode.)
  const [evalCriteria, setEvalCriteria] = useState('')
  const [evalMaxRounds, setEvalMaxRounds] = useState(3)
  const evalRef = useRef<HTMLTextAreaElement>(null)
  // Screen-reader turn status. Streamed replies mutate the DOM silently, so
  // mirror the coarse turn phase into a polite live region below. It announces
  // transitions (thinking → responding → done), never the token stream itself
  // (a live region on the growing text would restart the reader on every delta).
  const [liveStatus, setLiveStatus] = useState('')
  const wasActiveRef = useRef(false)

  // Composer seed — the mobile greeting's capability cards push a starter prompt
  // into the composer through ChatInput's injectText (nonce-guarded append). Own
  // the nonce here so each card tap reliably re-injects; still fold in an external
  // injectText if one ever arrives (the asset-pool source is gone, so it's inert).
  const [composerSeed, setComposerSeed] = useState<{ text: string; nonce: number }>(
    () => injectText ?? { text: '', nonce: 0 },
  )
  useEffect(() => {
    if (injectText && injectText.nonce > 0) setComposerSeed(injectText)
  }, [injectText])
  const seedComposer = useCallback((text: string) => {
    setComposerSeed((s) => ({ text, nonce: s.nonce + 1 }))
  }, [])

  function sendOpts() {
    if (!persist) return undefined
    const criteria = evalCriteria.trim()
    if (criteria) return { persist: true, evalCriteria: criteria, evalMaxRounds }
    // Goal toggled on but no criteria typed → nothing for the evaluator to
    // judge, so send as a plain one-off message rather than an open-ended run.
    return undefined
  }

  // A Goal is a one-shot kickoff: once dispatched, clear the panel so the next
  // message isn't silently re-sent as a fresh multi-round run against stale
  // criteria, and so the composer visibly returns to plain-chat state.
  function resetGoal() {
    setPersist(false)
    setEvalCriteria('')
    setEvalMaxRounds(3)
  }

  // The "/" command menu is served by the hub (GET .../commands): hub-scope
  // commands merged with the bound agent's reported commands (skills included),
  // so it always mirrors the real command set instead of a hardcoded list.
  // Descriptions prefer the local i18n string (keyed cmd<Name>) and fall back to
  // the server's (used for dynamic skill commands that have no i18n key).
  const [chatCommands, setChatCommands] = useState<CommandHint[]>([])
  useEffect(() => {
    if (!activeSessionID) {
      setChatCommands([])
      return
    }
    let cancelled = false
    const toHint = (spec: SlashCommandSpec): CommandHint => {
      const base = spec.name.replace(/^\//, '')
      const key = base ? `cmd${base.charAt(0).toUpperCase()}${base.slice(1)}` : ''
      const localized = key ? t(key, { defaultValue: '' }) : ''
      return { cmd: spec.name, desc: localized || spec.description || '', usage: spec.usage }
    }
    fetchSessionCommands(activeSessionID)
      .then((specs) => {
        if (!cancelled) setChatCommands(specs.map(toHint))
      })
      .catch(() => {
        if (!cancelled) setChatCommands([])
      })
    return () => {
      cancelled = true
    }
  }, [activeSessionID, i18n.language, t])

  // Re-entering a session (switching sessions, or returning to the chat tab)
  // re-pins to the bottom and requests an instant jump, so we land on the
  // latest message instead of sitting at the top.
  useEffect(() => {
    stickRef.current = true
    jumpRef.current = true
    // Goal (persist/eval) mode is per-session intent. ChatPanel doesn't remount
    // on session switch, so clear it here — otherwise session A's done-when
    // criteria stays toggled on and gets silently sent with the next message in
    // session B (an unexpected multi-round agentic run against stale criteria).
    setPersist(false)
    setEvalCriteria('')
    setEvalMaxRounds(3)
  }, [activeSessionID])

  useEffect(() => {
    if (!stickRef.current) return
    if (timeline.length === 0) return
    const behavior: ScrollBehavior = jumpRef.current ? 'auto' : 'smooth'
    jumpRef.current = false
    bottomRef.current?.scrollIntoView({ behavior })
    // Depend on `timeline` (not `timeline.length`): streamed deltas update the
    // last item in place, producing a new array reference but the same length,
    // so keying on length would freeze autoscroll mid-reply.
  }, [timeline, isThinking])

  // The composer footer shares the flex column with the message viewport, so
  // whenever it grows — Goal panel expands, the eval/message textareas auto-grow,
  // attachment chips or the offline banner appear — the viewport shrinks by the
  // same amount while scrollTop stays put, pushing the newest messages (often the
  // latest tool calls) below the fold so the composer reads as if it "covered"
  // them. Re-pin to the bottom on any footer resize while we're still stuck
  // there, keeping the tail visible without disturbing a user who scrolled up.
  useEffect(() => {
    const el = footerRef.current
    if (!el) return
    const ro = new ResizeObserver(() => {
      if (stickRef.current) bottomRef.current?.scrollIntoView({ behavior: 'auto' })
    })
    ro.observe(el)
    return () => ro.disconnect()
  }, [hasActiveSession])

  // Auto-grow the goal criteria textarea (min ~2 rows, capped) so long
  // natural-language goals stay readable instead of scrolling a one-liner.
  useEffect(() => {
    const el = evalRef.current
    if (!el) return
    el.style.height = 'auto'
    el.style.height = Math.min(el.scrollHeight, 144) + 'px'
  }, [evalCriteria, persist])

  // Derive the polite screen-reader status from the turn phase. `isBusy` keeps
  // the "working" state across tool-execution gaps (thinking false, no stream)
  // so the "done" cue fires once when the whole turn actually settles, not on
  // every pause between tool calls.
  useEffect(() => {
    const active = isBusy || isThinking
    if (active) setLiveStatus(t('a11yThinking'))
    else if (wasActiveRef.current) setLiveStatus(t('a11yTurnDone'))
    wasActiveRef.current = active
  }, [isBusy, isThinking, t])

  async function handleSendWithAttachments(content: string, attachments?: ChatAttachment[]) {
    const opts = sendOpts()
    const hadGoal = persist
    if (!attachments?.length) {
      onSend(content, opts)
      if (hadGoal) resetGoal()
      return
    }
    const contextParts: string[] = []
    for (const a of attachments) {
      if (a.mode === 'context') {
        const text = await a.file.text()
        contextParts.push(`<file name="${a.file.name}">\n${text}\n</file>`)
      } else if (a.mode === 'upload' && activeSessionID) {
        try {
          await uploadChatFile(activeSessionID, a.file)
        } catch { /* upload error shown via SSE system message */ }
      }
    }
    const fullContent = contextParts.length > 0
      ? `${FILE_CONTEXT_PREAMBLE}\n${contextParts.join('\n')}\n\n${content}`
      : content
    if (fullContent.trim()) onSend(fullContent, opts)
    if (hadGoal) resetGoal()
  }

  function handleScroll() {
    const el = scrollRef.current
    if (!el) return
    const atBottom = el.scrollHeight - el.scrollTop - el.clientHeight < 80
    stickRef.current = atBottom
  }

  return (
    <div className="flex min-w-0 flex-1 flex-col">
      {error && (
        <div
          role="alert"
          className="flex items-start gap-2 border-b border-destructive/30 bg-destructive/10 px-4 py-2 text-sm text-destructive animate-in fade-in slide-in-from-top-1 duration-200"
        >
          <AlertTriangle className="mt-0.5 h-4 w-4 shrink-0" />
          <span className="min-w-0 flex-1 break-words">{error}</span>
          <button type="button" aria-label={t('dismiss')} onClick={onClearError} className="rounded p-0.5 hover:bg-destructive/10">
            <X className="h-4 w-4" />
          </button>
        </div>
      )}

      <main className="flex min-h-0 flex-1 flex-col bg-transparent">
        {/* Off-screen polite live region — announces the turn phase to screen
            readers without reading the streamed tokens one by one. */}
        <div className="sr-only" role="status" aria-live="polite">{liveStatus}</div>
        <div
          ref={scrollRef}
          onScroll={handleScroll}
          className="min-h-0 flex-1 overflow-y-auto overscroll-contain"
        >
          <div className={cn(workspaceClass, 'space-y-3 py-4')}>
            {!hasActiveSession && timeline.length === 0 && (
              <div className={inputFormClass}>
                <EmptyState
                  eyebrow={t('consoleReady')}
                  title={t('startConversation')}
                  subtitle={t('createSession')}
                />
              </div>
            )}
            {hasActiveSession && timeline.length === 0 && !isThinking && (
              <div className={inputFormClass}>
                {/* Desktop keeps the idle "instrument" empty state; phones get the
                    Doubao-style greeting + capability cards (mobile-only, so the
                    deck's identity is untouched at md+). */}
                <div className="hidden md:block">
                  <EmptyState
                    eyebrow={t('readyEyebrow')}
                    title={t('ready')}
                    subtitle={
                      <>{t('readyHintBefore')}<code className="rounded bg-muted px-1 py-0.5 text-[10px] font-mono">/scan &lt;target&gt;</code>{t('readyHintAfter')}</>
                    }
                  />
                </div>
                <div className="md:hidden">
                  <MobileChatGreeting onSeed={seedComposer} />
                </div>
              </div>
            )}

            {timeline.map((item) => (
              <TimelineEntry
                key={item.id}
                item={item}
                scanResults={scanResults}
                hasThreadNotes={hasThreadNotes}
              />
            ))}

            {isThinking && !hasAssistantResponse && (
              <TimelineRow
                item={{
                  id: 'thinking-live',
                  kind: 'thinking',
                  timestamp: Date.now(),
                }}
                hasThreadNotes={hasThreadNotes}
              >
                <ChatThinking />
              </TimelineRow>
            )}

            <div ref={bottomRef} />
          </div>
        </div>

        {hasActiveSession && (
          <div ref={footerRef} className="bg-background/95 pb-safe backdrop-blur-sm">
            {agentOffline && (
              <div className={cn(workspaceClass, 'pt-2')}>
                <div className={inputFormClass}>
                  <Callout
                    tone="warning"
                    icon={<AlertTriangle className="h-3.5 w-3.5" />}
                    className="rounded-lg animate-in fade-in slide-in-from-bottom-1 duration-200"
                  >
                    {agentName ? t('agentOfflineBannerNamed', { name: agentName }) : t('agentOfflineBanner')}
                  </Callout>
                </div>
              </div>
            )}
            <div className={workspaceClass}>
              <div className={inputFormClass}>
                <ChatInput
                  className="!border-t-0 !bg-transparent !backdrop-blur-none"
                  topSlot={persist ? (
                    <div className="bg-primary/[0.04] px-3.5 py-3">
                      <div className="mb-2 flex items-center justify-between gap-2">
                        <span className="inline-flex items-center gap-1.5 text-xs font-medium text-primary">
                          <Target className="h-3.5 w-3.5" />
                          {t('persistMode')}
                        </span>
                        <label className="inline-flex shrink-0 items-center gap-1.5 text-[11px] text-muted-foreground">
                          {t('evalRoundsLabel')}
                          <input
                            type="number"
                            min={1}
                            max={10}
                            value={evalMaxRounds}
                            onChange={(e) => setEvalMaxRounds(Math.min(10, Math.max(1, parseInt(e.target.value, 10) || 1)))}
                            className="w-12 rounded-md border border-border/70 bg-card/60 px-2 py-0.5 text-xs text-foreground focus:border-ai/50 focus:outline-none focus:ring-1 focus:ring-ai/20"
                          />
                        </label>
                      </div>
                      <textarea
                        ref={evalRef}
                        rows={2}
                        value={evalCriteria}
                        onChange={(e) => setEvalCriteria(e.target.value)}
                        placeholder={t('evalCriteriaPlaceholder')}
                        className="block max-h-36 min-h-[3.25rem] w-full resize-none overflow-y-auto rounded-lg border border-border/60 bg-card/60 px-3 py-2 text-xs leading-relaxed text-foreground placeholder:text-muted-foreground/60 focus:border-ai/50 focus:outline-none focus:ring-1 focus:ring-ai/20"
                      />
                      <p className="mt-1.5 text-[11px] leading-relaxed text-muted-foreground/70">
                        {t('evalModeHint', { rounds: evalMaxRounds })}
                      </p>
                    </div>
                  ) : undefined}
                  leading={
                    <Tooltip>
                      <TooltipTrigger asChild>
                        {/* Ghost chip — lives *inside* the composer capsule, so no
                            border of its own (that would be a pill-in-pill). */}
                        <Button
                          variant="ghost"
                          active={persist}
                          onClick={() => setPersist((v) => !v)}
                          className={cn('h-9 shrink-0 gap-1.5 rounded-full px-3 text-xs md:h-10 md:px-3.5', !persist && 'text-muted-foreground')}
                        >
                          <Target className="h-3.5 w-3.5" />
                          {t('persistMode')}
                        </Button>
                      </TooltipTrigger>
                      <TooltipContent>{t('persistHint')}</TooltipContent>
                    </Tooltip>
                  }
                  onSend={handleSendWithAttachments}
                  onPause={onPause}
                  busy={isBusy}
                  commands={chatCommands}
                  mentionables={mentionables}
                  injectText={composerSeed}
                  placeholder={t('typeMessageWithCommands')}
                  enableAttachments={!!activeSessionID}
                />
              </div>
            </div>
          </div>
        )}
      </main>
    </div>
  )
}

// Memoized: during token streaming useChatSession mints a new `timeline` array
// on every message_delta, but only the streaming item's reference actually
// changes. Without memo, timeline.map re-renders EVERY settled entry each token
// — and each MessageBubble re-parses its markdown (remark) from scratch, so a
// 40-message transcript re-parses 40 docs per token. A shallow prop compare lets
// unchanged entries bail out because settled item references and the scan-results
// map stay stable while an unrelated response streams.
const TimelineEntry = memo(function TimelineEntry({
  item,
  scanResults,
  hasThreadNotes,
}: {
  item: TimelineItem
  scanResults: Map<string, ScanResult>
  hasThreadNotes: boolean
}) {
  const content = timelineContent(item, scanResults)
  if (!content) return null

  return (
    <TimelineRow item={item} hasThreadNotes={hasThreadNotes}>
      {content}
    </TimelineRow>
  )
})

function TimelineRow({
  item,
  hasThreadNotes,
  children,
}: {
  item: TimelineItem
  hasThreadNotes: boolean
  children: ReactNode
}) {
  return (
    <div
      data-testid="chat-timeline-row"
      data-kind={item.kind}
      className={cn(
        'grid grid-cols-1 gap-y-1 animate-in fade-in slide-in-from-bottom-1 duration-200',
        'xl:gap-x-3',
        hasThreadNotes ? 'xl:grid-cols-[6rem_minmax(0,1fr)_6rem]' : 'xl:grid-cols-[6rem_minmax(0,1fr)]',
      )}
    >
      <TimelineMark item={item} />
      <div data-testid="chat-content" className="min-w-0">
        {children}
      </div>
      {hasThreadNotes && <IOAThreadNote item={item} />}
    </div>
  )
}

function timelineContent(
  item: TimelineItem,
  scanResults: Map<string, ScanResult>,
): ReactNode {
  switch (item.kind) {
    case 'message':
      if (!item.message) return null
      {
        const msg = item.message
        const role = msg.role === 'tool_call' || msg.role === 'tool_result' ? 'system' : msg.role
        return (
          <ChatMessageBubble
            role={role}
            actorName={msg.agent_name}
            timestamp={msg.created_at}
          >
            {role === 'system' && systemCode(msg.metadata) ? (
              <SystemMessageContent metadata={msg.metadata!} fallback={msg.content} />
            ) : msg.content ? (
              <MessageBody content={msg.content} compact={role !== 'system'} />
            ) : null}
          </ChatMessageBubble>
        )
      }

    case 'assistant_response':
      if (!item.assistantResponse) return null
      return <AssistantResponseEntry response={item.assistantResponse} />

    case 'tool_call':
      if (!item.toolCall) return null
      return (
        <ChatToolCall
          toolName={item.toolCall.toolName}
          toolArgs={item.toolCall.toolArgs}
          result={item.toolCall.result}
          pending={item.toolCall.pending}
          toolCallId={item.toolCall.id}
        />
      )

    case 'scan_started':
    case 'scan_progress':
    case 'scan_complete': {
      if (item.kind !== 'scan_complete' && item.scanID && scanResults.has(item.scanID)) return null
      const ext = toExtensionItem(item) as ExtensionTimelineItem | null
      if (!ext) return null
      const config = resolveTimelineRenderer(ext.extensionType)
      if (!config) return null
      const Renderer = config.renderer
      return <Renderer item={ext} context={{ scanResults }} />
    }

    case 'thinking':
      return (
        <ChatThinking actorName={item.agentName}>
          {item.content?.trim() ? <MarkdownContent content={trimDisplayContent(item.content)} compact muted /> : null}
        </ChatThinking>
      )

    case 'agent_joined': {
      const ext = toExtensionItem(item) as ExtensionTimelineItem | null
      if (!ext) return null
      const config = resolveTimelineRenderer(ext.extensionType)
      if (!config) return null
      const AgentRenderer = config.renderer
      return <AgentRenderer item={ext} context={{}} />
    }

    case 'eval':
      return <EvalNote pass={!!item.evalPass} round={item.evalRound} reason={item.evalReason} />

    default:
      return null
  }
}

// Backend system messages carry a stable i18n code (+ params) in metadata; the
// English text in `content` is only a fallback. Localize by code so the message
// reads in the user's language on both the live path and after a reload.
function systemCode(metadata?: Record<string, unknown>): string {
  return typeof metadata?.code === 'string' ? metadata.code : ''
}

function systemParams(metadata: Record<string, unknown>): Record<string, unknown> {
  const p = metadata.params
  return p && typeof p === 'object' ? (p as Record<string, unknown>) : {}
}

function SystemMessageContent({ metadata, fallback }: { metadata: Record<string, unknown>; fallback: string }) {
  const { t } = useTranslation('chat')
  const code = systemCode(metadata)
  const params = systemParams(metadata)
  if (code === 'agents_list') return <AgentsListContent params={params} fallback={fallback} />
  const text = t(`sys.${code}`, { ...params, defaultValue: fallback })
  return <MarkdownContent content={trimDisplayContent(text)} />
}

type AgentListEntry = { name?: string; id?: string; busy?: boolean; provider?: string; model?: string }

function AgentsListContent({ params, fallback }: { params: Record<string, unknown>; fallback: string }) {
  const { t } = useTranslation('chat')
  const agents = Array.isArray(params.agents) ? (params.agents as AgentListEntry[]) : null
  if (!agents) return <MarkdownContent content={trimDisplayContent(fallback)} />
  const count = typeof params.count === 'number' ? params.count : agents.length
  const lines = [t('sys.agents_connected', { n: count })]
  for (const a of agents) {
    const status = a.busy ? t('sys.agent_status_busy') : t('sys.agent_status_idle')
    let line = `- **${a.name}** (${a.id}) — ${status}`
    if (a.model) line += ` — ${a.provider}/${a.model}`
    lines.push(line)
  }
  return <MarkdownContent content={lines.join('\n')} />
}

function EvalNote({ pass, round, reason }: { pass: boolean; round?: number; reason?: string }) {
  const { t } = useTranslation('chat')
  return (
    <Callout
      tone={pass ? 'success' : 'ai'}
      icon={pass ? <CheckCircle2 className="h-3.5 w-3.5" /> : <Sparkles className="h-3.5 w-3.5" />}
      className="rounded-lg"
    >
      <span className="font-medium">
        {t('evalRound', { round: (round ?? 0) + 1 })} · {pass ? t('evalPass') : t('evalFail')}
      </span>
      {reason ? <p className="mt-0.5 break-words text-muted-foreground">{reason}</p> : null}
    </Callout>
  )
}

function AssistantResponseEntry({ response }: { response: AssistantResponseState }) {
  const { t } = useTranslation('chat')
  const message = response.response
  const hasThinking = !!response.thinking?.trim()
  const hasResponse = !!message?.content?.trim()

  return (
    <AssistantResponse
      actorName={response.agentName || message?.agent_name}
      timestamp={message?.created_at}
      streaming={response.streaming}
      thinking={hasThinking ? <MarkdownContent content={trimDisplayContent(response.thinking || '')} compact muted /> : undefined}
      tools={response.tools.length > 0 ? (
        <div className="space-y-2">
          {response.tools.map((tool) => (
            <ChatToolCall
              key={tool.id}
              toolName={tool.toolName}
              toolArgs={tool.toolArgs}
              result={tool.result}
              pending={tool.pending}
              toolCallId={tool.id}
            />
          ))}
        </div>
      ) : undefined}
      response={hasResponse ? <MarkdownContent content={trimDisplayContent(message?.content || '')} compact /> : undefined}
      labels={{ tools: response.tools.length === 1 ? t('tool') : t('tools'), thinking: t('thinkingLabel'), response: t('responseLabel') }}
    />
  )
}

function TimelineMark({ item }: { item: TimelineItem }) {
  const { t } = useTranslation('chat')
  const descriptor = describeTimelineItem(item, t)
  if (!descriptor) return <div className="hidden xl:block" />

  return (
    <div className="hidden pr-2 pt-1 xl:block">
      <div className="relative min-h-8 border-r border-border/70 pr-3 text-right">
        <span
          className={cn(
            'absolute -right-[5px] top-1 flex h-2.5 w-2.5 items-center justify-center rounded-full border bg-background',
            descriptor.dotClass,
          )}
        />
        <div className="flex min-w-0 items-center justify-end gap-1.5 text-[11px] font-medium text-foreground">
          <span className="truncate">{descriptor.label}</span>
          {descriptor.icon}
        </div>
        <div className="mt-0.5 font-mono text-[10px] leading-4 text-muted-foreground">{descriptor.time}</div>
      </div>
    </div>
  )
}

function IOAThreadNote({ item }: { item: TimelineItem }) {
  const { t } = useTranslation('chat')
  const note = describeIOAThreadItem(item, t)
  if (!note) return <div className="hidden 2xl:block" />

  return (
    <div className="hidden pt-1 2xl:block">
      <div className="rounded-md border border-primary/25 bg-primary/5 px-2.5 py-2">
        <div className="flex min-w-0 items-center gap-1.5 text-[11px] font-medium text-primary">
          <GitBranch className="h-3 w-3 shrink-0" />
          <span className="truncate">{note.label}</span>
        </div>
        {note.detail && (
          <p className="mt-1 line-clamp-3 text-[11px] leading-4 text-muted-foreground">{note.detail}</p>
        )}
      </div>
    </div>
  )
}

function EmptyState({ eyebrow, title, subtitle }: { eyebrow: string; title: string; subtitle: ReactNode }) {
  return <InstrumentIdle eyebrow={eyebrow} title={title} subtitle={subtitle} className="py-16" />
}

// Phone-only greeting for a fresh, empty session: an AIScan hello + a 2×2 grid of
// capability cards, each seeding the composer with a starter prompt (Doubao's
// home pattern). Kept in AIScan's own skin — blue accent, warm reserved for
// severity, no mascot. The scan card seeds the real "/scan " command; the others
// seed editable natural-language templates the operator completes.
function MobileChatGreeting({ onSeed }: { onSeed: (text: string) => void }) {
  const { t } = useTranslation('chat')
  const cards: { key: string; Icon: typeof Radar; seed?: string; seedKey?: string; titleKey: string; subKey: string }[] = [
    { key: 'scan', Icon: Radar, seed: '/scan ', titleKey: 'cardScanTitle', subKey: 'cardScanSub' },
    { key: 'verify', Icon: RefreshCw, seedKey: 'cardVerifySeed', titleKey: 'cardVerifyTitle', subKey: 'cardVerifySub' },
    { key: 'assets', Icon: Layers, seedKey: 'cardAssetsSeed', titleKey: 'cardAssetsTitle', subKey: 'cardAssetsSub' },
    { key: 'swarm', Icon: Network, seedKey: 'cardSwarmSeed', titleKey: 'cardSwarmTitle', subKey: 'cardSwarmSub' },
  ]
  return (
    <div className="px-1 pb-2 pt-8">
      <h2 className="text-balance text-[1.35rem] font-bold leading-tight tracking-tight text-foreground">{t('mobileGreetingTitle')}</h2>
      <p className="mb-5 mt-1 text-sm text-muted-foreground">{t('mobileGreetingSubtitle')}</p>
      <div className="grid grid-cols-2 gap-2.5">
        {cards.map(({ key, Icon, seed, seedKey, titleKey, subKey }) => (
          <button
            key={key}
            type="button"
            onClick={() => onSeed(seed ?? t(seedKey!))}
            className="flex flex-col gap-2 rounded-[0.7rem] border border-border/75 bg-card p-3.5 text-left shadow-soft transition-transform active:scale-[0.98]"
          >
            <span className="grid h-8 w-8 place-items-center rounded-lg bg-accent text-primary">
              <Icon className="h-[18px] w-[18px]" />
            </span>
            <span className="text-sm font-semibold text-foreground">{t(titleKey)}</span>
            <span className="text-[11.5px] leading-snug text-muted-foreground">{t(subKey)}</span>
          </button>
        ))}
      </div>
    </div>
  )
}

interface TimelineDescriptor {
  label: string
  time: string
  icon: ReactNode
  dotClass: string
}

function describeTimelineItem(item: TimelineItem, t: (key: string) => string): TimelineDescriptor | null {
  const time = formatRailTime(item)

  switch (item.kind) {
    case 'message': {
      if (!item.message) return null
      const role = item.message.role
      if (role === 'user') {
        return {
          label: t('you'),
          time,
          icon: <User className="h-3 w-3 text-primary" />,
          dotClass: 'border-primary bg-primary',
        }
      }
      if (role === 'assistant') {
        return {
          label: item.message.agent_name || t('assistant'),
          time,
          icon: <BrandMark size={12} className="text-ai" />,
          dotClass: 'border-ai bg-ai',
        }
      }
      return {
        label: t('system'),
        time,
        icon: <MessageSquare className="h-3 w-3 text-muted-foreground" />,
        dotClass: 'border-border bg-muted-foreground/60',
      }
    }

    case 'assistant_response':
      return {
        label: item.assistantResponse?.agentName || item.agentName || t('assistant'),
        time,
        icon: <BrandMark size={12} className="text-ai" />,
        dotClass: item.assistantResponse?.streaming
          ? 'border-primary bg-background animate-pulse'
          : 'border-ai bg-ai',
      }

    case 'tool_call':
      return {
        label: item.toolCall?.toolName || t('tool'),
        time,
        icon: <Wrench className="h-3 w-3 text-warning" />,
        dotClass: item.toolCall?.pending ? 'border-warning bg-warning animate-pulse' : 'border-primary bg-primary',
      }

    case 'scan_started':
    case 'scan_progress':
    case 'scan_complete':
    case 'agent_joined': {
      const ext = toExtensionItem(item) as ExtensionTimelineItem | null
      if (!ext) return null
      const config = resolveTimelineRenderer(ext.extensionType)
      if (config?.mark) {
        const markLabel = typeof config.mark.label === 'function'
          ? config.mark.label(ext) : (config.mark.label || item.kind)
        const MarkIcon = config.mark.icon
        return {
          label: markLabel,
          time,
          icon: MarkIcon ? <MarkIcon className="h-3 w-3" /> : null,
          dotClass: config.mark.dotClass || 'border-border bg-muted-foreground/60',
        }
      }
      return null
    }

    case 'thinking':
      return {
        label: item.agentName || t('thinking'),
        time,
        icon: <Loader2 className="h-3 w-3 animate-spin text-primary" />,
        dotClass: 'border-primary bg-background',
      }

    case 'eval':
      return {
        label: t('evalLabel'),
        time,
        icon: <Sparkles className="h-3 w-3 text-ai" />,
        dotClass: item.evalPass ? 'border-success bg-success' : 'border-ai bg-ai',
      }

    default:
      return null
  }
}

function describeIOAThreadItem(item: TimelineItem, t: (key: string) => string): { label: string; detail?: string } | null {
  if (item.kind === 'assistant_response' && item.assistantResponse) {
    const ioaTool = item.assistantResponse.tools.find((tool) => isIOATool(tool.toolName, tool.toolArgs))
    if (ioaTool) {
      return {
        label: ioaTool.toolName || 'server',
        detail: previewText(summarizeArgs(ioaTool.toolArgs) || ioaTool.result || '', 140),
      }
    }
  }

  if (item.kind === 'tool_call' && item.toolCall && isIOATool(item.toolCall.toolName, item.toolCall.toolArgs)) {
    return {
      label: item.toolCall.toolName || 'server',
      detail: previewText(summarizeArgs(item.toolCall.toolArgs) || item.toolCall.result || '', 140),
    }
  }

  if (item.kind === 'message' && item.message) {
    const metadata = item.message.metadata || {}
    const thread = metadata.ioa_thread || metadata.ioa_message || metadata.thread
    if (thread) {
      return {
        label: t('ioaMessage'),
        detail: previewText(typeof thread === 'string' ? thread : JSON.stringify(thread), 140),
      }
    }
  }

  return null
}

function isIOATool(toolName: string, toolArgs: string): boolean {
  const name = toolName.toLowerCase()
  if (name === 'ioa' || name.startsWith('ioa_') || name.startsWith('ioa.')) return true
  return /\bioa_(space|send|read)\b/i.test(toolArgs)
}

function formatRailTime(item: TimelineItem): string {
  const raw = item.message?.created_at ? new Date(item.message.created_at).getTime() : item.timestamp
  const date = new Date(raw)
  if (Number.isNaN(date.getTime())) return ''
  return date.toLocaleTimeString(i18n.language, { hour: '2-digit', minute: '2-digit' })
}

function previewText(value: string, max: number): string {
  const compact = value.replace(/\s+/g, ' ').trim()
  if (compact.length <= max) return compact
  return `${compact.slice(0, Math.max(0, max - 1))}...`
}

function trimDisplayContent(value: string): string {
  return value.replace(/[ \t\r\n]+$/g, '')
}

// Prepended (agent-facing) to any outgoing message that carries `context`
// attachments. Without it the model reads the <file name="X"> block as a pointer
// to a file on disk, calls read/glob/find hunting for X, comes up empty, and
// reports the file "missing" — even though its full text is right there inline.
// parseMessageSegments strips this line back off before render so it never shows
// in the user's own bubble.
const FILE_CONTEXT_PREAMBLE =
  '[附件内容已内联，无需读取磁盘] 下方 <file> 块内是用户随本条消息附带文件的完整内容，已直接提供给你。请仅基于这些内容作答；该文件并未落盘，也不在工作目录，切勿用 read / glob / find 等工具去查找或打开它。'

// A "context" attachment is inlined into the outgoing message as
// `<file name="X">\n…contents…\n</file>` so the agent sees the file text. That
// wrapper is plumbing, not something to dump raw into the bubble — split it out
// and render each file as a collapsed card, with the surrounding prose as
// markdown. Messages without a <file> block fall straight through to markdown.
type MessageSegment =
  | { kind: 'text'; text: string }
  | { kind: 'file'; name: string; body: string }

const FILE_BLOCK_RE = /<file name="([^"]*)">\n([\s\S]*?)\n<\/file>/g

function parseMessageSegments(content: string): MessageSegment[] {
  // Drop the agent-facing preamble the composer prepends to context attachments;
  // it's guidance for the model, not part of what the user actually typed.
  if (content.startsWith(FILE_CONTEXT_PREAMBLE)) {
    content = content.slice(FILE_CONTEXT_PREAMBLE.length).replace(/^\n+/, '')
  }
  const segments: MessageSegment[] = []
  let last = 0
  FILE_BLOCK_RE.lastIndex = 0
  for (let m = FILE_BLOCK_RE.exec(content); m; m = FILE_BLOCK_RE.exec(content)) {
    const lead = content.slice(last, m.index)
    if (lead.trim()) segments.push({ kind: 'text', text: lead })
    segments.push({ kind: 'file', name: m[1], body: m[2] })
    last = FILE_BLOCK_RE.lastIndex
  }
  const tail = content.slice(last)
  if (tail.trim()) segments.push({ kind: 'text', text: tail })
  return segments
}

function MessageBody({ content, compact }: { content: string; compact: boolean }) {
  const segments = useMemo(() => parseMessageSegments(content), [content])
  const hasFile = segments.some((s) => s.kind === 'file')
  if (!hasFile) {
    return <MarkdownContent content={trimDisplayContent(content)} compact={compact} />
  }
  return (
    <div className="space-y-2">
      {segments.map((seg, i) =>
        seg.kind === 'file' ? (
          <AttachmentCard key={i} name={seg.name} body={seg.body} />
        ) : (
          <MarkdownContent key={i} content={trimDisplayContent(seg.text)} compact={compact} />
        ),
      )}
    </div>
  )
}

function AttachmentCard({ name, body }: { name: string; body: string }) {
  const [open, setOpen] = useState(false)
  const bytes = useMemo(() => new Blob([body]).size, [body])
  return (
    <DisclosureCard
      expanded={open}
      onToggle={setOpen}
      className="w-[min(28rem,100%)] border-border/70 bg-background/50"
      headerClassName="px-2.5 py-1.5 hover:bg-accent/40"
      header={
        <>
          <FileText className="h-3.5 w-3.5 shrink-0 text-primary" />
          <span className="min-w-0 flex-1 truncate text-xs font-medium text-foreground">{name}</span>
          <span className="shrink-0 font-mono text-[10px] text-muted-foreground">{formatBytes(bytes)}</span>
        </>
      }
    >
      <pre className="max-h-72 overflow-auto whitespace-pre-wrap border-t border-border/60 bg-card/40 px-2.5 py-2 font-mono text-[11px] leading-relaxed text-muted-foreground [overflow-wrap:anywhere]">
        {body}
      </pre>
    </DisclosureCard>
  )
}

function formatBytes(bytes: number): string {
  if (bytes < 1024) return `${bytes}B`
  if (bytes < 1024 * 1024) return `${(bytes / 1024).toFixed(1)}KB`
  return `${(bytes / (1024 * 1024)).toFixed(1)}MB`
}
