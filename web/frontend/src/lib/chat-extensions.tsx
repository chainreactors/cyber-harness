import { Activity, Bot, CheckCircle2 } from 'lucide-react'
import { registerTimelineRenderer } from '@aspect/viewer'
import type { ScanResult } from '../api'
import ScanProgressInline from '../components/chat/ScanProgressInline'
import ScanSummaryCard from '../components/chat/ScanSummaryCard'

export function registerChatExtensions() {
  registerTimelineRenderer('scan_started', {
    renderer: ({ item, context }) => {
      const scanResults = context.scanResults as Map<string, ScanResult> | undefined
      return (
        <ScanProgressInline
          scanID={item.data.scanID as string}
          lines={item.data.lines as string[] ?? []}
          complete={scanResults?.has(item.data.scanID as string)}
        />
      )
    },
    mark: {
      label: 'Scan',
      icon: Activity,
      dotClass: 'border-blue-400 bg-blue-400',
    },
  })

  registerTimelineRenderer('scan_complete', {
    renderer: ({ item, context }) => {
      const onShowScanDetail = context.onShowScanDetail as ((id: string) => void) | undefined
      return (
        <ScanSummaryCard
          scanID={item.data.scanID as string}
          result={item.data.result as ScanResult}
          onViewDetails={onShowScanDetail ?? (() => {})}
        />
      )
    },
    mark: {
      label: 'Complete',
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
          {(item.data.agentName as string) || 'Agent'} joined
        </span>
        <div className="h-px flex-1 bg-border" />
      </div>
    ),
    mark: {
      label: 'Agent',
      icon: Bot,
      dotClass: 'border-primary bg-primary',
    },
  })
}
