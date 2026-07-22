import { useState, useEffect, useCallback, useMemo, lazy, Suspense, type ReactNode } from 'react'
import { useTranslation } from 'react-i18next'
import { Box, Menu, Monitor, Network, Settings } from 'lucide-react'
import LanguageToggle from './components/LanguageToggle'
import SessionList from './components/SessionList'
import ChatPanel from './components/ChatPanel'
import ConfigPanel from './components/ConfigPanel'
import AgentPanel from './components/AgentPanel'
import AssetPanel, { assetMentionables } from './components/AssetPanel'
import AssetMentionPicker from './components/AssetMentionPicker'
import LLMHealth from './components/LLMHealth'
import QuickConnect from './components/QuickConnect'
import BrandLogo from './components/brand/BrandLogo'
// Lazy: the agent terminal drags in @xterm (~its own chunk) but only renders
// when a node's console is opened — keep it out of the first-paint bundle.
const AgentTerminal = lazy(() => import('./components/terminal'))
const IOAConsole = lazy(() => import('./components/IOAConsole'))
import { Button, Select, SelectContent, SelectItem, SelectTrigger, SelectValue, ThemeToggle, Tooltip, TooltipContent, TooltipProvider, TooltipTrigger, useConfirm } from '@cyber/ui'
import { ThemeProvider, useTheme } from '@cyber/theme'
import { activateLLMProfile, getConfigStatus, getStatus, listSCONodes } from './api'
import type { LLMProfileStatus, ServerStatus } from './api'
import type { SCONode } from '@cyber/cstx-easm'
import { useChatSession, agentNodeKey } from './hooks/useChatSession'
import { usePolling } from './hooks/usePolling'
import { isSessionAgentOnline } from './lib/session-agent'
import type { IOAConsoleTarget } from './lib/ioa-navigation'
import { cn } from '@cyber/theme'

const sidebarStorageKey = 'aiscan-sidebar-open'

const EMPTY_SEED = { text: '', nonce: 0 }

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
  const [llmProfiles, setLLMProfiles] = useState<LLMProfileStatus[]>([])
  const [activeLLMProfile, setActiveLLMProfile] = useState('')
  const [switchingLLM, setSwitchingLLM] = useState(false)
  const [configOpen, setConfigOpen] = useState(false)
  const [agentPanelOpen, setAgentPanelOpen] = useState(false)
  const [assetPanelOpen, setAssetPanelOpen] = useState(false)
  const [ioaConsoleOpen, setIOAConsoleOpen] = useState(false)
  const [ioaConsoleTarget, setIOAConsoleTarget] = useState<IOAConsoleTarget | null>(null)
  const [agentPanelFocusID, setAgentPanelFocusID] = useState<string | null>(null)
  const [sidebarOpen, setSidebarOpen] = useState(getInitialSidebarOpen)
  // Bumped after a settings save so the header LLM health dot re-probes.
  const [healthNonce, setHealthNonce] = useState(0)
  // Track the terminal target by the node's STABLE key, not its transient agent
  // id: the hub mints a fresh id on every reconnect, so keying on id would drop
  // the terminal (and never restore it) when a node bounces to reload config.
  const [terminalNodeKey, setTerminalNodeKey] = useState<string | null>(null)

  const openIOAConsole = useCallback((target?: IOAConsoleTarget) => {
    setIOAConsoleTarget(target ?? null)
    setIOAConsoleOpen(true)
  }, [])

  const refreshStatus = useCallback(async () => {
    const [statusResult, configResult] = await Promise.allSettled([getStatus(), getConfigStatus()])
    if (statusResult.status === 'fulfilled') setServerStatus(statusResult.value)
    if (configResult.status === 'fulfilled') {
      const profiles = configResult.value.llm.profiles ?? []
      setLLMProfiles(profiles)
      setActiveLLMProfile(configResult.value.llm.active_profile || profiles[0]?.id || '')
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

  // SCO nodes for @-mention in chat input
  const [scoNodes, setScoNodes] = useState<SCONode[]>([])
  const [composerSeed, setComposerSeed] = useState(EMPTY_SEED)

  const refreshSCONodes = useCallback(async () => {
    try {
      const data = await listSCONodes({ limit: 2000 })
      setScoNodes(data)
    } catch { /* non-critical */ }
  }, [])

  useEffect(() => { void refreshSCONodes() }, [refreshSCONodes])
  // Refresh mentionables when scans finish (timeline changes often signal new results)
  useEffect(() => { void refreshSCONodes() }, [chat.timeline.length, refreshSCONodes])

  const mentionables = useMemo(() => assetMentionables(scoNodes), [scoNodes])

  const handleAssetSendToChat = useCallback((text: string) => {
    setComposerSeed({ text, nonce: Date.now() })
  }, [])

  const renderMentionPopup = useMemo(() => {
    if (scoNodes.length === 0) return undefined
    return (api: { query: string; onSelect: (targets: string[]) => void; onDismiss: () => void }) => (
      <AssetMentionPicker {...api} nodes={scoNodes} />
    )
  }, [scoNodes])

  const terminalAgent = terminalNodeKey ? chat.agents.find((a) => agentNodeKey(a) === terminalNodeKey) ?? null : null

  const model = serverStatus?.llm_model || chat.agents.find((a) => a.status?.model)?.status?.model || 'cortex'

  const handleSwitchLLM = useCallback(async (profileID: string) => {
    if (!profileID || profileID === activeLLMProfile) return
    setSwitchingLLM(true)
    try {
      const next = await activateLLMProfile(profileID)
      setLLMProfiles(next.llm.profiles ?? [])
      setActiveLLMProfile(next.llm.active_profile || profileID)
      await refreshStatus()
      setHealthNonce((nonce) => nonce + 1)
    } catch {
      setConfigOpen(true)
    } finally {
      setSwitchingLLM(false)
    }
  }, [activeLLMProfile, refreshStatus])
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
            <LLMProfileSwitcher
              profiles={llmProfiles}
              activeProfileID={activeLLMProfile}
              fallbackModel={model}
              disabled={switchingLLM}
              onChange={handleSwitchLLM}
            />
            <LLMHealth onOpenSettings={() => setConfigOpen(true)} reloadSignal={healthNonce} />
          </div>
          <div className="flex items-center gap-2">
            <AssetPoolButton count={scoNodes.length} onClick={() => setAssetPanelOpen(true)} />
            <IOAConsoleButton onClick={() => openIOAConsole()} />
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
              aopEvents={chat.aopEvents}
              scanResults={chat.scanResults}
              isThinking={chat.isThinking}
              isBusy={chat.busy}
              error={chat.error}
              activeSessionID={chat.activeSessionID}
              hasActiveSession={chat.activeSessionID !== null}
              agentOffline={activeAgentOffline}
              agentName={activeSession?.agent_name}
              agents={chat.agents.map((a) => ({ id: a.id, name: a.name }))}
              onCreateSession={handleCreateSession}
              onOpenTerminal={handleOpenTerminal}
              onOpenIOA={openIOAConsole}
              mentionables={mentionables}
              renderMentionPopup={renderMentionPopup}
              injectText={composerSeed}
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

      <AssetPanel
        open={assetPanelOpen}
        onClose={() => setAssetPanelOpen(false)}
        onSendToChat={handleAssetSendToChat}
      />

      {ioaConsoleOpen && (
        <Suspense fallback={null}>
          <IOAConsole
            open={ioaConsoleOpen}
            initialSpaceID={ioaConsoleTarget?.spaceID}
            initialMessageID={ioaConsoleTarget?.messageID}
            onClose={() => {
              setIOAConsoleOpen(false)
              setIOAConsoleTarget(null)
            }}
          />
        </Suspense>
      )}
    </TooltipProvider>
    </ThemeProvider>
  )
}

function ConnectedThemeToggle() {
  const { isDark, toggle } = useTheme()
  return <ThemeToggle isDark={isDark} onToggle={toggle} size="sm" />
}

function LLMProfileSwitcher({
  profiles,
  activeProfileID,
  fallbackModel,
  disabled,
  onChange,
}: {
  profiles: LLMProfileStatus[]
  activeProfileID: string
  fallbackModel: string
  disabled: boolean
  onChange: (profileID: string) => void
}) {
  if (profiles.length === 0) {
    return <span className="ml-1 hidden font-mono text-[10px] uppercase tracking-wider text-muted-foreground sm:inline">{fallbackModel}</span>
  }

  return (
    <Select value={activeProfileID || profiles[0].id} onValueChange={onChange} disabled={disabled}>
      <SelectTrigger
        aria-label="Switch LLM profile"
        className="ml-1 hidden h-7 w-auto min-w-[120px] max-w-[230px] gap-1 border-0 bg-transparent px-2 font-mono text-[10px] text-muted-foreground shadow-none hover:bg-muted/60 hover:text-foreground sm:flex"
      >
        <SelectValue placeholder={fallbackModel} />
      </SelectTrigger>
      <SelectContent align="start">
        {profiles.map(profile => (
          <SelectItem key={profile.id} value={profile.id}>
            {profile.name || profile.model || profile.provider}
            {profile.model && profile.name !== profile.model ? ` · ${profile.model}` : ''}
          </SelectItem>
        ))}
      </SelectContent>
    </Select>
  )
}

function AssetPoolButton({ count, onClick }: { count: number; onClick: () => void }) {
  const { t } = useTranslation('assets')
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
          aria-label={t('openAssets')}
          className={cn(
            'h-7 shrink-0 cursor-pointer gap-1.5 rounded-md border hover:opacity-80',
            active
              ? 'border-primary/30'
              : 'border-border bg-secondary/50 text-muted-foreground hover:bg-secondary/50 hover:text-muted-foreground',
          )}
        >
          <Box className="h-3 w-3" aria-hidden="true" />
          <span className="font-mono" aria-hidden="true">{count}</span>
        </Button>
      </TooltipTrigger>
      <TooltipContent>{t('openAssets')}</TooltipContent>
    </Tooltip>
  )
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

function IOAConsoleButton({ onClick }: { onClick: () => void }) {
  const { t } = useTranslation('ioa')
  return (
    <Tooltip>
      <TooltipTrigger asChild>
        <Button
          type="button"
          variant="ghost"
          size="xs"
          onClick={onClick}
          aria-label={t('openConsole')}
          className="h-7 shrink-0 cursor-pointer gap-1.5 rounded-md border border-border bg-secondary/50 text-muted-foreground hover:border-primary/30 hover:bg-primary/10 hover:text-primary"
        >
          <Network className="h-3 w-3" aria-hidden="true" />
          <span className="hidden font-mono text-[10px] font-semibold sm:inline" aria-hidden="true">IOA</span>
        </Button>
      </TooltipTrigger>
      <TooltipContent>{t('openConsole')}</TooltipContent>
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
