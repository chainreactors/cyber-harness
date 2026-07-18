import { useEffect, useRef, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { Loader2, CheckCircle2 } from 'lucide-react'
import { cn } from '@cyber/theme'
import { DisclosureCard, Badge } from '@cyber/ui'

interface Props {
  scanID: string
  lines: string[]
  complete?: boolean
}

export default function ScanProgressInline({ scanID, lines, complete }: Props) {
  const { t } = useTranslation('scan')
  const [expanded, setExpanded] = useState(!complete)
  const logRef = useRef<HTMLDivElement>(null)
  const lastLine = lines[lines.length - 1] || t('startingScan')

  useEffect(() => {
    if (expanded && logRef.current) {
      logRef.current.scrollTop = logRef.current.scrollHeight
    }
  }, [lines.length, expanded])

  return (
    <DisclosureCard
      animated
      expanded={expanded}
      onToggle={setExpanded}
      className={cn(
        'rounded-xl bg-card shadow-soft transition-colors duration-200',
        complete ? 'border-primary/30' : 'border-info/35',
      )}
      headerClassName={cn(complete ? 'hover:bg-primary/5' : 'hover:bg-info/5')}
      header={
        <>
          {!complete ? (
            <Loader2 className="h-3.5 w-3.5 shrink-0 animate-spin text-info" />
          ) : (
            <CheckCircle2 className="h-3.5 w-3.5 shrink-0 text-primary" />
          )}
          <span className={cn(
            'min-w-0 flex-1 truncate font-mono',
            complete ? 'text-muted-foreground' : 'text-foreground',
          )}>
            {lastLine}
          </span>
          <Badge variant="muted" size="sm" className="shrink-0 rounded-full font-mono">
            {lines.length}
          </Badge>
        </>
      }
    >
      {lines.length > 0 && (
        <div
          ref={logRef}
          // Append-only progress log: role="log" + polite live region so a
          // screen reader announces each new scanner line as it streams in,
          // and aria-relevant="additions" keeps it to the new tail rather
          // than re-reading the whole buffer on every update.
          role="log"
          aria-live="polite"
          aria-relevant="additions"
          aria-label={t('scanLog')}
          className={cn(
            'max-h-48 overflow-auto border-t border-border/60 bg-background/80 px-3 py-2',
            'font-mono text-[11px] leading-[1.5] text-foreground/75',
          )}
        >
          {lines.map((line, i) => (
            <div key={i} className="whitespace-pre-wrap break-words hover:bg-foreground/5 px-1 -mx-1 rounded">
              {line}
            </div>
          ))}
        </div>
      )}
    </DisclosureCard>
  )
}
