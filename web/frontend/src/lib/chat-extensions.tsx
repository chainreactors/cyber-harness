import { Bot, CheckCircle2 } from 'lucide-react'
import { registerTimelineRenderer } from '@/viewer'
import i18n from '../i18n'
import type { SCONode } from '../api'
import ScanSummaryCard from '../components/chat/ScanSummaryCard'

export function registerChatExtensions() {
  registerTimelineRenderer('scan_complete', {
    renderer: ({ item, context }) => {
      const scanID = item.data.scanID as string
      // The hub persists a completed scan as SCO nodes keyed by scan_id; a live
      // scan_complete event carries them inline, a card rebuilt from a persisted
      // marker (page reload / session switch) falls back to the scanResults map
      // the session loads from its scan_ids. Until that map resolves the nodes
      // are absent — render nothing rather than an empty card; the row
      // re-renders and the card appears once the map fills.
      const scanResults = context.scanResults as Map<string, SCONode[]> | undefined
      const nodes = (item.data.nodes as SCONode[]) ?? scanResults?.get(scanID)
      if (!nodes) return null
      return (
        <ScanSummaryCard
          scanID={scanID}
          nodes={nodes}
        />
      )
    },
    mark: {
      label: () => i18n.t('chat:complete'),
      icon: CheckCircle2,
      dotClass: 'border-emerald-400 bg-emerald-400',
    },
  })

  registerTimelineRenderer('agent_joined', {
    renderer: ({ item }) => (
      <div className="flex items-center justify-center gap-2 py-1">
        <div className="h-px flex-1 bg-border" />
        <span className="flex items-center gap-1.5 text-[10px] text-muted-foreground">
          <Bot className="h-3 w-3" />
          {i18n.t('chat:agentJoined', { name: (item.data.agentName as string) || i18n.t('chat:agent') })}
        </span>
        <div className="h-px flex-1 bg-border" />
      </div>
    ),
    mark: {
      label: () => i18n.t('chat:agent'),
      icon: Bot,
      dotClass: 'border-primary bg-primary',
    },
  })
}
