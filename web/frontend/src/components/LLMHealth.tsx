import { useCallback, useEffect, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { Button, StatusDot, Tooltip, TooltipContent, TooltipTrigger, type StatusKind } from '@cyber/ui'
import { getStatus, llmConfigured, testLLM } from '../api'

type Phase = 'checking' | 'ok' | 'error' | 'unconfigured'

interface Props {
  onOpenSettings: () => void
  /** Bump to force a re-probe (e.g. right after settings are saved). */
  reloadSignal: number
}

// Probe the effective server provider on mount and after a settings save.
// A constructed client alone does not guarantee working credentials or endpoints.
// An empty probe resolves environment and CLI overrides on the server.
export default function LLMHealth({ onOpenSettings, reloadSignal }: Props) {
  const { t } = useTranslation('app')
  const [phase, setPhase] = useState<Phase>('checking')
  const [detail, setDetail] = useState('')

  const probe = useCallback(async () => {
    setPhase('checking')
    setDetail('')
    try {
      const status = await getStatus()
      // Same predicate the settings panel uses, so the two never disagree.
      if (!llmConfigured(status)) {
        setPhase('unconfigured')
        return
      }
      const res = await testLLM({})
      if (res.ok) {
        setPhase('ok')
        const label = res.model || status.llmModel || ''
        setDetail(res.latencyMs ? `${label} · ${res.latencyMs}ms` : label)
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
