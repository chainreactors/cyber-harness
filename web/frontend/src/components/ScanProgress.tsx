import { useEffect, useRef } from 'react'
import { ChevronDown, ChevronRight, Terminal } from 'lucide-react'

interface ScanProgressProps {
  lines: string[]
  status: string
  collapsed: boolean
  onToggleCollapse: () => void
}

const STAGES = ['Port Scan', 'Web Probe', 'Credentials', 'Vulns', 'Analysis']

export function detectStage(lines: string[]): number {
  for (let i = lines.length - 1; i >= 0; i--) {
    const line = lines[i].toLowerCase()
    if (line.includes('[ai') || line.includes('sniper') || line.includes('verify') || line.includes('[deep')) return 4
    if (line.includes('[vuln') || line.includes('neutron')) return 3
    if (line.includes('[risk') || line.includes('zombie') || line.includes('weakpass')) return 2
    if (line.includes('[web') || line.includes('[fingerprint') || line.includes('spray')) return 1
    if (line.includes('[service') || line.includes('gogo')) return 0
  }
  return 0
}

export default function ScanProgress({ lines, status, collapsed, onToggleCollapse }: ScanProgressProps) {
  const logRef = useRef<HTMLDivElement>(null)
  const activeStage = detectStage(lines)
  const isRunning = status === 'running'

  useEffect(() => {
    if (logRef.current && !collapsed) {
      logRef.current.scrollTop = logRef.current.scrollHeight
    }
  }, [lines, collapsed])

  return (
    <div className="space-y-3">
      {/* Thin progress bar */}
      <div className="space-y-1.5">
        <div className="flex h-1 overflow-hidden rounded bg-secondary">
          {STAGES.map((_, i) => (
            <div
              key={i}
              className={`flex-1 transition-all duration-500 ${
                i < activeStage
                  ? 'bg-primary'
                  : i === activeStage && isRunning
                  ? 'bg-primary animate-pulse'
                  : ''
              } ${i > 0 ? 'ml-px' : ''}`}
            />
          ))}
        </div>
        <div className="flex justify-between px-0.5">
          {STAGES.map((label, i) => (
            <span
              key={i}
              className={`text-[10px] transition-colors ${
                i <= activeStage ? 'text-muted-foreground' : 'text-muted-foreground/40'
              } ${i === activeStage && isRunning ? 'text-primary' : ''}`}
            >
              {label}
            </span>
          ))}
        </div>
      </div>

      {/* Collapsible log output */}
      <div className="overflow-hidden border border-border bg-[#060a0d]">
        <button
          onClick={onToggleCollapse}
          className="flex w-full items-center gap-2 border-b border-slate-800 px-3 py-2 text-xs text-slate-400 transition-colors hover:bg-slate-900 hover:text-slate-100"
        >
          {collapsed ? <ChevronRight className="w-3.5 h-3.5" /> : <ChevronDown className="w-3.5 h-3.5" />}
          <Terminal className="w-3.5 h-3.5" />
          <span>Scan Output</span>
          {lines.length > 0 && (
            <span className="text-muted-foreground/60 ml-auto">{lines.length} lines</span>
          )}
        </button>

        {!collapsed && (
          <div
            ref={logRef}
            className="max-h-72 overflow-y-auto px-3 py-3 font-mono text-xs leading-relaxed"
          >
            {lines.length === 0 ? (
              <div className="flex items-center gap-2 py-2 text-slate-500">
                <span className="animate-pulse">Initializing scan...</span>
              </div>
            ) : (
              lines.map((line, i) => (
                <div key={i} className={lineColor(line)}>
                  {line}
                </div>
              ))
            )}
          </div>
        )}
      </div>
    </div>
  )
}

function lineColor(line: string): string {
  const l = line.toLowerCase()
  if (l.includes('[vuln') || l.includes('critical')) return 'text-red-400'
  if (l.includes('[risk') || l.includes('weakpass')) return 'text-orange-400'
  if (l.includes('[ai') || l.includes('verified') || l.includes('sniper') || l.includes('[deep')) return 'text-purple-400'
  if (l.includes('[fingerprint')) return 'text-warning'
  if (l.includes('[web') || l.includes('[service')) return 'text-green-400'
  if (l.includes('[summary') || l.includes('completed')) return 'text-cyan-400'
  if (l.includes('error') || l.includes('failed')) return 'text-red-500'
  return 'text-slate-300'
}
