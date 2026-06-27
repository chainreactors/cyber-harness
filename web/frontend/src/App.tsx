import { useState, useEffect, useCallback, type ReactNode } from 'react'
import { AlertTriangle, CheckCircle2, History, Monitor, Settings, Shield, X } from 'lucide-react'
import Sidebar from './components/Sidebar'
import ScanForm from './components/ScanForm'
import ScanView from './components/ScanView'
import LLMConfigPanel from './components/LLMConfigPanel'
import AgentPanel from './components/AgentPanel'
import ThemeToggle from './components/ThemeToggle'
import { getStatus } from './api'
import type { ServerStatus } from './api'
import { useScanSession } from './hooks/useScanSession'
import { Button, TooltipProvider } from '@aspect/ui'
import { cn } from '@aspect/theme'

const sidebarStorageKey = 'aiscan-sidebar-open'

function getInitialSidebarOpen() {
  if (typeof window === 'undefined') {
    return true
  }
  if (window.matchMedia('(max-width: 767px)').matches) {
    return false
  }
  const stored = window.localStorage.getItem(sidebarStorageKey)
  if (stored === 'true' || stored === 'false') {
    return stored === 'true'
  }
  return window.matchMedia('(min-width: 1024px)').matches
}

export default function App() {
  const scanSession = useScanSession()
  const [analysisAvailable, setAnalysisAvailable] = useState(true)
  const [serverStatus, setServerStatus] = useState<ServerStatus | null>(null)
  const [llmConfigOpen, setLLMConfigOpen] = useState(false)
  const [agentPanelOpen, setAgentPanelOpen] = useState(false)
  const [sidebarOpen, setSidebarOpen] = useState(getInitialSidebarOpen)

  const refreshStatus = useCallback(async () => {
    try {
      const status = await getStatus()
      setServerStatus(status)
      setAnalysisAvailable(status.llm_available)
    } catch {
      setAnalysisAvailable(true)
    }
  }, [])

  useEffect(() => {
    refreshStatus()
  }, [refreshStatus])

  useEffect(() => {
    window.localStorage.setItem(sidebarStorageKey, String(sidebarOpen))
  }, [sidebarOpen])

  return (
    <TooltipProvider delayDuration={300}>
    <div className="flex min-h-screen bg-background">
      <Sidebar
        open={sidebarOpen}
        onToggle={() => setSidebarOpen(!sidebarOpen)}
        scans={scanSession.scans}
        activeId={scanSession.activeScan?.id}
        onSelectScan={scanSession.selectScan}
      />

      <main className="flex-1 flex flex-col min-w-0">
        {/* Header with form */}
        <div className="sticky top-0 z-20 border-b border-border bg-card/85 p-3 shadow-sm backdrop-blur-sm sm:p-4">
          <ScanForm
            onSubmit={scanSession.submit}
            disabled={scanSession.scanning}
            analysisAvailable={analysisAvailable}
            status={<StatusPill active={analysisAvailable} />}
            actions={
              <>
                <AgentsPill count={serverStatus?.agents ?? 0} onClick={() => setAgentPanelOpen(true)} />
                <Button
                  type="button"
                  variant="outline"
                  onClick={() => setLLMConfigOpen(true)}
                  className="h-10 w-10 shrink-0 px-0 sm:w-auto sm:px-3"
                  aria-label="Open LLM configuration"
                >
                  <Settings className="h-4 w-4" />
                  <span className="hidden sm:inline">LLM</span>
                </Button>
                <ThemeToggle />
              </>
            }
          />
        </div>

        {/* Error */}
        {scanSession.error && (
          <div
            role="alert"
            className="mx-4 mt-3 flex items-start gap-2 rounded-md border border-destructive/30 bg-destructive/10 px-3 py-2 text-sm text-destructive animate-fade-in"
          >
            <AlertTriangle className="mt-0.5 h-4 w-4 shrink-0" />
            <span className="min-w-0 flex-1 break-words">{scanSession.error}</span>
            <button
              type="button"
              aria-label="Dismiss error"
              onClick={scanSession.clearError}
              className="rounded p-0.5 text-destructive/70 hover:bg-destructive/10 hover:text-destructive"
            >
              <X className="h-4 w-4" />
            </button>
          </div>
        )}

        {/* Content */}
        {scanSession.activeScan ? (
          <div className="flex-1 p-4 overflow-auto">
            <ScanView
              scan={scanSession.activeScan}
              lines={scanSession.progressLines}
              report={scanSession.report}
              result={scanSession.result}
              logCollapsed={scanSession.logCollapsed}
              onToggleLog={scanSession.toggleLog}
            />
          </div>
        ) : (
          <div className="flex-1 flex items-center justify-center">
            <div className="text-center space-y-4">
              <Shield className="w-16 h-16 mx-auto text-muted-foreground/10" strokeWidth={1} />
              <div className="space-y-1">
                <p className="text-sm font-medium text-foreground">No active scan</p>
                <p className="text-xs text-muted-foreground">Ready for a target</p>
              </div>
              <div className="flex flex-wrap justify-center gap-2">
                <EmptyStateMetric icon={<History className="h-3.5 w-3.5" />} label="History" value={scanSession.scans.length} />
                <EmptyStateMetric
                  icon={<Monitor className="h-3.5 w-3.5" />}
                  label="Agents"
                  value={serverStatus?.agents ?? 0}
                  tone={(serverStatus?.agents ?? 0) > 0 ? 'ready' : 'warning'}
                />
                <EmptyStateMetric
                  icon={analysisAvailable ? <CheckCircle2 className="h-3.5 w-3.5" /> : <AlertTriangle className="h-3.5 w-3.5" />}
                  label="LLM"
                  value={analysisAvailable ? 'Ready' : 'Offline'}
                  tone={analysisAvailable ? 'ready' : 'warning'}
                />
                <EmptyStateMetric
                  icon={<CheckCircle2 className="h-3.5 w-3.5" />}
                  label="Config"
                  value={serverStatus?.config_loaded ? 'Loaded' : 'Default'}
                />
              </div>
            </div>
          </div>
        )}
      </main>
      <LLMConfigPanel
        open={llmConfigOpen}
        status={serverStatus}
        onClose={() => setLLMConfigOpen(false)}
        onSaved={refreshStatus}
      />
      <AgentPanel
        open={agentPanelOpen}
        onClose={() => setAgentPanelOpen(false)}
      />
    </div>
    </TooltipProvider>
  )
}

function AgentsPill({ count, onClick }: { count: number; onClick: () => void }) {
  const active = count > 0
  return (
    <button
      type="button"
      onClick={onClick}
      title={active ? `${count} agent(s) connected` : 'No agents connected'}
      className={cn(
        'h-10 shrink-0 items-center gap-2 rounded-md border px-3 text-xs font-medium inline-flex cursor-pointer transition-colors hover:opacity-80',
        active
          ? 'border-cyber-400/30 bg-cyber-400/10 text-cyber-700 dark:text-cyber-300'
          : 'border-yellow-400/30 bg-yellow-400/10 text-yellow-700 dark:text-yellow-300',
      )}
    >
      <Monitor className="h-3.5 w-3.5" />
      <span className="hidden sm:inline">Agents</span>
      <span className="font-mono">{count}</span>
    </button>
  )
}

function StatusPill({ active }: { active: boolean }) {
  return (
    <span
      title={active ? 'LLM Ready' : 'LLM Offline'}
      className={cn(
        'hidden h-10 shrink-0 items-center gap-2 rounded-md border px-3 text-xs font-medium lg:inline-flex',
        active
          ? 'border-cyber-400/30 bg-cyber-400/10 text-cyber-700 dark:text-cyber-300'
          : 'border-yellow-400/30 bg-yellow-400/10 text-yellow-700 dark:text-yellow-300',
      )}
    >
      {active ? <CheckCircle2 className="h-3.5 w-3.5" /> : <AlertTriangle className="h-3.5 w-3.5" />}
      {active ? 'LLM Ready' : 'LLM Offline'}
    </span>
  )
}

function EmptyStateMetric({
  icon,
  label,
  value,
  tone = 'muted',
}: {
  icon: ReactNode
  label: string
  value: string | number
  tone?: 'muted' | 'ready' | 'warning'
}) {
  return (
    <div
      className={cn(
        'inline-flex items-center gap-2 rounded-md border px-2.5 py-1.5 text-xs',
        tone === 'ready' && 'border-cyber-400/25 bg-cyber-400/10 text-cyber-700 dark:text-cyber-300',
        tone === 'warning' && 'border-yellow-400/25 bg-yellow-400/10 text-yellow-700 dark:text-yellow-300',
        tone === 'muted' && 'border-border bg-card/60 text-muted-foreground',
      )}
    >
      {icon}
      <span className="text-muted-foreground">{label}</span>
      <span className="font-mono text-foreground">{value}</span>
    </div>
  )
}
