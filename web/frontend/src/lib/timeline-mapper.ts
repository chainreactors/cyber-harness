import type { TimelineItem as ViewerTimelineItem } from '@aspect/viewer'
import type { TimelineItem as LocalTimelineItem } from '../hooks/useChatSession'

export function toViewerTimeline(items: LocalTimelineItem[]): ViewerTimelineItem[] {
  return items.map(toViewerTimelineItem).filter(Boolean) as ViewerTimelineItem[]
}

function toViewerTimelineItem(item: LocalTimelineItem): ViewerTimelineItem | null {
  switch (item.kind) {
    case 'message': {
      if (!item.message) return null
      const role = item.message.role
      if (role === 'tool_call' || role === 'tool_result') {
        return {
          id: item.id,
          kind: 'message',
          timestamp: item.timestamp,
          actorName: item.message.agent_name,
          role: 'system',
          content: item.message.content,
          metadata: item.message.metadata,
        }
      }
      return {
        id: item.id,
        kind: 'message',
        timestamp: item.timestamp,
        actorName: item.message.agent_name,
        role: role as 'user' | 'assistant' | 'system',
        content: item.message.content,
        metadata: item.message.metadata,
      }
    }

    case 'assistant_response': {
      if (!item.assistantResponse) return null
      const ar = item.assistantResponse
      return {
        id: item.id,
        kind: 'assistant_response',
        timestamp: item.timestamp,
        actorName: ar.agentName || ar.response?.agent_name,
        thinking: ar.thinking,
        tools: ar.tools.map((t) => ({
          id: t.id,
          toolName: t.toolName,
          toolArgs: t.toolArgs,
          result: t.result,
          pending: t.pending,
        })),
        response: ar.response?.content
          ? { content: ar.response.content, metadata: ar.response.metadata }
          : undefined,
        streaming: ar.streaming,
      }
    }

    case 'tool_call': {
      if (!item.toolCall) return null
      return {
        id: item.id,
        kind: 'tool_call',
        timestamp: item.timestamp,
        actorName: item.agentName,
        toolCall: {
          id: item.toolCall.id,
          toolName: item.toolCall.toolName,
          toolArgs: item.toolCall.toolArgs,
          result: item.toolCall.result,
          pending: item.toolCall.pending,
        },
      }
    }

    case 'thinking':
      return {
        id: item.id,
        kind: 'message',
        timestamp: item.timestamp,
        actorName: item.agentName,
        role: 'thinking',
        content: item.content || '',
      }

    case 'scan_started':
      return {
        id: item.id,
        kind: 'extension',
        timestamp: item.timestamp,
        extensionType: 'scan_started',
        data: {
          scanID: item.scanID || '',
          lines: item.scanLines || [],
        },
      }

    case 'scan_progress':
      return {
        id: item.id,
        kind: 'extension',
        timestamp: item.timestamp,
        extensionType: 'scan_started',
        data: {
          scanID: item.scanID || '',
          lines: item.scanLines || [],
        },
      }

    case 'scan_complete':
      return {
        id: item.id,
        kind: 'extension',
        timestamp: item.timestamp,
        extensionType: 'scan_complete',
        data: {
          scanID: item.scanID || '',
          result: item.scanResult,
        },
      }

    case 'agent_joined':
      return {
        id: item.id,
        kind: 'extension',
        timestamp: item.timestamp,
        extensionType: 'agent_joined',
        data: {
          agentName: item.agentName || '',
        },
      }

    default:
      return null
  }
}
