import type { ComponentType } from 'react'

export interface TimelineRendererContext {
  [key: string]: unknown
}

export interface BaseTimelineItem {
  id: string
  kind: string
  timestamp: number
  actorName?: string
}

export interface MessageTimelineItem extends BaseTimelineItem {
  kind: 'message'
  role: 'user' | 'assistant' | 'system' | 'thinking'
  content: string
  metadata?: Record<string, unknown>
}

export interface AssistantResponseTimelineItem extends BaseTimelineItem {
  kind: 'assistant_response'
  thinking?: string
  tools: Array<{
    id: string
    toolName: string
    toolArgs?: string
    result?: string
    pending?: boolean
  }>
  response?: {
    content: string
    metadata?: Record<string, unknown>
  }
  streaming?: boolean
}

export interface ToolCallTimelineItem extends BaseTimelineItem {
  kind: 'tool_call'
  toolCall: {
    id: string
    toolName: string
    toolArgs?: string
    result?: string
    pending?: boolean
  }
}

export interface ExtensionTimelineItem<TData extends Record<string, unknown> = Record<string, unknown>> extends BaseTimelineItem {
  kind: 'extension'
  extensionType: string
  data: TData
}

export type TimelineItem =
  | MessageTimelineItem
  | AssistantResponseTimelineItem
  | ToolCallTimelineItem
  | ExtensionTimelineItem

export interface TimelineRendererProps<TItem extends ExtensionTimelineItem = ExtensionTimelineItem> {
  item: TItem
  context: TimelineRendererContext
}

export interface TimelineRendererConfig<TItem extends ExtensionTimelineItem = ExtensionTimelineItem> {
  renderer: ComponentType<TimelineRendererProps<TItem>>
  mark?: {
    label?: string | ((item: TItem) => string)
    icon?: ComponentType<{ className?: string }>
    dotClass?: string
  }
}

const timelineRenderers = new Map<string, TimelineRendererConfig>()

export function registerTimelineRenderer<TItem extends ExtensionTimelineItem>(
  type: string,
  config: TimelineRendererConfig<TItem>,
): void {
  timelineRenderers.set(type, config as unknown as TimelineRendererConfig)
}

export function resolveTimelineRenderer(type: string): TimelineRendererConfig | undefined {
  return timelineRenderers.get(type)
}
