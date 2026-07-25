import type { ReactNode } from 'react'
import { CheckCircle2, GitBranch, Loader2, OctagonX, XCircle } from 'lucide-react'
import { DisclosureCard } from '@cyber/ui'
import { cn } from '@cyber/theme'
import type { ViewerTimelineItem } from '@/viewer'

type SubagentRun = Extract<ViewerTimelineItem, { kind: 'subagent_run' }>

export default function SubagentRunCard({ run, children }: { run: SubagentRun; children: ReactNode }) {
  const running = run.status === 'starting' || run.status === 'running'
  const failed = run.status === 'failed' || run.status === 'canceled'
  const StatusIcon = running ? Loader2 : failed ? (run.status === 'canceled' ? OctagonX : XCircle) : CheckCircle2

  return (
    <DisclosureCard
      defaultExpanded={failed}
      className={cn(
        'border-border/80 bg-card/70',
        running && 'border-blue-500/30',
        failed && 'border-destructive/35',
      )}
      headerClassName="py-2.5"
      header={(
        <>
          <GitBranch className="h-4 w-4 shrink-0 text-blue-500" />
          <span className="min-w-0 flex-1 truncate font-medium text-foreground">{run.name}</span>
          {run.mode && (
            <span className="shrink-0 rounded border border-border bg-muted/60 px-1.5 py-0.5 font-mono text-[10px] text-muted-foreground">
              {run.mode}
            </span>
          )}
          <span className={cn(
            'inline-flex shrink-0 items-center gap-1 text-[10px]',
            running ? 'text-blue-500' : failed ? 'text-destructive' : 'text-success',
          )}>
            <StatusIcon className={cn('h-3.5 w-3.5', running && 'animate-spin')} />
            {run.status}
          </span>
        </>
      )}
      collapsedPreview={run.prompt ? (
        <p className="truncate border-t border-border/60 px-3 py-2 text-xs text-muted-foreground">
          {run.prompt}
        </p>
      ) : undefined}
    >
      <div className="space-y-3 border-t border-border/60 px-3 py-3">
        {run.prompt && (
          <div>
            <div className="mb-1 text-[10px] font-semibold uppercase tracking-wide text-muted-foreground">Task</div>
            <p className="whitespace-pre-wrap text-xs leading-relaxed text-foreground">{run.prompt}</p>
          </div>
        )}
        {run.sessionID && (
          <div className="font-mono text-[10px] text-muted-foreground">session {run.sessionID}</div>
        )}
        {run.items.length > 0 && <div className="space-y-3 border-t border-border/60 pt-3">{children}</div>}
      </div>
    </DisclosureCard>
  )
}
