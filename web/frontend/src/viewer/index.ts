// Re-export from the canonical @cyber/viewer package.
// The local component forks are no longer maintained — use the upstream versions.

import type { TimelineItem as CyberTimelineItem } from '../../cyber-ui/packages/viewer/src/types/timeline'

export interface SubagentRunTimelineItem {
  id: string
  kind: 'subagent_run'
  timestamp: number
  actorName?: string
  name: string
  prompt: string
  mode?: string
  sessionID?: string
  status: 'starting' | 'running' | 'completed' | 'failed' | 'canceled'
  items: ViewerTimelineItem[]
}

export type ViewerTimelineItem = CyberTimelineItem | SubagentRunTimelineItem

export {
  stripAnsiControl,
  formatArgs,
  summarizeArgs,
} from '../../cyber-ui/packages/viewer/src/lib/tool-utils'

export {
  registerTimelineRenderer,
  resolveTimelineRenderer,
} from '../../cyber-ui/packages/viewer/src/components/chat/timeline-registry'

export { default as MessageBubble, StreamingCursor } from '../../cyber-ui/packages/viewer/src/components/chat/MessageBubble'
export { default as ToolCallDisplay, CodeCallDisplay, BlockingOutputDisplay, OutputSection } from '../../cyber-ui/packages/viewer/src/components/chat/ToolCallDisplay'
export { default as ChatThinking, ThinkingDots } from '../../cyber-ui/packages/viewer/src/components/chat/ChatThinking'
export { default as AssistantResponse } from '../../cyber-ui/packages/viewer/src/components/chat/AssistantResponse'
export { default as ChatInput } from '../../cyber-ui/packages/viewer/src/components/chat/ChatInput'
export { AgentVoiceCard } from '../../cyber-ui/packages/viewer/src/components/chat/AgentVoiceCard'
export { ChatPanel } from '../../cyber-ui/packages/viewer/src/components/chat/ChatPanel'
export { reduceAOPToTimeline } from '../../cyber-ui/packages/viewer/src/lib/aop-reducer'

export type {
  TimelineRendererConfig,
} from '../../cyber-ui/packages/viewer/src/components/chat/timeline-registry'
export type {
  TimelineItem as CyberTimelineItem,
  ExtensionTimelineItem,
} from '../../cyber-ui/packages/viewer/src/types/timeline'
export type { Event as AOPEvent } from '@cyber/aop'

export type { MessageBubbleProps, MessageBubbleVariant } from '../../cyber-ui/packages/viewer/src/components/chat/MessageBubble'
export type { ChatThinkingProps } from '../../cyber-ui/packages/viewer/src/components/chat/ChatThinking'
export type { AssistantResponseProps } from '../../cyber-ui/packages/viewer/src/components/chat/AssistantResponse'
export type { ToolCallDisplayProps, CodeCallDisplayProps, BlockingOutputDisplayProps } from '../../cyber-ui/packages/viewer/src/components/chat/ToolCallDisplay'
export type { ChatInputProps, CommandHint, ChatAttachment, AttachmentMode, Mentionable, MentionPopupApi, PopupNavigationCommand, PopupNavigationKey } from '../../cyber-ui/packages/viewer/src/components/chat/ChatInput'
export type { AgentVoiceCardProps } from '../../cyber-ui/packages/viewer/src/components/chat/AgentVoiceCard'

// Backward compat: the local timeline-registry types used slightly different names
export type {
  TimelineItemRendererProps as TimelineRendererProps,
} from '../../cyber-ui/packages/viewer/src/components/chat/timeline-registry'
export type { ExtensionRendererProps as TimelineRendererContext } from '../../cyber-ui/packages/viewer/src/components/chat/timeline-registry'
