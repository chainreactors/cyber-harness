import { useEffect, useState, type FormEvent } from 'react'
import { useTranslation } from 'react-i18next'
import { Check, CheckCircle, Plus, Settings, Trash2, Zap } from 'lucide-react'
import { getConfigStatus, saveConfig, testLLM, testConn, listLLMModels } from '../api'
import type { ConfigStatus, ConnCheck, DistributeConfig, LLMProviderProfile, LLMTestResult, ServerStatus } from '../api'
import { Button, Input, Select, SelectTrigger, SelectContent, SelectItem, SelectValue, Badge, Spinner, Callout, Field, Switch, Dialog, DialogContent, DialogHeader, DialogFooter, DialogTitle, DialogDescription, ResultLine } from '@cyber/ui'
import { cn } from '@cyber/theme'
import { ModelCombobox } from './ModelCombobox'

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
  { key: 'ioa', label: 'Server' },
  { key: 'agent', label: 'Agent' },
]

function emptyForm(): DistributeConfig {
  const profile = blankLLMProfile('default')
  return {
    llm: { provider: '', base_url: '', api_key: '', model: '', proxy: '', active_profile: profile.id, providers: [profile] },
    cyberhub: { url: '', key: '', mode: '', proxy: '' },
    recon: { fofa_email: '', fofa_key: '', hunter_token: '', hunter_api_key: '', proxy: '' },
    scan: { verify: '', verify_timeout: 0 },
    search: { tavily_keys: '' },
    ioa: { url: '', token: '', node_name: '', space: '' },
    agent: { tools: [], timeout: 0, save_session: false },
  }
}

function statusToForm(cs: ConfigStatus): DistributeConfig {
  const profiles: LLMProviderProfile[] = cs.llm.profiles?.length
    ? cs.llm.profiles.map(profile => ({ ...profile, api_key: '' }))
    : [{
        id: cs.llm.active_profile || 'default',
        name: cs.llm.model || cs.llm.provider || 'Default',
        provider: cs.llm.provider,
        base_url: cs.llm.base_url,
        api_key: '',
        model: cs.llm.model,
        proxy: cs.llm.proxy,
      }]
  const activeID = cs.llm.active_profile || profiles[0].id
  const active = profiles.find(profile => profile.id === activeID) || profiles[0]
  return {
    llm: {
      provider: active.provider,
      base_url: active.base_url,
      api_key: '',
      model: active.model,
      proxy: active.proxy,
      active_profile: activeID,
      providers: profiles,
    },
    cyberhub: { url: cs.cyberhub.url, key: '', mode: cs.cyberhub.mode, proxy: cs.cyberhub.proxy },
    recon: { fofa_email: cs.recon.fofa_email, fofa_key: '', hunter_token: '', hunter_api_key: '', proxy: cs.recon.proxy, limit: cs.recon.limit },
    scan: { ...cs.scan },
    search: { tavily_keys: '' },
    ioa: { url: cs.ioa.url, token: '', node_name: cs.ioa.node_name, space: cs.ioa.space },
    agent: { ...cs.agent },
  }
}

function blankLLMProfile(id = `llm-${Date.now()}`): LLMProviderProfile {
  return { id, name: 'New LLM', provider: 'openai', base_url: '', api_key: '', model: '', proxy: '' }
}

function prepareConfigForSave(form: DistributeConfig): DistributeConfig {
  const active = form.llm.providers.find(profile => profile.id === form.llm.active_profile) || form.llm.providers[0]
  if (!active) return form
  return {
    ...form,
    llm: {
      ...form.llm,
      provider: active.provider,
      base_url: active.base_url,
      api_key: active.api_key,
      model: active.model,
      proxy: active.proxy,
    },
  }
}

// sectionStatus returns the readiness badges for the active settings section.
// Each tab surfaces ITS OWN dependencies (Recon → FOFA/Hunter, Search → Tavily,
// IOA → token) instead of repeating the global "LLM ready" badge on every tab.
// Local-only tabs (scan, agent) have no external dependency, so they return none
// and only the global "config loaded" badge shows.
function sectionStatus(
  tab: TabKey,
  cs: ConfigStatus | null,
  status: ServerStatus | null,
  t: (key: string) => string,
): { key: string; label: string; ok: boolean }[] {
  const tag = (name: string, ok: boolean) => ({ key: name, label: `${name} ${ok ? t('configured') : t('notConfigured')}`, ok })
  switch (tab) {
    case 'llm':
      return [{ key: 'llm', label: status?.llm_available ? t('llmReady') : t('llmOffline'), ok: !!status?.llm_available }]
    case 'cyberhub':
      return [tag('Cyberhub', !!(cs?.cyberhub.url && cs?.cyberhub.key_configured))]
    case 'recon':
      return [
        // FOFA's 2023 simplified auth needs only the API key — the email never
        // enters the request URL (see engine/uncover.go). Gate readiness on the
        // key alone so a valid key-only setup doesn't read as "not configured".
        tag('FOFA', !!cs?.recon.fofa_key_configured),
        tag('Hunter', !!(cs?.recon.hunter_api_key_configured || cs?.recon.hunter_token_configured)),
      ]
    case 'search':
      return [tag('Tavily', !!cs?.search.tavily_keys_configured)]
    case 'ioa':
      return [tag('Server', !!(cs?.ioa.url && cs?.ioa.token_configured))]
    default:
      return [] // scan, agent — local only
  }
}

export default function ConfigPanel({ open, status, onClose, onSaved }: ConfigPanelProps) {
  const { t } = useTranslation('config')
  const [cs, setCs] = useState<ConfigStatus | null>(null)
  const [form, setForm] = useState<DistributeConfig>(emptyForm)
  const [loading, setLoading] = useState(false)
  const [saving, setSaving] = useState(false)
  const [error, setError] = useState('')
  const [saved, setSaved] = useState(false)
  const [activeTab, setActiveTab] = useState<TabKey>('llm')
  const [selectedLLMProfileID, setSelectedLLMProfileID] = useState('default')

  useEffect(() => {
    if (!open) return
    setLoading(true)
    setError('')
    setSaved(false)
    getConfigStatus()
      .then((s) => {
        const next = statusToForm(s)
        setCs(s)
        setForm(next)
        setSelectedLLMProfileID(next.llm.active_profile)
      })
      .catch((err: Error) => setError(err.message || t('failedLoad')))
      .finally(() => setLoading(false))
  }, [open])

  const handleSave = async (event: FormEvent) => {
    event.preventDefault()
    setSaving(true)
    setError('')
    setSaved(false)
    try {
      const next = await saveConfig(prepareConfigForSave(form))
      setCs(next)
      setForm(statusToForm(next))
      setSaved(true)
      onSaved()
    } catch (err: unknown) {
      const message = err instanceof Error ? err.message : String(err)
      setError(message || t('failedSave'))
    } finally {
      setSaving(false)
    }
  }

  return (
    <Dialog open={open} onOpenChange={(next) => { if (!next) onClose() }}>
      <DialogContent
        onInteractOutside={(e) => e.preventDefault()}
        className="block max-h-[85vh] w-full max-w-3xl gap-0 overflow-y-auto overflow-x-hidden rounded-2xl border-border/70 bg-card p-0 sm:rounded-2xl"
      >
        <form onSubmit={handleSave}>
        <DialogHeader className="flex flex-row items-center justify-between border-b border-border/60 px-4 py-3 pr-12 text-left">
          <div className="flex items-center gap-2">
            <Settings className="h-4 w-4 text-primary" />
            <div>
              <DialogTitle className="text-sm font-medium text-foreground">{t('settings')}</DialogTitle>
              <DialogDescription className="text-xs text-muted-foreground">{cs?.config_path || status?.config_path || 'config.yaml'}</DialogDescription>
            </div>
          </div>
        </DialogHeader>

        <div className="flex gap-1 overflow-x-auto border-b border-border px-4 py-1">
          {TABS.map((tab) => (
            <Button
              key={tab.key} type="button" variant="ghost" size="sm"
              active={activeTab === tab.key} onClick={() => setActiveTab(tab.key)}
              className={cn('h-8 text-xs', activeTab !== tab.key && 'text-muted-foreground')}
            >{tab.label}</Button>
          ))}
        </div>

        <div className="p-4">
          {loading ? (
            <div className="flex h-48 flex-col items-center justify-center gap-3 text-muted-foreground">
              <Spinner className="h-8 w-8" />
              <span className="text-xs">{t('loading')}</span>
            </div>
          ) : (
            <>
              <div className="mb-3 flex flex-wrap items-center gap-2 text-xs">
                {sectionStatus(activeTab, cs, status, t).map((b) => (
                  <Badge key={b.key} variant={b.ok ? 'success' : 'warning'} className="text-xs">{b.label}</Badge>
                ))}
                <Badge variant={cs?.config_loaded ? 'success' : 'warning'} className="text-xs">{cs?.config_loaded ? t('configLoaded') : t('configMissing')}</Badge>
              </div>
              <div className="min-h-[12rem]">
                {activeTab === 'llm' && (
                  <LLMTab
                    form={form}
                    setForm={setForm}
                    cs={cs}
                    selectedProfileID={selectedLLMProfileID}
                    onSelectProfile={setSelectedLLMProfileID}
                  />
                )}
                {activeTab === 'cyberhub' && <CyberhubTab form={form} setForm={setForm} cs={cs} />}
                {activeTab === 'recon' && <ReconTab form={form} setForm={setForm} cs={cs} />}
                {activeTab === 'scan' && <ScanTab form={form} setForm={setForm} />}
                {activeTab === 'search' && <SearchTab form={form} setForm={setForm} cs={cs} />}
                {activeTab === 'ioa' && <IOATab form={form} setForm={setForm} cs={cs} />}
                {activeTab === 'agent' && <AgentTab form={form} setForm={setForm} />}
              </div>
            </>
          )}

          {error && <Callout tone="destructive" className="mt-3">{error}</Callout>}
          {saved && <Callout tone="primary" icon={<CheckCircle className="h-4 w-4" />} className="mt-3">{t('saved')}</Callout>}
        </div>

        <DialogFooter className="flex flex-row justify-end gap-2 border-t border-border/60 px-4 py-3 sm:space-x-0">
          <Button type="button" variant="outline" onClick={onClose}>{t('close')}</Button>
          <Button type="submit" disabled={loading || saving}>{saving && <Spinner className="h-4 w-4" />}{t('save')}</Button>
        </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  )
}

type TabProps = { form: DistributeConfig; setForm: React.Dispatch<React.SetStateAction<DistributeConfig>>; cs?: ConfigStatus | null }

function LLMTab({
  form,
  setForm,
  cs,
  selectedProfileID,
  onSelectProfile,
}: TabProps & { selectedProfileID: string; onSelectProfile: (id: string) => void }) {
  const { t } = useTranslation('config')
  const [testing, setTesting] = useState(false)
  const [result, setResult] = useState<LLMTestResult | null>(null)
  const [models, setModels] = useState<string[]>([])
  const [fetchingModels, setFetchingModels] = useState(false)
  const [modelsError, setModelsError] = useState<string | null>(null)

  const profiles = form.llm.providers
  const profile = profiles.find(item => item.id === selectedProfileID) || profiles[0]
  const configuredProfile = cs?.llm.profiles?.find(item => item.id === profile?.id)

  const updateProfile = (key: keyof LLMProviderProfile, value: string) => {
    if (!profile) return
    setForm(current => ({
      ...current,
      llm: {
        ...current.llm,
        providers: current.llm.providers.map(item => item.id === profile.id ? { ...item, [key]: value } : item),
      },
    }))
  }

  const addProfile = () => {
    const next = blankLLMProfile()
    setForm(current => ({ ...current, llm: { ...current.llm, providers: [...current.llm.providers, next] } }))
    onSelectProfile(next.id)
    setModels([])
    setResult(null)
  }

  const removeProfile = () => {
    if (!profile || profiles.length <= 1) return
    const remaining = profiles.filter(item => item.id !== profile.id)
    const nextActive = form.llm.active_profile === profile.id ? remaining[0].id : form.llm.active_profile
    setForm(current => ({
      ...current,
      llm: { ...current.llm, active_profile: nextActive, providers: remaining },
    }))
    onSelectProfile(remaining[0].id)
    setModels([])
    setResult(null)
  }

  const setActiveProfile = () => {
    if (!profile) return
    setForm(current => ({ ...current, llm: { ...current.llm, active_profile: profile.id } }))
  }

  const handleFetchModels = async () => {
    if (!profile) return
    setFetchingModels(true)
    setModelsError(null)
    try {
      const res = await listLLMModels({
        provider: profile.provider,
        base_url: profile.base_url,
        api_key: profile.api_key,
        proxy: profile.proxy,
      })
      if (res.ok) {
        setModels(res.models ?? [])
        if ((res.models ?? []).length === 0) setModelsError(t('modelsEmpty'))
      } else {
        setModelsError(res.error || t('modelsFailed'))
      }
    } catch (err: unknown) {
      const message = err instanceof Error ? err.message : String(err)
      setModelsError(message || t('modelsFailed'))
    } finally {
      setFetchingModels(false)
    }
  }

  const handleTest = async () => {
    if (!profile) return
    setTesting(true)
    setResult(null)
    try {
      const res = await testLLM({
        provider: profile.provider,
        base_url: profile.base_url,
        api_key: profile.api_key,
        model: profile.model,
        proxy: profile.proxy,
      })
      setResult(res)
    } catch (err: unknown) {
      const message = err instanceof Error ? err.message : String(err)
      setResult({ ok: false, provider: profile.provider, model: profile.model, latency_ms: 0, error: message || t('testFailed') })
    } finally {
      setTesting(false)
    }
  }

  if (!profile) return null

  return (
    <div className="grid gap-3 sm:grid-cols-2">
      <div className="sm:col-span-2 flex flex-wrap items-center gap-2 rounded-lg border border-border/70 bg-muted/15 p-2">
        {profiles.map(item => {
          const active = item.id === form.llm.active_profile
          const selected = item.id === profile.id
          return (
            <button key={item.id} type="button" onClick={() => { onSelectProfile(item.id); setModels([]); setResult(null) }}>
              <Badge
                variant={active ? 'success' : selected ? 'secondary' : 'outline'}
                className={cn('gap-1.5 py-1 text-xs transition-colors', selected && 'ring-2 ring-primary/15')}
              >
                {active && <Check className="h-3 w-3" />}
                {item.name || item.model || item.provider || t('unnamedProfile')}
              </Badge>
            </button>
          )
        })}
        <Button type="button" variant="ghost" size="xs" onClick={addProfile} className="gap-1 text-muted-foreground">
          <Plus className="h-3.5 w-3.5" />
          {t('addLLMProfile')}
        </Button>
      </div>

      <Field label={t('profileName')}>
        <Input value={profile.name} onChange={(e) => updateProfile('name', e.target.value)} placeholder={t('profileNameHint')} />
      </Field>
      <Field label={t('provider')}>
        <Select value={profile.provider} onValueChange={(v) => updateProfile('provider', v)}>
          <SelectTrigger className="h-9 w-full"><SelectValue placeholder={t('selectProvider')} /></SelectTrigger>
          <SelectContent>
            {['deepseek','openai','openrouter','ollama','groq','moonshot','anthropic'].map((p) => <SelectItem key={p} value={p}>{p}</SelectItem>)}
          </SelectContent>
        </Select>
      </Field>
      <Field label={t('model')} error={modelsError}>
        <ModelCombobox
          value={profile.model}
          onChange={(v) => updateProfile('model', v)}
          models={models}
          loading={fetchingModels}
          error={modelsError}
          onRefresh={handleFetchModels}
          placeholder="deepseek-v4-pro / gpt-4.1"
        />
      </Field>
      <Field label={t('baseUrl')}><Input value={profile.base_url} onChange={(e) => updateProfile('base_url', e.target.value)} placeholder={t('providerDefault')} /></Field>
      <Field label={t('proxy')}><Input value={profile.proxy} onChange={(e) => updateProfile('proxy', e.target.value)} placeholder="http://127.0.0.1:7890" /></Field>
      <div className="sm:col-span-2">
        <Field label={t('apiKey')}>
          <Input type="password" value={profile.api_key} onChange={(e) => updateProfile('api_key', e.target.value)}
            placeholder={configuredProfile?.api_key_configured ? t('configuredKeep') : t('requiredUnlessOllama')} />
        </Field>
      </div>
      <div className="sm:col-span-2 flex flex-wrap items-center gap-3">
        <Button type="button" variant="outline" size="sm" onClick={handleTest} disabled={testing || !profile.model}>
          {testing ? <ProbePulse /> : <Zap className="h-4 w-4" />}
          {testing ? t('testing') : t('testConnection')}
        </Button>
        {profile.id !== form.llm.active_profile && (
          <Button type="button" variant="outline" size="sm" onClick={setActiveProfile}>
            <Check className="h-4 w-4" />
            {t('setActiveProfile')}
          </Button>
        )}
        {profiles.length > 1 && (
          <Button type="button" variant="ghost" size="sm" onClick={removeProfile} className="text-destructive hover:text-destructive">
            <Trash2 className="h-4 w-4" />
            {t('removeProfile')}
          </Button>
        )}
        {result && (
          <ResultLine ok={result.ok} title={result.ok ? undefined : result.error}>
            {result.ok
              ? <>{t('testOk')} · {t('testLatency')} {result.latency_ms}ms{result.reply ? ` · ${t('testReply')}: ${result.reply}` : ''}</>
              : <>{t('testFailed')}: {result.error}</>}
          </ResultLine>
        )}
      </div>
    </div>
  )
}

function CyberhubTab({ form, setForm, cs }: TabProps) {
  const { t } = useTranslation('config')
  const u = (k: string, v: string) => setForm((f) => ({ ...f, cyberhub: { ...f.cyberhub, [k]: v } }))
  return (
    <div className="grid gap-3 sm:grid-cols-2">
      <Field label={t('cyberhubUrl')}><Input value={form.cyberhub.url} onChange={(e) => u('url', e.target.value)} placeholder="https://cyberhub.example.com" /></Field>
      <Field label={t('mode')}>
        <Select value={form.cyberhub.mode || 'merge'} onValueChange={(v) => u('mode', v)}>
          <SelectTrigger className="h-9 w-full"><SelectValue placeholder="merge" /></SelectTrigger>
          <SelectContent><SelectItem value="merge">merge</SelectItem><SelectItem value="override">override</SelectItem></SelectContent>
        </Select>
      </Field>
      <Field label={t('proxy')}><Input value={form.cyberhub.proxy} onChange={(e) => u('proxy', e.target.value)} placeholder="socks5://127.0.0.1:1080" /></Field>
      <Field label={t('apiKey')}>
        <Input type="password" value={form.cyberhub.key} onChange={(e) => u('key', e.target.value)}
          placeholder={cs?.cyberhub.key_configured ? t('configuredKeep') : t('cyberhubApiKey')} />
      </Field>
      <ConnTest section="cyberhub" form={form} />
    </div>
  )
}

function ReconTab({ form, setForm, cs }: TabProps) {
  const { t } = useTranslation('config')
  const u = (k: string, v: string) => setForm((f) => ({ ...f, recon: { ...f.recon, [k]: v } }))
  return (
    <div className="grid gap-3 sm:grid-cols-2">
      <Field label={t('fofaEmail')}><Input value={form.recon.fofa_email} onChange={(e) => u('fofa_email', e.target.value)} placeholder="account@example.com" /></Field>
      <Field label={t('fofaKey')}><Input type="password" value={form.recon.fofa_key} onChange={(e) => u('fofa_key', e.target.value)} placeholder={cs?.recon.fofa_key_configured ? t('configuredKeep') : t('fofaApiKey')} /></Field>
      <Field label={t('hunterApiKey')}><Input type="password" value={form.recon.hunter_api_key} onChange={(e) => u('hunter_api_key', e.target.value)} placeholder={cs?.recon.hunter_api_key_configured ? t('configuredKeep') : t('hex64')} /></Field>
      <Field label={t('hunterToken')}><Input type="password" value={form.recon.hunter_token} onChange={(e) => u('hunter_token', e.target.value)} placeholder={cs?.recon.hunter_token_configured ? t('configuredKeep') : t('webTokenRare')} /></Field>
      <Field label={t('reconProxy')}><Input value={form.recon.proxy} onChange={(e) => u('proxy', e.target.value)} placeholder="socks5://host:port" /></Field>
      <Field label={t('perQueryLimit')}>
        <Input type="number" value={form.recon.limit ?? ''} onChange={(e) => { const v = e.target.value; setForm((f) => ({ ...f, recon: { ...f.recon, limit: v === '' ? undefined : parseInt(v, 10) } })) }} placeholder={t('unlimited')} />
      </Field>
      <ConnTest section="recon" form={form} />
    </div>
  )
}

function ScanTab({ form, setForm }: Omit<TabProps, 'cs'>) {
  const { t } = useTranslation('config')
  return (
    <div className="grid gap-3 sm:grid-cols-2">
      <Field label={t('defaultVerifyMode')}>
        <Select value={form.scan.verify || 'auto'} onValueChange={(v) => setForm((f) => ({ ...f, scan: { ...f.scan, verify: v } }))}>
          <SelectTrigger className="h-9 w-full"><SelectValue placeholder="auto" /></SelectTrigger>
          <SelectContent>{['auto','off','low','high'].map((v) => <SelectItem key={v} value={v}>{v}</SelectItem>)}</SelectContent>
        </Select>
      </Field>
      <Field label={t('verifyTimeout')}>
        <Input type="number" value={form.scan.verify_timeout || ''} onChange={(e) => setForm((f) => ({ ...f, scan: { ...f.scan, verify_timeout: parseInt(e.target.value, 10) || 0 } }))} placeholder="120" />
      </Field>
      <p className="sm:col-span-2 text-xs text-muted-foreground">{t('localOnlyNote')}</p>
    </div>
  )
}

function SearchTab({ form, setForm, cs }: TabProps) {
  const { t } = useTranslation('config')
  return (
    <div className="grid gap-3">
      <Field label={t('tavilyKeys')}>
        <Input type="password" value={form.search.tavily_keys} onChange={(e) => setForm((f) => ({ ...f, search: { tavily_keys: e.target.value } }))}
          placeholder={cs?.search.tavily_keys_configured ? t('configuredKeep') : t('tavilyHint')} />
      </Field>
      <ConnTest section="search" form={form} />
    </div>
  )
}

function IOATab({ form, setForm, cs }: TabProps) {
  const { t } = useTranslation('config')
  const u = (k: string, v: string) => setForm((f) => ({ ...f, ioa: { ...f.ioa, [k]: v } }))
  return (
    <div className="grid gap-3 sm:grid-cols-2">
      <Field label={t('ioaServerUrl')}><Input value={form.ioa.url} onChange={(e) => u('url', e.target.value)} placeholder="http://host:port" /></Field>
      <Field label={t('accessToken')}><Input type="password" value={form.ioa.token} onChange={(e) => u('token', e.target.value)} placeholder={cs?.ioa.token_configured ? t('configuredKeep') : t('ioaAccessKey')} /></Field>
      <Field label={t('nodeName')}><Input value={form.ioa.node_name} onChange={(e) => u('node_name', e.target.value)} placeholder={t('autoRegisterNode')} /></Field>
      <Field label={t('space')}><Input value={form.ioa.space} onChange={(e) => u('space', e.target.value)} placeholder="default" /></Field>
      <ConnTest section="ioa" form={form} />
    </div>
  )
}

function AgentTab({ form, setForm }: Omit<TabProps, 'cs'>) {
  const { t } = useTranslation('config')
  return (
    <div className="grid gap-3 sm:grid-cols-2">
      <Field label={t('timeout')}>
        <Input type="number" value={form.agent.timeout || ''} onChange={(e) => setForm((f) => ({ ...f, agent: { ...f.agent, timeout: parseInt(e.target.value, 10) || 0 } }))} placeholder="3600" />
      </Field>
      <Field label={t('optionalTools')}>
        <Input value={(form.agent.tools || []).join(', ')} onChange={(e) => { const tools = e.target.value.split(',').map((s) => s.trim()).filter(Boolean); setForm((f) => ({ ...f, agent: { ...f.agent, tools } })) }} placeholder="search, browser" />
      </Field>
      <div className="sm:col-span-2">
        <label className="flex items-center gap-2 text-xs text-muted-foreground">
          <Switch checked={form.agent.save_session} onCheckedChange={(v) => setForm((f) => ({ ...f, agent: { ...f.agent, save_session: v } }))} />
          {t('autoSaveSessions')}
        </label>
      </div>
      <p className="sm:col-span-2 text-xs text-muted-foreground">{t('localOnlyNote')}</p>
    </div>
  )
}

// ProbePulse — the indicator shown while a connection test is in flight. A radar
// "ping": concentric signal rings emanate from a luminous source dot, echoing the
// deck's radar/sonar motion signature. Deliberately NOT a rotating ring — probing
// a remote endpoint reads as a pulse outward, not a spinner. Under reduced-motion
// the rings hold static, settling into a quiet reticle.
function ProbePulse({ className }: { className?: string }) {
  return (
    <span
      role="status"
      aria-label="probing"
      className={cn('relative inline-flex h-4 w-4 shrink-0 items-center justify-center', className)}
    >
      <span className="absolute h-2.5 w-2.5 rounded-full border border-primary/60 motion-safe:animate-ping" />
      <span className="absolute h-2.5 w-2.5 rounded-full border border-primary/40 motion-safe:animate-ping [animation-delay:0.5s]" />
      <span className="h-1.5 w-1.5 rounded-full bg-primary shadow-[0_0_6px_hsl(var(--primary)_/_0.9)]" />
    </span>
  )
}

// ConnTest renders a "Test connection" button for a settings section and shows
// one result row per external dependency probed (Recon returns FOFA + Hunter).
// The whole form is sent so unsaved edits are tested; blank secrets fall back to
// the stored values on the server.
function ConnTest({ section, form }: { section: 'cyberhub' | 'recon' | 'search' | 'ioa'; form: DistributeConfig }) {
  const { t } = useTranslation('config')
  const [testing, setTesting] = useState(false)
  const [checks, setChecks] = useState<ConnCheck[] | null>(null)

  const handleTest = async () => {
    setTesting(true)
    setChecks(null)
    try {
      const res = await testConn(section, form)
      setChecks(res.checks)
    } catch (err: unknown) {
      const message = err instanceof Error ? err.message : String(err)
      setChecks([{ name: section, ok: false, latency_ms: 0, error: message || t('testFailed') }])
    } finally {
      setTesting(false)
    }
  }

  return (
    <div className="sm:col-span-2 space-y-2">
      <Button type="button" variant="outline" size="sm" onClick={handleTest} disabled={testing}>
        {testing ? <ProbePulse /> : <Zap className="h-4 w-4" />}
        {testing ? t('testing') : t('testConnection')}
      </Button>
      {checks && (
        <div className="space-y-1">
          {checks.map((c, i) => <ConnCheckRow key={`${c.name}-${i}`} check={c} />)}
        </div>
      )}
    </div>
  )
}

const CHECK_LABELS: Record<string, string> = {
  fofa: 'FOFA', hunter: 'Hunter', cyberhub: 'Cyberhub', tavily: 'Tavily', ioa: 'Server',
}

function ConnCheckRow({ check }: { check: ConnCheck }) {
  const { t } = useTranslation('config')
  const label = CHECK_LABELS[check.name] ?? check.name
  return (
    <ResultLine ok={check.ok} title={check.ok ? undefined : check.error}>
      {check.ok
        ? <>{label} · {t('testOk')} · {t('testLatency')} {check.latency_ms}ms{check.detail ? ` · ${check.detail}` : ''}</>
        : <>{label} · {t('testFailed')}: {check.error}</>}
    </ResultLine>
  )
}
