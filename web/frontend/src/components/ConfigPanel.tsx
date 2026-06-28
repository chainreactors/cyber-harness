import { useEffect, useState, type FormEvent, type ReactNode } from 'react'
import { CheckCircle, Settings, X } from 'lucide-react'
import { getConfigStatus, saveConfig } from '../api'
import type { ConfigStatus, DistributeConfig, ServerStatus } from '../api'
import { Button, Input, Select, SelectTrigger, SelectContent, SelectItem, SelectValue, Badge, Spinner } from '@aspect/ui'

interface ConfigPanelProps {
  open: boolean
  status: ServerStatus | null
  onClose: () => void
  onSaved: () => void
}

type TabKey = 'llm' | 'cyberhub' | 'recon' | 'scan' | 'search' | 'ioa' | 'agent'

const TABS: { key: TabKey; label: string }[] = [
  { key: 'llm', label: 'LLM' },
  { key: 'cyberhub', label: 'Cyberhub' },
  { key: 'recon', label: 'Recon' },
  { key: 'scan', label: 'Scan' },
  { key: 'search', label: 'Search' },
  { key: 'ioa', label: 'IOA' },
  { key: 'agent', label: 'Agent' },
]

function emptyForm(): DistributeConfig {
  return {
    llm: { provider: '', base_url: '', api_key: '', model: '', proxy: '' },
    cyberhub: { url: '', key: '', mode: '', proxy: '' },
    recon: { fofa_email: '', fofa_key: '', hunter_token: '', hunter_api_key: '', proxy: '' },
    scan: { verify: '', verify_timeout: 0 },
    search: { tavily_keys: '' },
    ioa: { url: '', token: '', node_name: '', space: '' },
    agent: { tools: [], timeout: 0, save_session: false },
  }
}

function statusToForm(cs: ConfigStatus): DistributeConfig {
  return {
    llm: { provider: cs.llm.provider, base_url: cs.llm.base_url, api_key: '', model: cs.llm.model, proxy: cs.llm.proxy },
    cyberhub: { url: cs.cyberhub.url, key: '', mode: cs.cyberhub.mode, proxy: cs.cyberhub.proxy },
    recon: { fofa_email: cs.recon.fofa_email, fofa_key: '', hunter_token: '', hunter_api_key: '', proxy: cs.recon.proxy, limit: cs.recon.limit },
    scan: { ...cs.scan },
    search: { tavily_keys: '' },
    ioa: { url: cs.ioa.url, token: '', node_name: cs.ioa.node_name, space: cs.ioa.space },
    agent: { ...cs.agent },
  }
}

export default function ConfigPanel({ open, status, onClose, onSaved }: ConfigPanelProps) {
  const [cs, setCs] = useState<ConfigStatus | null>(null)
  const [form, setForm] = useState<DistributeConfig>(emptyForm)
  const [loading, setLoading] = useState(false)
  const [saving, setSaving] = useState(false)
  const [error, setError] = useState('')
  const [saved, setSaved] = useState(false)
  const [activeTab, setActiveTab] = useState<TabKey>('llm')

  useEffect(() => {
    if (!open) return
    setLoading(true)
    setError('')
    setSaved(false)
    getConfigStatus()
      .then((s) => { setCs(s); setForm(statusToForm(s)) })
      .catch((err: Error) => setError(err.message || 'Failed to load config'))
      .finally(() => setLoading(false))
  }, [open])

  if (!open) return null

  const handleSave = async (event: FormEvent) => {
    event.preventDefault()
    setSaving(true)
    setError('')
    setSaved(false)
    try {
      const next = await saveConfig(form)
      setCs(next)
      setForm(statusToForm(next))
      setSaved(true)
      onSaved()
    } catch (err: any) {
      setError(err.message || 'Failed to save config')
    } finally {
      setSaving(false)
    }
  }

  return (
    <div className="fixed inset-0 z-50 flex items-start justify-center bg-background/80 px-4 py-8 backdrop-blur-sm">
      <form onSubmit={handleSave} className="w-full max-w-3xl rounded-lg border border-border bg-card shadow-xl">
        <div className="flex items-center justify-between border-b border-border px-4 py-3">
          <div className="flex items-center gap-2">
            <Settings className="h-4 w-4 text-primary" />
            <div>
              <div className="text-sm font-medium text-foreground">Settings</div>
              <div className="text-xs text-muted-foreground">{cs?.config_path || status?.config_path || 'config.yaml'}</div>
            </div>
          </div>
          <button type="button" onClick={onClose} className="rounded-md p-1 text-muted-foreground hover:bg-accent hover:text-foreground">
            <X className="h-4 w-4" />
          </button>
        </div>

        <div className="flex gap-1 overflow-x-auto border-b border-border px-4 py-1">
          {TABS.map((tab) => (
            <button
              key={tab.key} type="button" onClick={() => setActiveTab(tab.key)}
              className={`whitespace-nowrap rounded-md px-3 py-1.5 text-xs font-medium transition-colors ${activeTab === tab.key ? 'bg-primary/10 text-primary' : 'text-muted-foreground hover:bg-accent hover:text-foreground'}`}
            >{tab.label}</button>
          ))}
        </div>

        <div className="p-4">
          <div className="mb-3 flex flex-wrap items-center gap-2 text-xs">
            <Badge variant={status?.llm_available ? 'success' : 'warning'} className="text-xs">{status?.llm_available ? 'LLM Ready' : 'LLM Offline'}</Badge>
            <Badge variant={cs?.config_loaded ? 'success' : 'warning'} className="text-xs">{cs?.config_loaded ? 'Config Loaded' : 'Config Missing'}</Badge>
          </div>

          {loading ? (
            <div className="flex h-48 items-center justify-center text-muted-foreground"><Spinner className="mr-2 h-4 w-4" />Loading</div>
          ) : (
            <div className="min-h-[12rem]">
              {activeTab === 'llm' && <LLMTab form={form} setForm={setForm} cs={cs} />}
              {activeTab === 'cyberhub' && <CyberhubTab form={form} setForm={setForm} cs={cs} />}
              {activeTab === 'recon' && <ReconTab form={form} setForm={setForm} cs={cs} />}
              {activeTab === 'scan' && <ScanTab form={form} setForm={setForm} />}
              {activeTab === 'search' && <SearchTab form={form} setForm={setForm} cs={cs} />}
              {activeTab === 'ioa' && <IOATab form={form} setForm={setForm} cs={cs} />}
              {activeTab === 'agent' && <AgentTab form={form} setForm={setForm} />}
            </div>
          )}

          {error && <div className="mt-3 rounded-md border border-destructive/30 bg-destructive/10 px-3 py-2 text-sm text-destructive">{error}</div>}
          {saved && <div className="mt-3 flex items-center gap-2 rounded-md border border-primary/30 bg-primary/10 px-3 py-2 text-sm text-primary"><CheckCircle className="h-4 w-4" />Saved and runtime reloaded</div>}
        </div>

        <div className="flex justify-end gap-2 border-t border-border px-4 py-3">
          <Button type="button" variant="outline" onClick={onClose}>Close</Button>
          <Button type="submit" disabled={loading || saving} className="bg-primary text-white hover:bg-primary">{saving && <Spinner className="h-4 w-4" />}Save</Button>
        </div>
      </form>
    </div>
  )
}

type TabProps = { form: DistributeConfig; setForm: React.Dispatch<React.SetStateAction<DistributeConfig>>; cs?: ConfigStatus | null }

function LLMTab({ form, setForm, cs }: TabProps) {
  const u = (k: string, v: string) => setForm((f) => ({ ...f, llm: { ...f.llm, [k]: v } }))
  return (
    <div className="grid gap-3 sm:grid-cols-2">
      <Field label="Provider">
        <Select value={form.llm.provider} onValueChange={(v) => u('provider', v)}>
          <SelectTrigger className="h-9 w-full"><SelectValue placeholder="Select provider" /></SelectTrigger>
          <SelectContent>
            {['deepseek','openai','openrouter','ollama','groq','moonshot','anthropic'].map((p) => <SelectItem key={p} value={p}>{p}</SelectItem>)}
          </SelectContent>
        </Select>
      </Field>
      <Field label="Model"><Input value={form.llm.model} onChange={(e) => u('model', e.target.value)} placeholder="deepseek-v4-pro / gpt-4.1" /></Field>
      <Field label="Base URL"><Input value={form.llm.base_url} onChange={(e) => u('base_url', e.target.value)} placeholder="leave empty for provider default" /></Field>
      <Field label="Proxy"><Input value={form.llm.proxy} onChange={(e) => u('proxy', e.target.value)} placeholder="http://127.0.0.1:7890" /></Field>
      <div className="sm:col-span-2">
        <Field label="API Key">
          <Input type="password" value={form.llm.api_key} onChange={(e) => u('api_key', e.target.value)}
            placeholder={cs?.llm.api_key_configured ? 'configured; leave blank to keep' : 'required unless ollama'} />
        </Field>
      </div>
    </div>
  )
}

function CyberhubTab({ form, setForm, cs }: TabProps) {
  const u = (k: string, v: string) => setForm((f) => ({ ...f, cyberhub: { ...f.cyberhub, [k]: v } }))
  return (
    <div className="grid gap-3 sm:grid-cols-2">
      <Field label="Cyberhub URL"><Input value={form.cyberhub.url} onChange={(e) => u('url', e.target.value)} placeholder="https://cyberhub.example.com" /></Field>
      <Field label="Mode">
        <Select value={form.cyberhub.mode || 'merge'} onValueChange={(v) => u('mode', v)}>
          <SelectTrigger className="h-9 w-full"><SelectValue placeholder="merge" /></SelectTrigger>
          <SelectContent><SelectItem value="merge">merge</SelectItem><SelectItem value="override">override</SelectItem></SelectContent>
        </Select>
      </Field>
      <Field label="Proxy"><Input value={form.cyberhub.proxy} onChange={(e) => u('proxy', e.target.value)} placeholder="socks5://127.0.0.1:1080" /></Field>
      <Field label="API Key">
        <Input type="password" value={form.cyberhub.key} onChange={(e) => u('key', e.target.value)}
          placeholder={cs?.cyberhub.key_configured ? 'configured; leave blank to keep' : 'cyberhub API key'} />
      </Field>
    </div>
  )
}

function ReconTab({ form, setForm, cs }: TabProps) {
  const u = (k: string, v: string) => setForm((f) => ({ ...f, recon: { ...f.recon, [k]: v } }))
  return (
    <div className="grid gap-3 sm:grid-cols-2">
      <Field label="FOFA Email"><Input value={form.recon.fofa_email} onChange={(e) => u('fofa_email', e.target.value)} placeholder="account@example.com" /></Field>
      <Field label="FOFA Key"><Input type="password" value={form.recon.fofa_key} onChange={(e) => u('fofa_key', e.target.value)} placeholder={cs?.recon.fofa_key_configured ? 'configured; leave blank to keep' : 'FOFA API key'} /></Field>
      <Field label="Hunter API Key"><Input type="password" value={form.recon.hunter_api_key} onChange={(e) => u('hunter_api_key', e.target.value)} placeholder={cs?.recon.hunter_api_key_configured ? 'configured; leave blank to keep' : '64-hex key'} /></Field>
      <Field label="Hunter Token"><Input type="password" value={form.recon.hunter_token} onChange={(e) => u('hunter_token', e.target.value)} placeholder={cs?.recon.hunter_token_configured ? 'configured; leave blank to keep' : 'web token (rarely needed)'} /></Field>
      <Field label="Recon Proxy"><Input value={form.recon.proxy} onChange={(e) => u('proxy', e.target.value)} placeholder="socks5://host:port" /></Field>
      <Field label="Per-query Limit">
        <Input type="number" value={form.recon.limit ?? ''} onChange={(e) => { const v = e.target.value; setForm((f) => ({ ...f, recon: { ...f.recon, limit: v === '' ? undefined : parseInt(v, 10) } })) }} placeholder="0 = unlimited" />
      </Field>
    </div>
  )
}

function ScanTab({ form, setForm }: Omit<TabProps, 'cs'>) {
  return (
    <div className="grid gap-3 sm:grid-cols-2">
      <Field label="Default Verify Mode">
        <Select value={form.scan.verify || 'auto'} onValueChange={(v) => setForm((f) => ({ ...f, scan: { ...f.scan, verify: v } }))}>
          <SelectTrigger className="h-9 w-full"><SelectValue placeholder="auto" /></SelectTrigger>
          <SelectContent>{['auto','off','low','high'].map((v) => <SelectItem key={v} value={v}>{v}</SelectItem>)}</SelectContent>
        </Select>
      </Field>
      <Field label="Verify Timeout (seconds)">
        <Input type="number" value={form.scan.verify_timeout || ''} onChange={(e) => setForm((f) => ({ ...f, scan: { ...f.scan, verify_timeout: parseInt(e.target.value, 10) || 0 } }))} placeholder="120" />
      </Field>
    </div>
  )
}

function SearchTab({ form, setForm, cs }: TabProps) {
  return (
    <div className="grid gap-3">
      <Field label="Tavily API Keys">
        <Input type="password" value={form.search.tavily_keys} onChange={(e) => setForm((f) => ({ ...f, search: { tavily_keys: e.target.value } }))}
          placeholder={cs?.search.tavily_keys_configured ? 'configured; leave blank to keep' : 'comma-separated keys (fallback: DuckDuckGo)'} />
      </Field>
    </div>
  )
}

function IOATab({ form, setForm, cs }: TabProps) {
  const u = (k: string, v: string) => setForm((f) => ({ ...f, ioa: { ...f.ioa, [k]: v } }))
  return (
    <div className="grid gap-3 sm:grid-cols-2">
      <Field label="IOA Server URL"><Input value={form.ioa.url} onChange={(e) => u('url', e.target.value)} placeholder="http://host:port" /></Field>
      <Field label="Access Token"><Input type="password" value={form.ioa.token} onChange={(e) => u('token', e.target.value)} placeholder={cs?.ioa.token_configured ? 'configured; leave blank to keep' : 'IOA access key'} /></Field>
      <Field label="Node Name"><Input value={form.ioa.node_name} onChange={(e) => u('node_name', e.target.value)} placeholder="auto-register node name" /></Field>
      <Field label="Space"><Input value={form.ioa.space} onChange={(e) => u('space', e.target.value)} placeholder="default" /></Field>
    </div>
  )
}

function AgentTab({ form, setForm }: Omit<TabProps, 'cs'>) {
  return (
    <div className="grid gap-3 sm:grid-cols-2">
      <Field label="Timeout (seconds)">
        <Input type="number" value={form.agent.timeout || ''} onChange={(e) => setForm((f) => ({ ...f, agent: { ...f.agent, timeout: parseInt(e.target.value, 10) || 0 } }))} placeholder="3600" />
      </Field>
      <Field label="Optional Tools">
        <Input value={(form.agent.tools || []).join(', ')} onChange={(e) => { const tools = e.target.value.split(',').map((t) => t.trim()).filter(Boolean); setForm((f) => ({ ...f, agent: { ...f.agent, tools } })) }} placeholder="search, browser" />
      </Field>
      <div className="sm:col-span-2">
        <label className="flex items-center gap-2 text-xs text-muted-foreground">
          <input type="checkbox" checked={form.agent.save_session} onChange={(e) => setForm((f) => ({ ...f, agent: { ...f.agent, save_session: e.target.checked } }))} className="rounded border-border" />
          Auto-save sessions
        </label>
      </div>
    </div>
  )
}

function Field({ label, children }: { label: string; children: ReactNode }) {
  return <label className="block space-y-1.5"><span className="text-xs font-medium text-muted-foreground">{label}</span>{children}</label>
}
