import * as React from 'react'
import { cn } from '@cyber/theme'

export interface AgentVoiceCardProps extends React.HTMLAttributes<HTMLDivElement> {
  /** While the turn is live, the border shifts to primary. */
  streaming?: boolean
}

/**
 * The shell that marks a card as "the agent's voice": a hairline Cortex-blue
 * left edge (`border-l-ai/40`) on a soft-elevated card, echoing the timeline
 * rail's blue node without a competing avatar. Shared by the assistant bubble,
 * the thinking card and the structured response card, so the AI-voice look has
 * one source of truth. Padding / overflow / text colour are the caller's via
 * `className`. Aiscan-local: depends on the app-local `ai` token.
 */
export const AgentVoiceCard = React.forwardRef<HTMLDivElement, AgentVoiceCardProps>(
  ({ streaming, className, ...props }, ref) => (
    <div
      ref={ref}
      className={cn(
        'rounded-lg border border-border border-l-2 border-l-ai/40 bg-card text-foreground shadow-soft',
        streaming && 'border-primary/40',
        className,
      )}
      {...props}
    />
  ),
)
AgentVoiceCard.displayName = 'AgentVoiceCard'
