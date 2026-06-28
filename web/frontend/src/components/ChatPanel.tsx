import { useEffect, useRef, type ReactNode } from 'react'
import {
  Activity,
  AlertTriangle,
  Bot,
  CheckCircle2,
  CircleDashed,
  GitBranch,
  MessageSquare,
  PanelRight,
  PanelRightClose,
  Settings,
  User,
  Wrench,
  X,
} from 'lucide-react'
import {
  AssistantResponse,
  Button,
  ChatThinking,
  MessageBubble as ChatMessageBubble,
  ThemeToggle,
  ToolCallDisplay as ChatToolCall,
  summarizeArgs,
} from '@aspect/ui'
import { cn } from '@aspect/theme'
import { useTheme } from '@aspect/theme'
import { MarkdownContent } from '@aspect/markdown'
import type { ChatMessage, ScanResult } from '../api'
import type { AssistantResponseState, TimelineItem } from '../hooks/useChatSession'
import ChatInput from './chat/ChatInput'
import ScanProgressInline from './chat/ScanProgressInline'
import ScanSummaryCard from './chat/ScanSummaryCard'

const workspaceClass = 'mx-auto w-full max-w-[96rem] px-4 sm:px-5 lg:px-6'
const contentOffsetClass = 'xl:ml-[10.75rem]'
const threadOffsetClass = '2xl:mr-[14.75rem]'

interface Props {
  timeline: TimelineItem[]
  streamingText: string | null
  streamingAgent: string | null
  scanResults: Map<string, ScanResult>
  isThinking: boolean
  isBusy: boolean
  error: string
  hasActiveSession: boolean
  onSend: (content: string) => void
  onPause: () => void
  onClearError: () => void
  onShowScanDetail: (scanID: string) => void
  detailOpen: boolean
  onToggleDetail: () => void
  onOpenConfig: () => void
  onOpenScan: () => void
  agentsPill: ReactNode
}

export default function ChatPanel({
  timeline,
  streamingText,
  streamingAgent,
  scanResults,
  isThinking,
  isBusy,
  error,
  hasActiveSession,
  onSend,
  onPause,
  onClearError,
  onShowScanDetail,
  detailOpen,
  onToggleDetail,
  onOpenConfig,
  onOpenScan,
  agentsPill,
}: Props) {
  const scrollRef = useRef<HTMLDivElement>(null)
  const bottomRef = useRef<HTMLDivElement>(null)
  const stickRef = useRef(true)
  const { isDark, toggle: toggleTheme } = useTheme()
  const inputFormClass = cn(contentOffsetClass, !detailOpen && threadOffsetClass)
  const hasAssistantResponse = timeline.some((item) => item.kind === 'assistant_response')

  useEffect(() => {
    if (stickRef.current) {
      bottomRef.current?.scrollIntoView({ behavior: 'smooth' })
    }
  }, [timeline.length, streamingText, isThinking])

  function handleScroll() {
    const el = scrollRef.current
    if (!el) return
    const atBottom = el.scrollHeight - el.scrollTop - el.clientHeight < 80
    stickRef.current = atBottom
  }

  return (
    <div className="flex min-w-0 flex-1 flex-col">
      <div className="flex h-11 shrink-0 items-center justify-between border-b border-border bg-card/85 px-3 backdrop-blur-sm">
        <div className="flex items-center gap-2 text-sm font-medium text-foreground">
          <MessageSquare className="h-4 w-4 text-primary" />
          Chat
        </div>
        <div className="flex items-center gap-1">
          {agentsPill}
          <Button variant="ghost" size="icon" onClick={onOpenScan} className="h-7 w-7 text-muted-foreground" aria-label="Open scan workspace">
            <Activity className="h-3.5 w-3.5" />
          </Button>
          <Button variant="ghost" size="icon" onClick={onOpenConfig} className="h-7 w-7 text-muted-foreground" aria-label="LLM Config">
            <Settings className="h-3.5 w-3.5" />
          </Button>
          <ThemeToggle isDark={isDark} onToggle={toggleTheme} size="sm" />
          <Button
            variant="ghost"
            size="icon"
            onClick={onToggleDetail}
            className={cn('h-7 w-7 text-muted-foreground', detailOpen && 'bg-primary/10 text-primary')}
            aria-label={detailOpen ? 'Hide detail panel' : 'Show detail panel'}
          >
            {detailOpen ? <PanelRightClose className="h-3.5 w-3.5" /> : <PanelRight className="h-3.5 w-3.5" />}
          </Button>
        </div>
      </div>

      {error && (
        <div
          role="alert"
          className="flex items-start gap-2 border-b border-destructive/30 bg-destructive/10 px-4 py-2 text-sm text-destructive animate-in fade-in slide-in-from-top-1 duration-200"
        >
          <AlertTriangle className="mt-0.5 h-4 w-4 shrink-0" />
          <span className="min-w-0 flex-1 break-words">{error}</span>
          <button type="button" aria-label="Dismiss" onClick={onClearError} className="rounded p-0.5 hover:bg-destructive/10">
            <X className="h-4 w-4" />
          </button>
        </div>
      )}

      <main className="flex min-h-0 flex-1 flex-col bg-background">
        <div
          ref={scrollRef}
          onScroll={handleScroll}
          className="min-h-0 flex-1 overflow-y-auto"
        >
          <div className={cn(workspaceClass, 'space-y-3 py-4')}>
            {!hasActiveSession && timeline.length === 0 && (
              <div className={inputFormClass}>
                <EmptyState
                  title="Start a conversation"
                  subtitle="Create a new session to begin chatting"
                />
              </div>
            )}
            {hasActiveSession && timeline.length === 0 && !isThinking && streamingText === null && (
              <div className={inputFormClass}>
                <EmptyState
                  title="Ready"
                  subtitle={
                    <>Type a message or use <code className="rounded bg-muted px-1 py-0.5 text-[10px] font-mono">/scan &lt;target&gt;</code> to start scanning</>
                  }
                />
              </div>
            )}

            {timeline.map((item) => (
              <TimelineEntry
                key={item.id}
                item={item}
                scanResults={scanResults}
                detailOpen={detailOpen}
                onShowScanDetail={onShowScanDetail}
              />
            ))}

            {streamingText !== null && (
              <StreamingEntry
                text={streamingText}
                agentName={streamingAgent}
                detailOpen={detailOpen}
              />
            )}

            {isThinking && streamingText === null && !hasAssistantResponse && (
              <TimelineRow
                item={{
                  id: 'thinking-live',
                  kind: 'thinking',
                  timestamp: Date.now(),
                  agentName: streamingAgent || undefined,
                }}
                detailOpen={detailOpen}
              >
                <ChatThinking actorName={streamingAgent} />
              </TimelineRow>
            )}

            <div ref={bottomRef} />
          </div>
        </div>

        {hasActiveSession && (
          <ChatInput
            onSend={onSend}
            onPause={onPause}
            busy={isBusy}
            formClassName={inputFormClass}
          />
        )}
      </main>
    </div>
  )
}

function TimelineEntry({
  item,
  scanResults,
  detailOpen,
  onShowScanDetail,
}: {
  item: TimelineItem
  scanResults: Map<string, ScanResult>
  detailOpen: boolean
  onShowScanDetail: (scanID: string) => void
}) {
  const content = timelineContent(item, scanResults, onShowScanDetail)
  if (!content) return null

  return (
    <TimelineRow item={item} detailOpen={detailOpen}>
      {content}
    </TimelineRow>
  )
}

function StreamingEntry({
  text,
  agentName,
  detailOpen,
}: {
  text: string
  agentName: string | null
  detailOpen: boolean
}) {
  const now = new Date().toISOString()
  const message: ChatMessage = {
    id: 'streaming-assistant',
    session_id: '',
    role: 'assistant',
    agent_name: agentName || undefined,
    content: text,
    created_at: now,
  }

  return (
    <TimelineRow
      item={{ id: 'streaming-assistant', kind: 'message', timestamp: Date.now(), message }}
      detailOpen={detailOpen}
    >
      <ChatMessageBubble
        role="assistant"
        actorName={agentName || undefined}
        streaming
      >
        {text ? <MarkdownContent content={trimDisplayContent(text)} compact /> : null}
      </ChatMessageBubble>
    </TimelineRow>
  )
}

function TimelineRow({
  item,
  detailOpen,
  children,
}: {
  item: TimelineItem
  detailOpen: boolean
  children: ReactNode
}) {
  return (
    <div
      data-testid="chat-timeline-row"
      data-kind={item.kind}
      className={cn(
        'grid grid-cols-1 gap-y-1 animate-in fade-in slide-in-from-bottom-1 duration-200',
        'xl:grid-cols-[10rem_minmax(0,1fr)] xl:gap-x-3',
        !detailOpen && '2xl:grid-cols-[10rem_minmax(0,1fr)_14rem]',
      )}
    >
      <TimelineMark item={item} />
      <div data-testid="chat-content" className="min-w-0">
        {children}
      </div>
      {!detailOpen && <IOAThreadNote item={item} />}
    </div>
  )
}

function timelineContent(
  item: TimelineItem,
  scanResults: Map<string, ScanResult>,
  onShowScanDetail: (scanID: string) => void,
): ReactNode {
  switch (item.kind) {
    case 'message':
      if (!item.message) return null
      {
        const role = item.message.role === 'tool_call' || item.message.role === 'tool_result' ? 'system' : item.message.role
        return (
          <ChatMessageBubble
            role={role}
            actorName={item.message.agent_name}
            timestamp={item.message.created_at}
          >
            {item.message.content ? (
              <MarkdownContent content={trimDisplayContent(item.message.content)} compact={role !== 'system'} />
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
        />
      )

    case 'scan_started':
      return (
        <ScanProgressInline
          scanID={item.scanID || ''}
          lines={item.scanLines || []}
          complete={item.scanID ? scanResults.has(item.scanID) : false}
        />
      )

    case 'scan_complete':
      if (!item.scanResult || !item.scanID) return null
      return (
        <ScanSummaryCard
          scanID={item.scanID}
          result={item.scanResult}
          onViewDetails={onShowScanDetail}
        />
      )

    case 'thinking':
      return (
        <ChatThinking actorName={item.agentName}>
          {item.content?.trim() ? <MarkdownContent content={trimDisplayContent(item.content)} compact muted /> : null}
        </ChatThinking>
      )

    case 'agent_joined':
      return (
        <div className="flex items-center justify-center gap-2 py-1">
          <div className="h-px flex-1 bg-border" />
          <span className="flex items-center gap-1.5 text-[10px] text-muted-foreground">
            <Bot className="h-3 w-3" />
            {item.agentName} joined
          </span>
          <div className="h-px flex-1 bg-border" />
        </div>
      )

    default:
      return null
  }
}

function AssistantResponseEntry({ response }: { response: AssistantResponseState }) {
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
            />
          ))}
        </div>
      ) : undefined}
      response={hasResponse ? <MarkdownContent content={trimDisplayContent(message?.content || '')} compact /> : undefined}
      labels={{ tools: response.tools.length === 1 ? 'Tool' : 'Tools' }}
    />
  )
}

function TimelineMark({ item }: { item: TimelineItem }) {
  const descriptor = describeTimelineItem(item)
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
  const note = describeIOAThreadItem(item)
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

function EmptyState({ title, subtitle }: { title: string; subtitle: ReactNode }) {
  return (
    <div className="flex flex-col items-center justify-center py-20 text-center animate-in fade-in duration-300">
      <MessageSquare className="h-12 w-12 text-muted-foreground/10" strokeWidth={1} />
      <p className="mt-3 text-sm font-medium text-foreground">{title}</p>
      <p className="mt-1 text-xs text-muted-foreground">{subtitle}</p>
    </div>
  )
}

interface TimelineDescriptor {
  label: string
  time: string
  icon: ReactNode
  dotClass: string
}

function describeTimelineItem(item: TimelineItem): TimelineDescriptor | null {
  const time = formatRailTime(item)

  switch (item.kind) {
    case 'message': {
      if (!item.message) return null
      const role = item.message.role
      if (role === 'user') {
        return {
          label: 'You',
          time,
          icon: <User className="h-3 w-3 text-primary" />,
          dotClass: 'border-primary bg-primary',
        }
      }
      if (role === 'assistant') {
        return {
          label: item.message.agent_name || 'Assistant',
          time,
          icon: <Bot className="h-3 w-3 text-purple-500" />,
          dotClass: 'border-purple-400 bg-purple-400',
        }
      }
      return {
        label: 'System',
        time,
        icon: <MessageSquare className="h-3 w-3 text-muted-foreground" />,
        dotClass: 'border-border bg-muted-foreground/60',
      }
    }

    case 'assistant_response':
      return {
        label: item.assistantResponse?.agentName || item.agentName || 'Assistant',
        time,
        icon: <Bot className="h-3 w-3 text-purple-500" />,
        dotClass: item.assistantResponse?.streaming
          ? 'border-primary bg-background animate-pulse'
          : 'border-purple-400 bg-purple-400',
      }

    case 'tool_call':
      return {
        label: item.toolCall?.toolName || 'Tool',
        time,
        icon: <Wrench className="h-3 w-3 text-yellow-500" />,
        dotClass: item.toolCall?.pending ? 'border-warning bg-warning animate-pulse' : 'border-primary bg-primary',
      }

    case 'scan_started':
      return {
        label: 'Scan',
        time,
        icon: <Activity className="h-3 w-3 text-blue-500" />,
        dotClass: 'border-blue-400 bg-blue-400',
      }

    case 'scan_complete':
      return {
        label: 'Complete',
        time,
        icon: <CheckCircle2 className="h-3 w-3 text-emerald-500" />,
        dotClass: 'border-emerald-400 bg-emerald-400',
      }

    case 'thinking':
      return {
        label: item.agentName || 'Thinking',
        time,
        icon: <CircleDashed className="h-3 w-3 animate-spin text-primary" />,
        dotClass: 'border-primary bg-background',
      }

    case 'agent_joined':
      return {
        label: item.agentName || 'Agent',
        time,
        icon: <Bot className="h-3 w-3 text-primary" />,
        dotClass: 'border-primary bg-primary',
      }

    default:
      return null
  }
}

function describeIOAThreadItem(item: TimelineItem): { label: string; detail?: string } | null {
  if (item.kind === 'assistant_response' && item.assistantResponse) {
    const ioaTool = item.assistantResponse.tools.find((tool) => isIOATool(tool.toolName, tool.toolArgs))
    if (ioaTool) {
      return {
        label: ioaTool.toolName || 'ioa',
        detail: previewText(summarizeArgs(ioaTool.toolArgs) || ioaTool.result || '', 140),
      }
    }
  }

  if (item.kind === 'tool_call' && item.toolCall && isIOATool(item.toolCall.toolName, item.toolCall.toolArgs)) {
    return {
      label: item.toolCall.toolName || 'ioa',
      detail: previewText(summarizeArgs(item.toolCall.toolArgs) || item.toolCall.result || '', 140),
    }
  }

  if (item.kind === 'message' && item.message) {
    const metadata = item.message.metadata || {}
    const thread = metadata.ioa_thread || metadata.ioa_message || metadata.thread
    if (thread) {
      return {
        label: 'IOA message',
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
  return date.toLocaleTimeString(undefined, { hour: '2-digit', minute: '2-digit' })
}

function previewText(value: string, max: number): string {
  const compact = value.replace(/\s+/g, ' ').trim()
  if (compact.length <= max) return compact
  return `${compact.slice(0, Math.max(0, max - 1))}...`
}

function trimDisplayContent(value: string): string {
  return value.replace(/[ \t\r\n]+$/g, '')
}
