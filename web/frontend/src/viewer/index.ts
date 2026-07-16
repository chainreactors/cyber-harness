// First-party viewer kit — the curated slice of the former cyber-ui viewer
// package the app actually consumes: the timeline-renderer registry, tool-arg
// utilities and the presentational chat components. The upstream graph/APG
// dashboard is not vendored because nothing here imports it.

// Utility functions
export {
  stripAnsiControl,
  formatArgs,
  summarizeArgs,
} from './lib/tool-utils'

// Timeline renderer registry (extension mechanism)
export {
  registerTimelineRenderer,
  resolveTimelineRenderer,
} from './lib/timeline-registry'

// Presentational chat components (props-first, no provider dependency)
export { default as MessageBubble, StreamingCursor } from './components/chat/MessageBubble'
export { default as ToolCallDisplay, CodeCallDisplay, BlockingOutputDisplay, OutputSection } from './components/chat/ToolCallDisplay'
export { default as ChatThinking, ThinkingDots } from './components/chat/ChatThinking'
export { default as AssistantResponse } from './components/chat/AssistantResponse'
export { default as ChatInput } from './components/chat/ChatInput'

// Types — timeline registry
export type {
  ExtensionTimelineItem,
  TimelineItem,
  TimelineRendererConfig,
  TimelineRendererContext,
  TimelineRendererProps,
} from './lib/timeline-registry'

// Types — components
export type { MessageBubbleProps } from './components/chat/MessageBubble'
export type { ChatThinkingProps } from './components/chat/ChatThinking'
export type { AssistantResponseProps } from './components/chat/AssistantResponse'
export type { ToolCallDisplayProps, CodeCallDisplayProps, BlockingOutputDisplayProps } from './components/chat/ToolCallDisplay'
export type { ChatInputProps, CommandHint, ChatAttachment, AttachmentMode, Mentionable, MentionPopupApi } from './components/chat/ChatInput'
