import { useState, useEffect, type ReactNode } from 'react'
import {
  AlertTriangle,
  Check,
  CheckCircle2,
  Code2,
  Loader2,
  Terminal,
  Wrench,
} from 'lucide-react'
import { cn } from '@cyber/theme'
import { DisclosureCard, Badge } from '@cyber/ui'
import { CodeBlock } from '@/markdown'
import { stripAnsiControl, formatArgs, summarizeArgs } from '../../lib/tool-utils'
import { listSCONodes } from '@/api'
import { EasmResultFromNodes } from '@cyber/cstx-easm'
import type { SCONode } from '@cyber/cstx-easm'

const SCANNER_COMMANDS = new Set(['gogo', 'spray', 'zombie', 'neutron', 'katana', 'proton', 'scan'])

function extractPseudoCommand(toolName: string, toolArgs: string): string | undefined {
  if (toolName !== 'bash') return undefined
  try {
    const parsed = JSON.parse(toolArgs)
    const cmd = (parsed.command || '').trim()
    const first = cmd.split(/\s+/)[0]
    return SCANNER_COMMANDS.has(first) ? first : undefined
  } catch {
    return undefined
  }
}

export interface ToolCallDisplayProps {
  toolName: string
  toolArgs?: string
  result?: string
  pending?: boolean
  toolCallId?: string
  defaultExpanded?: boolean
  className?: string
}

export default function ToolCallDisplay({
  toolName,
  toolArgs = '',
  result,
  pending = false,
  toolCallId,
  defaultExpanded = false,
  className,
}: ToolCallDisplayProps) {
  const summary = summarizeArgs(toolArgs)
  const formattedArgs = formatArgs(toolArgs)
  const displayResult = result === undefined ? undefined : stripAnsiControl(result)
  const scannerCmd = extractPseudoCommand(toolName, toolArgs)

  const [scoNodes, setScoNodes] = useState<SCONode[] | null>(null)
  const [scoLoading, setScoLoading] = useState(false)

  useEffect(() => {
    if (!scannerCmd || !toolCallId || pending) return
    setScoLoading(true)
    listSCONodes({ scanId: toolCallId }).then((nodes) => {
      setScoNodes(nodes.length > 0 ? nodes : null)
    }).catch(() => {
      setScoNodes(null)
    }).finally(() => setScoLoading(false))
  }, [scannerCmd, toolCallId, pending])

  return (
    <DisclosureCard
      animated
      defaultExpanded={defaultExpanded || (!!scannerCmd && !pending)}
      className={cn(
        'transition-colors duration-200',
        pending ? 'border-warning/30' : 'border-border',
        className,
      )}
      header={
        <>
          <Wrench
            className={cn(
              'h-3.5 w-3.5 shrink-0 transition-colors',
              pending ? 'text-warning' : 'text-muted-foreground',
            )}
          />
          <Badge
            variant="outline"
            size="sm"
            className="shrink-0 bg-muted/40 font-mono font-medium text-foreground"
          >
            {scannerCmd || toolName || 'tool'}
          </Badge>
          <span
            className="min-w-0 flex-1 truncate font-mono text-muted-foreground"
            title={summary || formattedArgs}
          >
            {summary || (pending ? 'running' : 'completed')}
          </span>
          {pending ? (
            <Loader2 className="h-3 w-3 shrink-0 animate-spin text-warning" />
          ) : (
            <Check className="h-3 w-3 shrink-0 text-success" />
          )}
        </>
      }
    >
      <div className="border-t border-border">
        {/* SCO structured view for scanner tools */}
        {scoNodes && scoNodes.length > 0 && (
          <div className="p-3">
            <EasmResultFromNodes nodes={scoNodes} />
          </div>
        )}

        {/* Loading state for SCO fetch */}
        {scoLoading && (
          <div className="flex items-center gap-2 px-3 py-2 text-xs text-muted-foreground">
            <Loader2 className="h-3 w-3 animate-spin" />
            <span>Loading structured results...</span>
          </div>
        )}

        {/* Raw text fallback (always shown for non-scanner or when no SCO data) */}
        {displayResult !== undefined && (!scoNodes || scoNodes.length === 0) && (
          <div className="border-t border-border px-3 py-2">
            <div className="mb-1 text-[10px] font-medium uppercase tracking-wider text-muted-foreground">
              Result
            </div>
            <pre className="max-h-60 overflow-auto whitespace-pre-wrap break-words rounded font-mono text-xs text-foreground">
              {displayResult}
            </pre>
          </div>
        )}

        {/* For scanner tools with SCO data, show raw output in a collapsible section */}
        {scoNodes && scoNodes.length > 0 && displayResult !== undefined && (
          <details className="border-t border-border">
            <summary className="cursor-pointer px-3 py-1.5 text-[10px] font-medium uppercase tracking-wider text-muted-foreground hover:text-foreground">
              Raw Output
            </summary>
            <pre className="max-h-40 overflow-auto whitespace-pre-wrap break-words px-3 pb-2 font-mono text-xs text-muted-foreground">
              {displayResult}
            </pre>
          </details>
        )}
      </div>
    </DisclosureCard>
  )
}

/* ---- Code call rendering (uses CodeBlock) ---- */

export interface CodeCallDisplayProps {
  language: string
  label: string
  code: string
  toolCallId?: string
  defaultExpanded?: boolean
  className?: string
}

export function CodeCallDisplay({
  language,
  label,
  code,
  toolCallId,
  defaultExpanded = false,
  className,
}: CodeCallDisplayProps) {
  const firstLine = code.split('\n')[0].slice(0, 80)

  return (
    <DisclosureCard
      defaultExpanded={defaultExpanded}
      className={cn('border-warning/30', className)}
      headerClassName="bg-warning/10 hover:bg-warning/20"
      collapsedPreview={
        <div className="truncate border-t border-border px-3 py-1 font-mono text-[10px] text-muted-foreground">
          {firstLine}
        </div>
      }
      header={
        <>
          <Code2 className="h-3.5 w-3.5 shrink-0 text-warning" />
          <span className="text-[10px] font-semibold uppercase text-warning">{label}</span>
          {toolCallId && (
            <span className="ml-auto mr-1 font-mono text-[9px] text-muted-foreground">
              {toolCallId.slice(0, 8)}
            </span>
          )}
        </>
      }
    >
      <div className="border-t border-border">
        <CodeBlock
          code={code}
          language={language}
          showLineNumbers
          maxHeight={288}
          className="rounded-none border-0"
        />
      </div>
    </DisclosureCard>
  )
}

/* ---- Structured BlockingOutput (stdout/stderr/traceback/result) ---- */

export interface BlockingOutputDisplayProps {
  toolName: string
  rawContent: Record<string, unknown>
  toolCallId?: string
  defaultExpanded?: boolean
  className?: string
}

export function BlockingOutputDisplay({
  toolName,
  rawContent,
  toolCallId,
  defaultExpanded = false,
  className,
}: BlockingOutputDisplayProps) {
  const stdout = rawContent.stdout as string | undefined
  const stderr = rawContent.stderr as string | undefined
  const result = rawContent.result
  const tb = rawContent.traceback as Record<string, unknown> | undefined

  const hasStdout = stdout && stdout.trim()
  const hasStderr = stderr && stderr.trim()
  const hasTb = !!tb
  const hasResult = result != null

  const summary = hasTb
    ? `Error: ${(tb.exc_type as string) ?? 'Exception'}`
    : hasStdout
      ? stdout.trim().split('\n')[0].slice(0, 80)
      : hasResult
        ? String(typeof result === 'string' ? result : JSON.stringify(result)).slice(0, 80)
        : '(no output)'

  return (
    <DisclosureCard
      defaultExpanded={defaultExpanded}
      className={cn('border-success/30', className)}
      headerClassName="bg-success/10 hover:bg-success/20"
      collapsedPreview={
        <div className="truncate border-t border-border px-3 py-1 font-mono text-[10px] text-muted-foreground">
          {summary}
        </div>
      }
      header={
        <>
          <Terminal className="h-3.5 w-3.5 shrink-0 text-success" />
          <span className="text-[10px] font-semibold uppercase text-success">Return</span>
          <span className="min-w-0 flex-1 truncate font-mono text-muted-foreground">{toolName}</span>
          {toolCallId && (
            <span className="mr-1 font-mono text-[9px] text-muted-foreground">
              {toolCallId.slice(0, 8)}
            </span>
          )}
        </>
      }
    >
      <div className="flex flex-col gap-1.5 border-t border-border p-2">
          {hasTb && (
            <OutputSection
              icon={<AlertTriangle className="h-3 w-3 text-destructive" />}
              label={(tb.exc_type as string) ?? 'Error'}
              labelClass="text-destructive"
              borderClass="border-destructive/30"
              bgClass="bg-destructive/10"
            >
              <div className="px-2 py-1.5 text-xs text-destructive">
                {tb.exc_value as string}
              </div>
              {Array.isArray(tb.frames) && (tb.frames as unknown[]).length > 0 && (
                <pre className="border-t border-destructive/20 px-2 py-1.5 font-mono text-[10px] text-destructive/70">
                  {(tb.frames as Array<Record<string, unknown>>)
                    .map((f) => `  ${f.filename}:${f.lineno} in ${f.name}\n    ${f.line ?? ''}\n`)
                    .join('')}
                </pre>
              )}
            </OutputSection>
          )}

          {hasStdout && (
            <OutputSection
              icon={<Terminal className="h-3 w-3 text-success" />}
              label="stdout"
              labelClass="text-success"
              borderClass="border-success/30"
              bgClass="bg-success/10"
            >
              <pre className="max-h-40 overflow-auto whitespace-pre-wrap px-2 py-1.5 font-mono text-xs text-foreground">
                {stdout.trim()}
              </pre>
            </OutputSection>
          )}

          {hasStderr && (
            <OutputSection
              icon={<AlertTriangle className="h-3 w-3 text-warning" />}
              label="stderr"
              labelClass="text-warning"
              borderClass="border-warning/30"
              bgClass="bg-warning/10"
            >
              <pre className="max-h-40 overflow-auto whitespace-pre-wrap px-2 py-1.5 font-mono text-xs text-warning">
                {stderr.trim()}
              </pre>
            </OutputSection>
          )}

          {hasResult && (
            <OutputSection
              icon={<CheckCircle2 className="h-3 w-3 text-info" />}
              label="result"
              labelClass="text-info"
              borderClass="border-info/30"
              bgClass="bg-info/10"
            >
              <CodeBlock
                code={typeof result === 'string' ? result : JSON.stringify(result, null, 2)}
                language="json"
                maxHeight={192}
                className="rounded-none border-0"
              />
            </OutputSection>
          )}

          {!hasTb && !hasStdout && !hasStderr && !hasResult && (
            <div className="px-2 py-1.5 text-xs text-muted-foreground">(no output)</div>
          )}
      </div>
    </DisclosureCard>
  )
}

export function OutputSection({
  icon,
  label,
  labelClass,
  borderClass,
  bgClass,
  children,
}: {
  icon: ReactNode
  label: string
  labelClass: string
  borderClass: string
  bgClass: string
  children: ReactNode
}) {
  return (
    <div className={cn('overflow-hidden rounded border', borderClass)}>
      <div className={cn('flex items-center gap-1.5 border-b px-2 py-1', borderClass, bgClass)}>
        {icon}
        <span className={cn('text-[10px] font-semibold', labelClass)}>{label}</span>
      </div>
      {children}
    </div>
  )
}
