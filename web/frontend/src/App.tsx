import { useState, useEffect, useCallback, type ReactNode } from 'react'
import { AlertTriangle, CheckCircle2, MessageSquare, Monitor, Settings } from 'lucide-react'
import SessionList from './components/SessionList'
import ChatPanel from './components/ChatPanel'
import DetailPanel from './components/DetailPanel'
import ScanWorkspace from './components/ScanWorkspace'
import ConfigPanel from './components/ConfigPanel'
import AgentPanel from './components/AgentPanel'
import AgentTerminal from './components/terminal'
import { ThemeToggle } from '@aspect/ui'
import { ThemeProvider, useTheme } from '@aspect/theme'
import { getStatus } from './api'
import type { ScanJob, ServerStatus } from './api'
import { useScanSession } from './hooks/useScanSession'
import { useChatSession } from './hooks/useChatSession'
import { parseRoute, sessionRoutePath } from './lib/scan-route'
import { TooltipProvider } from '@aspect/ui'
import { cn } from '@aspect/theme'

const sidebarStorageKey = 'aiscan-sidebar-open'

type AppView = 'chat' | 'scan'

function getInitialSidebarOpen() {
  if (typeof window === 'undefined') return true
  if (window.matchMedia('(max-width: 767px)').matches) return false
  const stored = window.localStorage.getItem(sidebarStorageKey)
  if (stored === 'true' || stored === 'false') return stored === 'true'
  return window.matchMedia('(min-width: 1024px)').matches
}

function getInitialView(): AppView {
  if (typeof window === 'undefined') return 'chat'
  return parseRoute(window.location.pathname).kind === 'scan' ? 'scan' : 'chat'
}

export default function App() {
  const chat = useChatSession()
  const scanSession = useScanSession()
  const [analysisAvailable, setAnalysisAvailable] = useState(true)
  const [serverStatus, setServerStatus] = useState<ServerStatus | null>(null)
  const [configOpen, setConfigOpen] = useState(false)
  const [agentPanelOpen, setAgentPanelOpen] = useState(false)
  const [sidebarOpen, setSidebarOpen] = useState(getInitialSidebarOpen)
  const [detailOpen, setDetailOpen] = useState(true)
  const [terminalAgentID, setTerminalAgentID] = useState<string | null>(null)
  const [view, setView] = useState<AppView>(getInitialView)

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

  useEffect(() => {
    const syncViewFromRoute = () => {
      const route = parseRoute(window.location.pathname)
      setView(route.kind === 'scan' ? 'scan' : 'chat')
      if (route.kind !== 'scan') {
        setAgentPanelOpen(false)
      }
    }
    syncViewFromRoute()
    window.addEventListener('popstate', syncViewFromRoute)
    return () => window.removeEventListener('popstate', syncViewFromRoute)
  }, [])

  const detailResult = chat.detailScanID ? chat.scanResults.get(chat.detailScanID) ?? null : null
  const showDetail = detailOpen && !!chat.detailScanID && !!detailResult
  const terminalAgent = terminalAgentID ? chat.agents.find((a) => a.id === terminalAgentID) ?? null : null

  function handleOpenTerminal(agentID: string) {
    setTerminalAgentID(agentID)
    chat.selectAgent(agentID)
  }

  function handleSelectSession(id: string) {
    setTerminalAgentID(null)
    chat.selectSession(id)
  }

  function handleCreateSession(agentID: string) {
    setTerminalAgentID(null)
    chat.createSession(agentID)
  }

  function handleOpenScanWorkspace() {
    setTerminalAgentID(null)
    setView('scan')
  }

  function handleOpenChatWorkspace() {
    setTerminalAgentID(null)
    setView('chat')
    const path = chat.activeSessionID ? sessionRoutePath(chat.activeSessionID) : '/'
    window.history.pushState({}, '', path)
  }

  function handleSelectScan(scan: ScanJob) {
    setTerminalAgentID(null)
    setView('scan')
    scanSession.selectScan(scan)
  }

  return (
    <ThemeProvider initial="dark" storageKey="aiscan-theme">
    <TooltipProvider delayDuration={300}>
      <div className="flex h-screen bg-background">
        {view === 'scan' ? (
          <>
            <main className="flex min-h-0 min-w-0 flex-1">
              <ScanWorkspace
                scans={scanSession.scans}
                activeScan={scanSession.activeScan}
                lines={scanSession.progressLines}
                report={scanSession.report}
                result={scanSession.result}
                scanning={scanSession.scanning}
                error={scanSession.error}
                logCollapsed={scanSession.logCollapsed}
                analysisAvailable={analysisAvailable}
                onSubmit={scanSession.submit}
                onSelectScan={handleSelectScan}
                onRefreshScans={scanSession.refreshScans}
                onToggleLog={scanSession.toggleLog}
                onClearError={scanSession.clearError}
                status={<StatusPill active={analysisAvailable} />}
                actions={
                  <>
                    <HeaderIconButton label="Open chat workspace" onClick={handleOpenChatWorkspace}>
                      <MessageSquare className="h-3.5 w-3.5" />
                    </HeaderIconButton>
                    <ScanAgentsButton count={serverStatus?.agents ?? chat.agents.length} onClick={() => setAgentPanelOpen(true)} />
                    <HeaderIconButton label="Open settings" onClick={() => setConfigOpen(true)}>
                      <Settings className="h-3.5 w-3.5" />
                    </HeaderIconButton>
                    <ConnectedThemeToggle />
                  </>
                }
              />
            </main>

            <AgentPanel
              open={agentPanelOpen}
              onClose={() => setAgentPanelOpen(false)}
            />
          </>
        ) : (
          <>
            <SessionList
              open={sidebarOpen}
              onToggle={() => setSidebarOpen(!sidebarOpen)}
              agents={chat.agents}
              sessions={chat.sessions}
              activeSessionID={chat.activeSessionID}
              selectedAgentID={chat.selectedAgentID}
              terminalAgentID={terminalAgentID}
              onSelectAgent={chat.selectAgent}
              onSelectSession={handleSelectSession}
              onCreateSession={handleCreateSession}
              onDeleteSession={chat.deleteSession}
              onOpenTerminal={handleOpenTerminal}
            />

            {terminalAgent ? (
              <section className="relative min-h-0 min-w-0 flex-1">
                <div className="absolute inset-0 flex flex-col">
                  <AgentTerminal agent={terminalAgent} />
                </div>
              </section>
            ) : (
              <>
                <ChatPanel
                  timeline={chat.timeline}
                  streamingText={chat.streamingText}
                  streamingAgent={chat.streamingAgent}
                  scanResults={chat.scanResults}
                  isThinking={chat.isThinking}
                  isBusy={chat.busy}
                  error={chat.error}
                  hasActiveSession={chat.activeSessionID !== null}
                  onSend={chat.sendMessage}
                  onPause={chat.cancelMessage}
                  onClearError={chat.clearError}
                  onShowScanDetail={(scanID) => {
                    chat.showScanDetail(scanID)
                    setDetailOpen(true)
                  }}
                  detailOpen={showDetail}
                  onToggleDetail={() => setDetailOpen(!detailOpen)}
                  onOpenConfig={() => setConfigOpen(true)}
                  onOpenScan={handleOpenScanWorkspace}
                  agentsPill={<AgentsPill count={chat.agents.length} />}
                />

                <div
                  className={cn(
                    'shrink-0 transition-[width,opacity] duration-200 ease-in-out overflow-hidden',
                    showDetail ? 'w-full lg:w-[28rem] opacity-100' : 'w-0 opacity-0',
                  )}
                >
                  {showDetail && (
                    <DetailPanel
                      scanID={chat.detailScanID!}
                      result={detailResult}
                      onClose={() => setDetailOpen(false)}
                    />
                  )}
                </div>
              </>
            )}
          </>
        )}
      </div>

      <ConfigPanel
        open={configOpen}
        status={serverStatus}
        onClose={() => setConfigOpen(false)}
        onSaved={refreshStatus}
      />
    </TooltipProvider>
    </ThemeProvider>
  )
}

function ConnectedThemeToggle() {
  const { isDark, toggle } = useTheme()
  return <ThemeToggle isDark={isDark} onToggle={toggle} size="sm" />
}

function ScanAgentsButton({ count, onClick }: { count: number; onClick: () => void }) {
  const active = count > 0
  return (
    <button
      type="button"
      onClick={onClick}
      title={active ? `${count} agent(s) connected` : 'No agents connected'}
      className={cn(
        'inline-flex h-7 shrink-0 cursor-pointer items-center gap-1.5 rounded-md border px-2 text-[10px] font-medium transition-colors hover:opacity-80',
        active
          ? 'border-primary/30 bg-primary/10 text-primary'
          : 'border-yellow-400/30 bg-yellow-400/10 text-yellow-700 dark:text-yellow-300',
      )}
    >
      <Monitor className="h-3 w-3" />
      <span className="font-mono">{count}</span>
    </button>
  )
}

function HeaderIconButton({ children, label, onClick }: { children: ReactNode; label: string; onClick: () => void }) {
  return (
    <button
      type="button"
      aria-label={label}
      title={label}
      onClick={onClick}
      className="inline-flex h-7 w-7 items-center justify-center rounded-md text-muted-foreground hover:bg-accent hover:text-foreground"
    >
      {children}
    </button>
  )
}

function AgentsPill({ count }: { count: number }) {
  const active = count > 0
  return (
    <span
      className={cn(
        'h-7 shrink-0 items-center gap-1.5 rounded-md border px-2 text-[10px] font-medium inline-flex',
        active
          ? 'border-primary/30 bg-primary/10 text-primary'
          : 'border-yellow-400/30 bg-yellow-400/10 text-yellow-700 dark:text-yellow-300',
      )}
    >
      <Monitor className="h-3 w-3" />
      <span className="font-mono">{count}</span>
    </span>
  )
}

function StatusPill({ active }: { active: boolean }) {
  return (
    <span
      title={active ? 'LLM Ready' : 'LLM Offline'}
      className={cn(
        'hidden h-7 shrink-0 items-center gap-1.5 rounded-md border px-2 text-[10px] font-medium lg:inline-flex',
        active
          ? 'border-primary/30 bg-primary/10 text-primary'
          : 'border-yellow-400/30 bg-yellow-400/10 text-yellow-700 dark:text-yellow-300',
      )}
    >
      {active ? <CheckCircle2 className="h-3 w-3" /> : <AlertTriangle className="h-3 w-3" />}
      {active ? 'LLM Ready' : 'LLM Offline'}
    </span>
  )
}
