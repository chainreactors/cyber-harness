import { useEffect, useMemo, useState } from 'react'
import { Check, Loader2, Wrench } from 'lucide-react'
import { EasmResultFromNodes, type SCONode } from '@cyber/cstx-easm'
import { Badge, DisclosureCard } from '@cyber/ui'
import { cn } from '@cyber/theme'
import { ToolCallDisplay, formatArgs, stripAnsiControl, summarizeArgs } from '@/viewer'
import { listSCONodes } from '../../api'

const SCANNER_COMMANDS = new Set(['gogo', 'spray', 'zombie', 'neutron', 'katana', 'proton', 'scan'])

function scannerCommand(toolName: string, toolArgs: string): string | undefined {
  const direct = toolName.trim().toLowerCase()
  if (SCANNER_COMMANDS.has(direct)) return direct
  if (direct !== 'bash') return undefined
  try {
    const parsed = JSON.parse(toolArgs) as Record<string, unknown>
    const command = typeof parsed.command === 'string' ? parsed.command.trim() : ''
    const first = command.split(/\s+/, 1)[0]?.toLowerCase() || ''
    return SCANNER_COMMANDS.has(first) ? first : undefined
  } catch {
    return undefined
  }
}

export interface ScannerToolCallProps {
  id: string
  toolName: string
  toolArgs?: string
  result?: string
  pending?: boolean
}

export default function ScannerToolCall({
  id,
  toolName,
  toolArgs = '',
  result,
  pending = false,
}: ScannerToolCallProps) {
  const command = useMemo(() => scannerCommand(toolName, toolArgs), [toolName, toolArgs])
  const [nodes, setNodes] = useState<SCONode[] | null>(null)
  const [loading, setLoading] = useState(false)

  useEffect(() => {
    if (!command || !id || pending) return
    let disposed = false
    setLoading(true)
    void listSCONodes({ scanId: id }).then((value) => {
      if (!disposed) setNodes(value.length > 0 ? value : null)
    }).catch(() => {
      if (!disposed) setNodes(null)
    }).finally(() => {
      if (!disposed) setLoading(false)
    })
    return () => { disposed = true }
  }, [command, id, pending])

  if (!command) {
    return (
      <ToolCallDisplay
        toolName={toolName}
        toolArgs={toolArgs}
        result={result}
        pending={pending}
      />
    )
  }

  const summary = summarizeArgs(toolArgs)
  const formattedArgs = formatArgs(toolArgs)
  const displayResult = result === undefined ? undefined : stripAnsiControl(result)

  return (
    <DisclosureCard
      animated
      defaultExpanded={!pending}
      className={cn('transition-colors duration-200', pending ? 'border-warning/30' : 'border-border')}
      header={
        <>
          <Wrench className={cn('h-3.5 w-3.5 shrink-0', pending ? 'text-warning' : 'text-muted-foreground')} />
          <Badge variant="outline" size="sm" className="shrink-0 bg-muted/40 font-mono font-medium text-foreground">
            {command}
          </Badge>
          <span className="min-w-0 flex-1 truncate font-mono text-muted-foreground" title={summary || formattedArgs}>
            {summary || (pending ? 'running' : 'completed')}
          </span>
          {pending
            ? <Loader2 className="h-3 w-3 shrink-0 animate-spin text-warning" />
            : <Check className="h-3 w-3 shrink-0 text-success" />}
        </>
      }
    >
      <div className="border-t border-border">
        {nodes && nodes.length > 0 && (
          <div className="p-3">
            <EasmResultFromNodes nodes={nodes} />
          </div>
        )}
        {loading && (
          <div className="flex items-center gap-2 px-3 py-2 text-xs text-muted-foreground">
            <Loader2 className="h-3 w-3 animate-spin" />
            <span>Loading structured results...</span>
          </div>
        )}
        {toolArgs && (
          <details className="border-t border-border">
            <summary className="cursor-pointer px-3 py-1.5 text-[10px] font-medium uppercase tracking-wider text-muted-foreground hover:text-foreground">
              Arguments
            </summary>
            <pre className="max-h-40 overflow-auto whitespace-pre-wrap break-words px-3 pb-2 font-mono text-xs text-muted-foreground">
              {formattedArgs}
            </pre>
          </details>
        )}
        {displayResult !== undefined && (
          <details className="border-t border-border" open={!nodes && !loading}>
            <summary className="cursor-pointer px-3 py-1.5 text-[10px] font-medium uppercase tracking-wider text-muted-foreground hover:text-foreground">
              Raw Output
            </summary>
            <pre className="max-h-60 overflow-auto whitespace-pre-wrap break-words px-3 pb-2 font-mono text-xs text-muted-foreground">
              {displayResult}
            </pre>
          </details>
        )}
      </div>
    </DisclosureCard>
  )
}
