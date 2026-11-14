import { useState, useRef, useEffect, useCallback } from 'react'
import { useTranslation } from 'react-i18next'
import { AlertTriangle, Check, Copy, Download, Link, Loader2, RefreshCw } from 'lucide-react'
import { Button, Input, Tooltip, TooltipContent, TooltipTrigger } from '@cyber/ui'
import { cn } from '@cyber/theme'
import { copyToClipboard } from '../../cyber-ui/packages/template/src/clipboard'
import { getAgentConnectToken } from '../api'
import { nodeCommands, nodeDownloadURL, type OS, type Arch, type DownloadSource } from '../lib/node-bootstrap'

interface Platform {
  os: OS
  arch: Arch
}

const OS_OPTIONS: { value: OS; label: string }[] = [
  { value: 'linux', label: 'Linux' },
  { value: 'darwin', label: 'macOS' },
  { value: 'windows', label: 'Windows' },
]

const ARCH_OPTIONS: { value: Arch; label: string }[] = [
  { value: 'amd64', label: 'x86_64' },
  { value: 'arm64', label: 'ARM64' },
]

interface Props {
  serverURL: string | undefined
  version: string | undefined
  profiles?: Array<{ id: string; title?: string }>
}

const DEFAULT_PROFILES = [
  { id: 'cyber-scan', title: 'Cyber Scan' },
  { id: 'cyber-audit', title: 'Cyber Audit' },
]

function detectPlatform(): Platform {
  const ua = navigator.userAgent.toLowerCase()
  let os: OS = 'linux'
  if (ua.includes('win')) os = 'windows'
  else if (ua.includes('mac')) os = 'darwin'

  const arch: Arch = os === 'darwin' ? 'arm64' : 'amd64'
  return { os, arch }
}

export default function QuickConnect({ serverURL, version, profiles }: Props) {
  const { t } = useTranslation('app')
  const [open, setOpen] = useState(false)
  const [platform, setPlatform] = useState<Platform>(detectPlatform)
  const [distribution, setDistribution] = useState('cyber-scan')
  const [defaultNodeName] = useState(() => `node-${Math.random().toString(36).slice(2, 10)}`)
  const [nodeName, setNodeName] = useState('')
  const [downloadSource, setDownloadSource] = useState<DownloadSource>('global')
  const [copied, setCopied] = useState<string | null>(null)
  const [copyError, setCopyError] = useState(false)
  const copyResetTimer = useRef<ReturnType<typeof setTimeout> | null>(null)
  const [accessToken, setAccessToken] = useState<string | null>(null)
  const [tokenError, setTokenError] = useState(false)
  const [tokenRequest, setTokenRequest] = useState(0)
  const panelRef = useRef<HTMLDivElement>(null)
  const availableProfiles = profiles?.length ? profiles : DEFAULT_PROFILES

  useEffect(() => {
    if (availableProfiles.some((profile) => profile.id === distribution)) return
    setDistribution(availableProfiles[0].id)
    setCopied(null)
    setCopyError(false)
  }, [availableProfiles, distribution])

  const closePanel = useCallback(() => {
    if (copyResetTimer.current) clearTimeout(copyResetTimer.current)
    setOpen(false)
    setAccessToken(null)
    setTokenError(false)
    setCopied(null)
    setCopyError(false)
  }, [])

  const setOS = useCallback((os: OS) => {
    setPlatform((prev) => ({ ...prev, os }))
    setCopied(null)
    setCopyError(false)
  }, [])

  const setArch = useCallback((arch: Arch) => {
    setPlatform((prev) => ({ ...prev, arch }))
    setCopied(null)
    setCopyError(false)
  }, [])

  const handleCopy = useCallback(async (key: string, text: string) => {
    if (copyResetTimer.current) clearTimeout(copyResetTimer.current)
    const success = await copyToClipboard(text)
    if (!success) {
      setCopied(null)
      setCopyError(true)
      return
    }
    setCopyError(false)
    setCopied(key)
    copyResetTimer.current = setTimeout(() => setCopied(null), 2000)
  }, [])

  useEffect(() => {
    if (!open) return
    let cancelled = false
    setAccessToken(null)
    setTokenError(false)
    getAgentConnectToken()
      .then((token) => {
        if (!cancelled) {
          setAccessToken(token)
        }
      })
      .catch(() => {
        if (!cancelled) setTokenError(true)
      })
    return () => { cancelled = true }
  }, [open, tokenRequest])

  useEffect(() => {
    if (!open) return
    function onClickOutside(e: MouseEvent) {
      if (panelRef.current && !panelRef.current.contains(e.target as Node)) {
        closePanel()
      }
    }
    function onKeyDown(e: KeyboardEvent) {
      if (e.key === 'Escape') closePanel()
    }
    document.addEventListener('mousedown', onClickOutside)
    document.addEventListener('keydown', onKeyDown)
    return () => {
      document.removeEventListener('mousedown', onClickOutside)
      document.removeEventListener('keydown', onKeyDown)
    }
  }, [closePanel, open])

  if (!serverURL) return null

  const { os, arch } = platform
  const downloadSources: { value: DownloadSource; label: string }[] = [
    { value: 'global', label: t('quickConnectGlobal') },
    { value: 'china', label: t('quickConnectChina') },
  ]

  const tokenReady = accessToken !== null
  const options = { os, arch, distribution, source: downloadSource, version,
    serverURL: new URL(serverURL, window.location.origin).toString(), accessToken: accessToken ?? '',
    nodeName: nodeName.trim() || defaultNodeName }
  const { install, connect } = tokenReady ? nodeCommands(options) : { install: '', connect: '' }
  const downloadURL = nodeDownloadURL(options)

  return (
    <div className="relative" ref={panelRef}>
      <Tooltip>
        <TooltipTrigger asChild>
          <Button
            type="button"
            variant="ghost"
            size="icon-xs"
            aria-label={t('quickConnect')}
            onClick={() => open ? closePanel() : setOpen(true)}
            className={cn('hover:text-foreground', open ? 'text-foreground' : 'text-muted-foreground')}
          >
            <Link className="h-3.5 w-3.5" />
          </Button>
        </TooltipTrigger>
        <TooltipContent>{t('quickConnect')}</TooltipContent>
      </Tooltip>

      {open && (
        <div
          role="dialog"
          aria-label={t('quickConnectTitle')}
          className={cn(
            'z-50 rounded-lg border border-border bg-popover p-3 shadow-lg',
            // Phone: a right-aligned dropdown spills off the LEFT edge here. The
            // trigger sits mid-header (settings / language / theme sit to its
            // right), so pinning the panel's right edge to the trigger and giving
            // it 90vw pushes its left edge well past the screen. Anchor it to the
            // viewport just under the header instead, and let it scroll if the
            // commands run tall in landscape.
            'fixed inset-x-3 top-[calc(env(safe-area-inset-top)+3.5rem)] max-h-[calc(100dvh-5rem)] overflow-y-auto',
            // ≥md: enough width for the 36rem panel to sit right-anchored under the
            // trigger without its left edge clipping — revert to the dropdown. (At
            // sm the panel would still overflow left, so hold the pinned layout.)
            'md:absolute md:inset-x-auto md:right-0 md:top-full md:mt-2 md:max-h-none md:w-[36rem] md:max-w-[90vw] md:overflow-visible',
          )}
        >
          <div className="mb-3 flex flex-col gap-2 sm:flex-row sm:items-center sm:justify-between">
            <span className="flex items-center gap-2 text-xs font-medium text-foreground">
              {t('quickConnectTitle')}
              {tokenReady && (
                <span className="inline-flex items-center gap-1 text-[10px] font-normal text-emerald-600 dark:text-emerald-400">
                  <span className="h-1.5 w-1.5 rounded-full bg-current" />
                  {t('quickConnectTokenReady')}
                </span>
              )}
            </span>
            <div className="flex flex-wrap gap-1">
              {OS_OPTIONS.map((o) => (
                <button
                  key={o.value}
                  type="button"
                  onClick={() => setOS(o.value)}
                  className={cn(
                    'rounded px-2 py-0.5 text-[11px] transition-colors',
                    os === o.value
                      ? 'bg-primary text-primary-foreground'
                      : 'bg-secondary text-muted-foreground hover:text-foreground',
                  )}
                >
                  {o.label}
                </button>
              ))}
              <span className="mx-0.5 self-center text-border">|</span>
              {ARCH_OPTIONS.map((a) => (
                <button
                  key={a.value}
                  type="button"
                  onClick={() => setArch(a.value)}
                  className={cn(
                    'rounded px-2 py-0.5 text-[11px] transition-colors',
                    arch === a.value
                      ? 'bg-primary text-primary-foreground'
                      : 'bg-secondary text-muted-foreground hover:text-foreground',
                  )}
                >
                  {a.label}
                </button>
              ))}
            </div>
          </div>

          <div role="group" aria-label={t('quickConnectProfile')} className="mb-3 flex items-center gap-2">
            <span className="text-[11px] text-muted-foreground">{t('quickConnectProfile')}</span>
            {availableProfiles.map((profile) => (
              <Button key={profile.id} type="button" size="xs" variant={distribution === profile.id ? 'default' : 'outline'}
                aria-label={profile.id} aria-pressed={distribution === profile.id}
                onClick={() => { setDistribution(profile.id); setCopied(null); setCopyError(false) }}>
                {profile.title || profile.id}
              </Button>
            ))}
          </div>

          <label className="mb-3 flex items-center gap-2 text-[11px] text-muted-foreground">
            <span className="shrink-0">{t('quickConnectNodeName')}</span>
            <Input value={nodeName} placeholder={defaultNodeName} className="h-7 text-xs"
              onChange={(event) => { setNodeName(event.target.value); setCopied(null); setCopyError(false) }} />
          </label>

          {!tokenReady && !tokenError && (
            <div className="flex min-h-28 items-center justify-center gap-2 text-xs text-muted-foreground" role="status">
              <Loader2 className="h-4 w-4 animate-spin text-primary" />
              {t('quickConnectTokenLoading')}
            </div>
          )}

          {tokenError && (
            <div className="flex min-h-28 flex-col items-center justify-center gap-2 text-center" role="alert">
              <AlertTriangle className="h-5 w-5 text-destructive" />
              <span className="text-xs text-muted-foreground">{t('quickConnectTokenError')}</span>
              <Button size="xs" variant="outline" className="gap-1.5" onClick={() => setTokenRequest((request) => request + 1)}>
                <RefreshCw className="h-3 w-3" />
                {t('quickConnectRetry')}
              </Button>
            </div>
          )}

          {tokenReady && (
            <>
              <div className="mb-2 flex items-center justify-between gap-2">
                <span className="text-[11px] font-medium text-muted-foreground">{t('quickConnectDownloadSource')}</span>
                <div
                  role="group"
                  aria-label={t('quickConnectDownloadSource')}
                  className="inline-flex rounded-md bg-muted/60 p-0.5"
                >
                  {downloadSources.map((source) => (
                    <button
                      key={source.value}
                      type="button"
                      aria-pressed={downloadSource === source.value}
                      onClick={() => {
                        setDownloadSource(source.value)
                        setCopied(null)
                        setCopyError(false)
                      }}
                      className={cn(
                        'rounded px-2 py-0.5 text-[10px] transition-colors',
                        downloadSource === source.value
                          ? 'bg-background text-foreground shadow-sm'
                          : 'text-muted-foreground hover:text-foreground',
                      )}
                    >
                      {source.label}
                    </button>
                  ))}
                </div>
              </div>

              <CommandRow
                label={t('quickConnectInstall')}
                copyLabel={t('quickConnectCopy')}
                commands={[
                  { key: `install-${downloadSource}`, text: install },
                ]}
                copied={copied}
                onCopy={handleCopy}
              />

              <a href={downloadURL} className="mt-2 inline-flex items-center gap-1 text-[11px] text-primary hover:underline">
                <Download className="h-3 w-3" />{t('quickConnectDownload')}
              </a>

              <CommandRow
                label={t('quickConnectOnly')}
                copyLabel={t('quickConnectCopy')}
                commands={[
                  { key: 'connect', text: connect },
                ]}
                copied={copied}
                onCopy={handleCopy}
                className="mt-2"
              />

              {copyError && <p role="alert" className="mt-2 text-xs text-destructive">{t('quickConnectCopyError')}</p>}

              <p className="mt-2 text-[10px] text-muted-foreground">
                {t('quickConnectHint')}
              </p>
            </>
          )}
        </div>
      )}
    </div>
  )
}

interface CmdEntry {
  key: string
  tag?: string
  text: string
}

function CommandRow({ label, copyLabel, commands, copied, onCopy, className }: {
  label: string
  copyLabel: string
  commands: CmdEntry[]
  copied: string | null
  onCopy: (key: string, text: string) => void
  className?: string
}) {
  return (
    <div className={className}>
      <span className="mb-1 block text-[11px] font-medium text-muted-foreground">{label}</span>
      <div className="rounded-md bg-muted/50 p-2">
        <pre className="overflow-x-auto whitespace-pre-wrap break-all font-mono text-[11px] leading-relaxed text-foreground/90 pr-1">
          {commands[0].text}
        </pre>
        <div className="mt-1.5 flex gap-1.5 justify-end">
          {commands.map((c) => (
            <CopyButton key={c.key} tag={c.tag} label={`${copyLabel} — ${label}`} copied={copied === c.key} onClick={() => onCopy(c.key, c.text)} />
          ))}
        </div>
      </div>
    </div>
  )
}

function CopyButton({ tag, label, copied, onClick }: { tag?: string; label: string; copied: boolean; onClick: () => void }) {
  return (
    <button
      type="button"
      onClick={onClick}
      aria-label={label}
      className={cn(
        'inline-flex items-center gap-1 rounded px-2 py-0.5 text-[10px] transition-colors',
        copied
          ? 'bg-emerald-500/10 text-emerald-500'
          : 'bg-secondary text-muted-foreground hover:text-foreground',
      )}
    >
      {copied ? <Check className="h-3 w-3" /> : <Copy className="h-3 w-3" />}
      {tag && <span>{tag}</span>}
    </button>
  )
}
