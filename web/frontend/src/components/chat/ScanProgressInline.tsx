import { useEffect, useRef, useState } from 'react'
import { ChevronDown, ChevronRight, Loader2, CheckCircle2 } from 'lucide-react'
import { cn } from '@aspect/theme'

interface Props {
  scanID: string
  lines: string[]
  complete?: boolean
}

export default function ScanProgressInline({ scanID, lines, complete }: Props) {
  const [expanded, setExpanded] = useState(!complete)
  const logRef = useRef<HTMLDivElement>(null)
  const lastLine = lines[lines.length - 1] || 'Starting scan...'

  useEffect(() => {
    if (expanded && logRef.current) {
      logRef.current.scrollTop = logRef.current.scrollHeight
    }
  }, [lines.length, expanded])

  return (
    <div className={cn(
      'rounded-lg border overflow-hidden transition-colors duration-200',
      complete ? 'border-primary/30' : 'border-blue-400/30',
    )}>
      <button
        type="button"
        onClick={() => setExpanded(!expanded)}
        className={cn(
          'flex w-full items-center gap-2 px-3 py-2 text-left text-xs transition-colors',
          complete ? 'hover:bg-primary/5' : 'hover:bg-blue-400/5',
        )}
      >
        {!complete ? (
          <Loader2 className="h-3.5 w-3.5 shrink-0 animate-spin text-blue-400" />
        ) : (
          <CheckCircle2 className="h-3.5 w-3.5 shrink-0 text-primary" />
        )}
        <span className={cn(
          'min-w-0 flex-1 truncate font-mono',
          complete ? 'text-muted-foreground' : 'text-foreground',
        )}>
          {lastLine}
        </span>
        <span className="shrink-0 rounded-full bg-muted px-1.5 py-0.5 text-[10px] font-mono text-muted-foreground">
          {lines.length}
        </span>
        {expanded ? (
          <ChevronDown className="h-3 w-3 shrink-0 text-muted-foreground" />
        ) : (
          <ChevronRight className="h-3 w-3 shrink-0 text-muted-foreground" />
        )}
      </button>

      <div
        className={cn(
          'grid transition-[grid-template-rows] duration-200 ease-in-out',
          expanded ? 'grid-rows-[1fr]' : 'grid-rows-[0fr]',
        )}
      >
        <div className="overflow-hidden">
          {lines.length > 0 && (
            <div
              ref={logRef}
              className={cn(
                'max-h-48 overflow-auto border-t border-border bg-[#060a0d] px-3 py-2',
                'font-mono text-[11px] leading-[1.5] text-gray-300',
              )}
            >
              {lines.map((line, i) => (
                <div key={i} className="whitespace-pre-wrap break-words hover:bg-white/5 px-1 -mx-1 rounded">
                  {line}
                </div>
              ))}
            </div>
          )}
        </div>
      </div>
    </div>
  )
}
