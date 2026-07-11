import { useState, useEffect, useCallback, lazy, Suspense, type ReactNode } from 'react'
import { useTranslation } from 'react-i18next'
import { Menu, Monitor, Settings } from 'lucide-react'
import LanguageToggle from './components/LanguageToggle'
import SessionList from './components/SessionList'
import ChatPanel from './components/ChatPanel'
import ConfigPanel from './components/ConfigPanel'
import AgentPanel from './components/AgentPanel'
import LLMHealth from './components/LLMHealth'
import QuickConnect from './components/QuickConnect'
import BrandLogo from './components/brand/BrandLogo'
// Lazy: the agent terminal drags in @xterm (~its own chunk) but only renders
// when a node's console is opened — keep it out of the first-paint bundle.
const AgentTerminal = lazy(() => import('./components/terminal'))
import { Button, ThemeToggle, Tooltip, TooltipContent, TooltipProvider, TooltipTrigger, useConfirm } from '@aspect/ui'
import { ThemeProvider, useTheme } from '@aspect/theme'
import { getStatus } from './api'
import type { ServerStatus } from './api'
import { useChatSession, agentNodeKey } from './hooks/useChatSession'
import { usePolling } from './hooks/usePolling'
import { isSessionAgentOnline } from './lib/session-agent'
import { cn } from '@aspect/theme'

const sidebarStorageKey = 'aiscan-sidebar-open'

// Stable empty props so ChatPanel's memoized rows aren't re-created every render.
// The asset-pool @-mention source and composer seeding were scan-deck features;
// with the deck gone the chat composer just runs free-text.
const NO_MENTIONABLES: { target: string; label?: string; source?: string }[] = []
const NO_SEED = { text: '', nonce: 0 }

// Respect a previously-chosen theme on boot. ThemeProvider's own initializer is
// short-circuited by the `initial` prop (it returns `initial` before ever reading
// storage), so we read the persisted value here and feed it in as the initial —
// otherwise every reload snaps back to the light default.
function getInitialTheme(): 'light' | 'dark' {
  if (typeof window === 'undefined') return 'light'
  const v = window.localStorage.getItem('aiscan-theme')
  return v === 'dark' || v === 'light' ? v : 'light'
}

function getInitialSidebarOpen() {
  if (typeof window === 'undefined') return true
  if (window.matchMedia('(max-width: 767px)').matches) return false
  const stored = window.localStorage.getItem(sidebarStorageKey)
  if (stored === 'true' || stored === 'false') return stored === 'true'
  return window.matchMedia('(min-width: 1024px)').matches
}

export default function App() {
  const { t } = useTranslation('app')
  const { t: tc } = useTranslation('chat')
  const confirm = useConfirm()
  const chat = useChatSession()
  const [serverStatus, setServerStatus] = useState<ServerStatus | null>(null)
  const [configOpen, setConfigOpen] = useState(false)
  const [agentPanelOpen, setAgentPanelOpen] = useState(false)
  const [agentPanelFocusID, setAgentPanelFocusID] = useState<string | null>(null)
  const [sidebarOpen, setSidebarOpen] = useState(getInitialSidebarOpen)
  // Bumped after a settings save so the header LLM health dot re-probes.
  const [healthNonce, setHealthNonce] = useState(0)
  // Track the terminal target by the node's STABLE key, not its transient agent
  // id: the hub mints a fresh id on every reconnect, so keying on id would drop
  // the terminal (and never restore it) when a node bounces to reload config.
  const [terminalNodeKey, setTerminalNodeKey] = useState<string | null>(null)

  const refreshStatus = useCallback(async () => {
    try {
      setServerStatus(await getStatus())
    } catch {
      /* leave prior status; the settings panel surfaces connectivity */
    }
  }, [])

  useEffect(() => {
    refreshStatus()
  }, [refreshStatus])

  // Keep the header (model + agent count + health base) fresh without a reload.
  usePolling(refreshStatus, 30000)

  useEffect(() => {
    window.localStorage.setItem(sidebarStorageKey, String(sidebarOpen))
  }, [sidebarOpen])

  const terminalAgent = terminalNodeKey ? chat.agents.find((a) => agentNodeKey(a) === terminalNodeKey) ?? null : null

  const model = serverStatus?.llm_model || chat.agents.find((a) => a.identity?.model)?.identity?.model || 'cortex'
  const activeSession = chat.sessions.find((s) => s.id === chat.activeSessionID) || null
  // The open session's bound agent has dropped off the live roster (its node
  // exited / the hub restarted). The transcript still shows, but a new turn
  // can't be dispatched until it reconnects — surface that in the chat panel.
  const activeAgentOffline = !!activeSession && !isSessionAgentOnline(activeSession, chat.agents)

  // On phones the sidebar is an overlay drawer (see SessionList); entering a
  // conversation or terminal should dismiss it so the content isn't left covered.
  // No-op at md+ where the sidebar is a docked rail that shares the row.
  function closeSidebarOnMobile() {
    if (typeof window !== 'undefined' && window.matchMedia('(max-width: 767px)').matches) {
      setSidebarOpen(false)
    }
  }

  function handleOpenTerminal(agentID: string) {
    const a = chat.agents.find((x) => x.id === agentID)
    setTerminalNodeKey(a ? agentNodeKey(a) : agentID)
    chat.selectAgent(agentID)
    closeSidebarOnMobile()
  }

  function handleSelectSession(id: string) {
    setTerminalNodeKey(null)
    chat.selectSession(id)
    closeSidebarOnMobile()
  }

  function handleCreateSession(agentID: string) {
    setTerminalNodeKey(null)
    chat.createSession(agentID)
    closeSidebarOnMobile()
  }

  // Deleting a session also tears down its live subscription, so confirm first —
  // matches every other destructive action in the app (node / config).
  async function handleDeleteSession(id: string) {
    if (!(await confirm({ description: tc('deleteSessionConfirm'), destructive: true }))) return
    void chat.deleteSession(id)
  }

  // Agent node clicked (roster / terminal open) → open the agent console focused
  // on that node.
  function handleOpenNode(agentID: string) {
    setAgentPanelFocusID(agentID)
    setAgentPanelOpen(true)
  }

  function handleOpenAgentPanel() {
    setAgentPanelFocusID(null)
    setAgentPanelOpen(true)
  }

  return (
    <ThemeProvider initial={getInitialTheme()} storageKey="aiscan-theme" className="aspect-theme-root h-full text-foreground font-sans antialiased">
    <TooltipProvider delayDuration={300}>
      <div className="flex h-[100dvh] flex-col overflow-hidden">
        <header className="flex min-h-12 shrink-0 items-center justify-between gap-2 border-b border-border/60 px-3 pt-safe sm:px-4">
          <div className="flex min-w-0 items-center gap-2">
            {/* Phone-only drawer opener — the collapsed sidebar is hidden below md,
                so the session history opens from here (Doubao-style). */}
            <Button
              variant="ghost"
              size="icon-xs"
              onClick={() => setSidebarOpen(true)}
              aria-label={t('openSessions')}
              className="-ml-1 shrink-0 text-muted-foreground md:hidden"
            >
              <Menu className="h-4 w-4" />
            </Button>
            <BrandLogo size={22} />
            <span className="truncate text-sm font-semibold tracking-tight text-foreground">AIScan</span>
            <span className="ml-1 hidden font-mono text-[10px] uppercase tracking-wider text-muted-foreground sm:inline">{model}</span>
            <LLMHealth onOpenSettings={() => setConfigOpen(true)} reloadSignal={healthNonce} />
          </div>
          <div className="flex items-center gap-2">
            <AgentsButton count={chat.agents.length} onClick={handleOpenAgentPanel} />
            <QuickConnect ioaURL={serverStatus?.ioa_url} version={serverStatus?.version} />
            <HeaderIconButton label={t('openSettings')} onClick={() => setConfigOpen(true)}>
              <Settings className="h-3.5 w-3.5" />
            </HeaderIconButton>
            <LanguageToggle />
            <ConnectedThemeToggle />
          </div>
        </header>

        <div className="flex min-h-0 flex-1 overflow-hidden">
          <SessionList
            open={sidebarOpen}
            onToggle={() => setSidebarOpen(!sidebarOpen)}
            agents={chat.agents}
            sessions={chat.sessions}
            activeSessionID={chat.activeSessionID}
            selectedAgentID={chat.selectedAgentID}
            terminalAgentID={terminalAgent?.id ?? null}
            onSelectAgent={chat.selectAgent}
            onSelectSession={handleSelectSession}
            onCreateSession={handleCreateSession}
            onDeleteSession={handleDeleteSession}
            onOpenTerminal={handleOpenTerminal}
          />

          {terminalAgent ? (
            <section className="relative min-h-0 min-w-0 flex-1">
              <div className="absolute inset-0 flex flex-col">
                <Suspense fallback={<div className="flex-1" />}>
                  <AgentTerminal agent={terminalAgent} />
                </Suspense>
              </div>
            </section>
          ) : (
            <ChatPanel
              timeline={chat.timeline}
              scanResults={chat.scanResults}
              isThinking={chat.isThinking}
              isBusy={chat.busy}
              error={chat.error}
              activeSessionID={chat.activeSessionID}
              hasActiveSession={chat.activeSessionID !== null}
              agentOffline={activeAgentOffline}
              agentName={activeSession?.agent_name}
              mentionables={NO_MENTIONABLES}
              injectText={NO_SEED}
              onSend={chat.sendMessage}
              onPause={chat.cancelMessage}
              onClearError={chat.clearError}
            />
          )}
        </div>
      </div>

      <ConfigPanel
        open={configOpen}
        status={serverStatus}
        onClose={() => setConfigOpen(false)}
        onSaved={() => { refreshStatus(); setHealthNonce((n) => n + 1) }}
      />

      <AgentPanel
        open={agentPanelOpen}
        agents={chat.agents}
        focusAgentID={agentPanelFocusID ?? undefined}
        onClose={() => setAgentPanelOpen(false)}
      />
    </TooltipProvider>
    </ThemeProvider>
  )
}

function ConnectedThemeToggle() {
  const { isDark, toggle } = useTheme()
  return <ThemeToggle isDark={isDark} onToggle={toggle} size="sm" />
}

function AgentsButton({ count, onClick }: { count: number; onClick: () => void }) {
  const { t } = useTranslation('app')
  const active = count > 0
  return (
    <Tooltip>
      <TooltipTrigger asChild>
        <Button
          type="button"
          variant="ghost"
          size="xs"
          active={active}
          onClick={onClick}
          aria-label={active ? t('agentsConnected', { count }) : t('noAgents')}
          className={cn(
            'h-7 shrink-0 cursor-pointer gap-1.5 rounded-md border hover:opacity-80',
            // A connection count is neutral status, not an alert — keep warm hues for
            // severity only. Blue when connected, quiet neutral when none.
            active
              ? 'border-primary/30'
              : 'border-border bg-secondary/50 text-muted-foreground hover:bg-secondary/50 hover:text-muted-foreground',
          )}
        >
          <Monitor className="h-3 w-3" aria-hidden="true" />
          <span className="font-mono" aria-hidden="true">{count}</span>
        </Button>
      </TooltipTrigger>
      <TooltipContent>{active ? t('agentsConnected', { count }) : t('noAgents')}</TooltipContent>
    </Tooltip>
  )
}

function HeaderIconButton({ children, label, onClick, active }: { children: ReactNode; label: string; onClick: () => void; active?: boolean }) {
  return (
    <Tooltip>
      <TooltipTrigger asChild>
        <Button
          type="button"
          variant="ghost"
          size="icon-xs"
          active={active}
          aria-label={label}
          onClick={onClick}
          className={cn('hover:text-foreground', !active && 'text-muted-foreground')}
        >
          {children}
        </Button>
      </TooltipTrigger>
      <TooltipContent>{label}</TooltipContent>
    </Tooltip>
  )
}
