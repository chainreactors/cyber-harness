import { type ReactNode } from 'react'
import { cn } from '@cyber/theme'
import { formatTime } from '../../../lib/format'
import { Collapsible } from '@cyber/ui'
import { AgentVoiceCard } from './AgentVoiceCard'
import { StreamingCursor } from './MessageBubble'
import { ThinkingDots } from './ChatThinking'

export interface AssistantResponseProps {
  actorName?: string | null
  timestamp?: string
  thinking?: ReactNode
  tools?: ReactNode
  response?: ReactNode
  streaming?: boolean
  defaultThinkingExpanded?: boolean
  className?: string
  labels?: {
    assistant?: string
    thinking?: string
    tools?: string
    response?: string
  }
}

export default function AssistantResponse({
  actorName,
  className,
  defaultThinkingExpanded = false,
  labels,
  response,
  streaming,
  thinking,
  timestamp,
  tools,
}: AssistantResponseProps) {
  const time = timestamp ? formatTime(timestamp) : ''
  const hasThinking = hasContent(thinking)
  const hasTools = hasContent(tools)
  const hasResponse = hasContent(response)
  const showResponse = hasResponse || !!streaming
  const responseIsLast = !hasTools

  return (
    <div data-testid="assistant-response" className={cn('flex w-full flex-col', className)}>
      {/* Actor + time — below xl only. At xl the timeline rail carries identity,
          so this would just duplicate it. */}
      <div className="mb-1 flex items-center gap-2 text-[10px] text-muted-foreground xl:hidden">
        <span className="font-medium">{actorName || (labels?.assistant || 'Assistant')}</span>
        {time && <span className="font-mono">{time}</span>}
      </div>

      {/* card — a hairline Cortex-blue left edge marks it as the agent's voice,
          echoing the rail's blue node without a competing avatar (AgentVoiceCard). */}
      <AgentVoiceCard streaming={streaming} className="overflow-hidden">
        {hasThinking && (
          <Collapsible
            title={labels?.thinking || 'Thinking'}
            defaultExpanded={defaultThinkingExpanded}
            className={cn(!showResponse && !hasTools ? '' : 'border-b border-border')}
            bodyClassName="text-sm leading-relaxed text-muted-foreground"
          >
            {thinking}
          </Collapsible>
        )}

        {showResponse && (
          <Section
            testId="assistant-response-content"
            title={labels?.response || 'Response'}
            last={responseIsLast}
          >
            {hasResponse ? (
              <div className="text-sm leading-relaxed">
                {response}
                {/* Typing caret only while the text itself is streaming. Once the
                    turn has moved on to tool calls, `streaming` stays true to keep
                    the run "busy" (send/stop button) — but the text is done, so a
                    caret here would linger after the message ends. Gate on !hasTools. */}
                {streaming && !hasTools && <StreamingCursor />}
              </div>
            ) : (
              <ThinkingDots className="py-1" />
            )}
          </Section>
        )}

        {hasTools && (
          <Section
            testId="assistant-response-tools"
            title={labels?.tools || 'Tools'}
            last
          >
            {tools}
          </Section>
        )}
      </AgentVoiceCard>
    </div>
  )
}

function Section({
  children,
  last,
  testId,
  title,
}: {
  children: ReactNode
  last?: boolean
  testId: string
  title: string
}) {
  return (
    <section data-testid={testId} className={cn('px-3 py-2', !last && 'border-b border-border')}>
      <div className="mb-1.5 text-[10px] font-medium uppercase tracking-wider text-muted-foreground">
        {title}
      </div>
      {children}
    </section>
  )
}

function hasContent(value: ReactNode) {
  return value !== undefined && value !== null && value !== false
}
