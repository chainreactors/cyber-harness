import { useCallback, useEffect, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { Button, StatusDot, Tooltip, TooltipContent, TooltipTrigger, type StatusKind } from '@cyber/ui'
import { getConfigStatus, testLLM } from '../api'

type Phase = 'checking' | 'ok' | 'error' | 'unconfigured'

interface Props {
  onOpenSettings: () => void
  /** Bump to force a re-probe (e.g. right after settings are saved). */
  reloadSignal: number
}

// A persistent LLM health dot in the header. getStatus()'s `llm_available` only
// means "a provider client was constructed" — it stays green even when the
// base_url 404s at request time (the misconfig people actually hit). So we run a
// real testLLM round-trip against the *stored* config (a blank api_key reuses
// the server-side secret) on mount and whenever reloadSignal changes — i.e.
// exactly when a bad endpoint would first bite: startup and just after saving
// settings. No tight polling, so we never hammer the model.
export default function LLMHealth({ onOpenSettings, reloadSignal }: Props) {
  const { t } = useTranslation('app')
  const [phase, setPhase] = useState<Phase>('checking')
  const [detail, setDetail] = useState('')

  const probe = useCallback(async () => {
    setPhase('checking')
    setDetail('')
    try {
      const cfg = await getConfigStatus()
      if (!cfg.llm.api_key_configured) {
        setPhase('unconfigured')
        return
      }
      const res = await testLLM({
        provider: cfg.llm.provider,
        base_url: cfg.llm.base_url,
        api_key: '', // reuse the key already stored server-side
        model: cfg.llm.model,
        proxy: cfg.llm.proxy,
      })
      if (res.ok) {
        setPhase('ok')
        const label = res.model || cfg.llm.model
        setDetail(res.latency_ms ? `${label} · ${res.latency_ms}ms` : label)
      } else {
        setPhase('error')
        setDetail(res.error || '')
      }
    } catch (err) {
      // Couldn't even reach our own backend to probe — surface as unreachable.
      setPhase('error')
      setDetail((err as Error)?.message || '')
    }
  }, [])

  useEffect(() => {
    void probe()
  }, [probe, reloadSignal])

  const meta: Record<Phase, { status: StatusKind; label: string }> = {
    checking: { status: 'running', label: t('llmChecking') },
    ok: { status: 'success', label: t('llmReady') },
    error: { status: 'error', label: t('llmUnreachable') },
    unconfigured: { status: 'warning', label: t('llmUnconfigured') },
  }
  const { status, label } = meta[phase]
  const title = [label, detail, t('llmHealthSettings')].filter(Boolean).join(' · ')

  return (
    <Tooltip>
      <TooltipTrigger asChild>
        <Button
          type="button"
          variant="ghost"
          onClick={onOpenSettings}
          aria-label={title}
          className="h-7 shrink-0 gap-1 px-1.5 text-[10px] text-muted-foreground"
        >
          <StatusDot status={status} />
          <span className="hidden sm:inline">{label}</span>
        </Button>
      </TooltipTrigger>
      <TooltipContent>{title}</TooltipContent>
    </Tooltip>
  )
}
