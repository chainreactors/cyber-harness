import { type ReactNode } from 'react'
import { cn } from '@cyber/theme'
import { AgentVoiceCard } from './AgentVoiceCard'

export interface ChatThinkingProps {
  actorName?: string | null
  children?: ReactNode
  className?: string
}

export default function ChatThinking({ actorName, children, className }: ChatThinkingProps) {
  return (
    <div className={cn('flex w-full flex-col', className)}>
      {/* actor — below xl only; the timeline rail carries it at xl */}
      <div className="mb-1 text-[10px] font-medium text-muted-foreground xl:hidden">
        {actorName || 'Agent'}
      </div>
      {children ? (
        // Streamed reasoning text — a card with the same Cortex-blue left edge as
        // the agent's response (the shared AgentVoiceCard shell), so thinking
        // reads as the same voice.
        <AgentVoiceCard className="px-3 py-2 text-sm leading-relaxed text-muted-foreground">
          {children}
        </AgentVoiceCard>
      ) : (
        // Live "working" pulse — just the dots, no empty card to fill.
        <div className="flex h-7 items-center">
          <ThinkingDots />
        </div>
      )}
    </div>
  )
}

export function ThinkingDots({ className }: { className?: string }) {
  return (
    <div className={cn('flex items-center gap-1.5', className)}>
      <span className="h-2 w-2 animate-bounce rounded-full bg-primary [animation-delay:0ms] [animation-duration:1s]" />
      <span className="h-2 w-2 animate-bounce rounded-full bg-primary [animation-delay:150ms] [animation-duration:1s]" />
      <span className="h-2 w-2 animate-bounce rounded-full bg-primary [animation-delay:300ms] [animation-duration:1s]" />
    </div>
  )
}
