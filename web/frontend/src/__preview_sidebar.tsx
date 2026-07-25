// THROWAWAY preview harness for visual QA of the sidebar redesign. Not imported
// by the app; loaded only by /preview.html under `vite dev`. Delete after QA.
import React from 'react'
import ReactDOM from 'react-dom/client'
import i18n from './i18n'
import './index.css'
import { ThemeProvider } from '@cyber/theme'
import { TooltipProvider } from '@cyber/ui'
import SessionList from './components/SessionList'
import type { AgentInfo, ChatSession } from './api'

const params = new URLSearchParams(location.search)
const theme = params.get('theme') === 'dark' ? 'dark' : 'light'
const lang = params.get('lang') === 'en' ? 'en' : 'zh'
document.documentElement.lang = lang
window.localStorage.setItem('aiscan-locale', lang)
void i18n.changeLanguage(lang)

const agents: AgentInfo[] = [
  {
    id: 'a1', name: 'recon-01', busy: true, connected_at: '',
    node: { id: 'recon-01', authority: 'https://ioa.example' },
    status: { provider: 'anthropic', model: 'claude-opus-4-8', bound: true },
    stats: { current_tool: 'gogo', current_detail: '10.0.0.0/24' } as never,
  },
  {
    id: 'a2', name: 'osint-tokyo', busy: false, connected_at: '',
    node: { id: 'osint-tokyo', authority: 'https://ioa.example' },
    status: { provider: 'openai', model: 'gpt-5', bound: true },
  },
]
const sessions: ChatSession[] = [
  { id: 's1', agent_id: 'a1', agent_name: 'recon-01', title: '内网横向探测', status: 'active', created_at: '2026-07-06T00:00:00Z', updated_at: '2026-07-06T10:00:00Z' },
  { id: 's2', agent_id: 'a1', agent_name: 'recon-01', title: '子域接管复测', status: 'active', created_at: '2026-07-05T00:00:00Z', updated_at: '2026-07-05T09:00:00Z' },
  { id: 's3', agent_id: 'a2', agent_name: 'osint-tokyo', title: 'API 密钥泄露排查', status: 'active', created_at: '2026-07-04T00:00:00Z', updated_at: '2026-07-04T08:00:00Z' },
]

function Harness() {
  const [open, setOpen] = React.useState(true)
  return (
    <ThemeProvider initial={theme} storageKey="aiscan-theme-preview" className="aspect-theme-root h-full text-foreground font-sans antialiased">
      <TooltipProvider>
        <div className="flex h-screen bg-background">
          <SessionList
            open={open}
            onToggle={() => setOpen((o) => !o)}
            agents={agents}
            sessions={sessions}
            activeSessionID="s1"
            selectedAgentID="a1"
            terminalAgentID={null}
            onSelectAgent={() => {}}
            onSelectSession={() => {}}
            onCreateSession={() => {}}
            onDeleteSession={() => {}}
            onOpenTerminal={() => {}}
          />
          <div className="flex-1" />
        </div>
      </TooltipProvider>
    </ThemeProvider>
  )
}

ReactDOM.createRoot(document.getElementById('root')!).render(<Harness />)
