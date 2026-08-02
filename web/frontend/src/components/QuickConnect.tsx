import { useState, useRef, useEffect, useCallback } from 'react'
import { useTranslation } from 'react-i18next'
import { Check, Copy, Link } from 'lucide-react'
import { Button, Tooltip, TooltipContent, TooltipTrigger } from '@cyber/ui'
import { cn } from '@cyber/theme'

type OS = 'linux' | 'darwin' | 'windows'
type Arch = 'amd64' | 'arm64'

interface Platform {
  os: OS
  arch: Arch
}

const OS_OPTIONS: { value: OS; label: string }[] = [
  { value: 'linux', label: 'Linux' },
  { value: 'darwin', label: 'macOS' },
  { value: 'windows', label: 'Windows' },
]

const ARCH_OPTIONS: { value: Arch; label: string; osFilter?: OS[] }[] = [
  { value: 'amd64', label: 'x86_64' },
  { value: 'arm64', label: 'ARM64', osFilter: ['linux', 'darwin'] },
]

const CHINA_MIRROR = 'https://ghfast.top/'
const ACCESS_TOKEN_PLACEHOLDER = 'ACCESS_TOKEN'
const NODE_NAME_PLACEHOLDER = 'NODE_NAME'

interface Props {
  serverURL: string | undefined
  version: string | undefined
}

function detectPlatform(): Platform {
  const ua = navigator.userAgent.toLowerCase()
  let os: OS = 'linux'
  if (ua.includes('win')) os = 'windows'
  else if (ua.includes('mac')) os = 'darwin'

  const arch: Arch = os === 'darwin' ? 'arm64' : 'amd64'
  return { os, arch }
}

function archOptionsForOS(os: OS) {
  return ARCH_OPTIONS.filter((a) => !a.osFilter || a.osFilter.includes(os))
}

function binaryName(os: OS, arch: Arch): string {
  return `aiscan-full_${os}_${arch}.zip`
}

function releaseURL(os: OS, arch: Arch, version?: string, mirror?: string): string {
  const tag = version && version !== 'dev' ? `v${version}` : 'latest'
  const base = tag !== 'latest'
    ? `https://github.com/chainreactors/aiscan/releases/download/${tag}`
    : `https://github.com/chainreactors/aiscan/releases/latest/download`
  const url = `${base}/${binaryName(os, arch)}`
  return mirror ? mirror + url : url
}

function authenticatedURL(rawURL: string): string {
  const url = new URL(rawURL, window.location.origin)
  url.username = ACCESS_TOKEN_PLACEHOLDER
  url.password = ''
  return url.toString().replace(/\/$/, '')
}

function agentArgs(serverURL: string): string {
  return `--server-url '${authenticatedURL(serverURL)}' --space default --node-name '${NODE_NAME_PLACEHOLDER}'`
}

function connectCmd(os: OS, serverURL: string): string {
  const args = agentArgs(serverURL)
  if (os === 'windows') {
    return `.\\aiscan-full.exe agent ${args}`
  }
  return `./aiscan-full agent ${args}`
}

function installCmd(os: OS, arch: Arch, serverURL: string, version?: string, mirror?: string): string {
  const dlURL = releaseURL(os, arch, version, mirror)
  const args = agentArgs(serverURL)
  if (os === 'windows') {
    return `powershell -c "Invoke-WebRequest '${dlURL}' -OutFile aiscan.zip; Expand-Archive aiscan.zip -DestinationPath .; .\\aiscan-full.exe agent ${args}"`
  }
  const bin = 'aiscan-full'
  return `curl -sL '${dlURL}' -o aiscan.zip && unzip -o aiscan.zip ${bin} && chmod +x ${bin} && ./${bin} agent ${args}`
}

type CopiedKey = string | null

export default function QuickConnect({ serverURL, version }: Props) {
  const { t } = useTranslation('app')
  const [open, setOpen] = useState(false)
  const [platform, setPlatform] = useState<Platform>(detectPlatform)
  const [copied, setCopied] = useState<CopiedKey>(null)
  const panelRef = useRef<HTMLDivElement>(null)

  const setOS = useCallback((os: OS) => {
    setPlatform((prev) => {
      const available = archOptionsForOS(os)
      const arch = available.some((a) => a.value === prev.arch) ? prev.arch : 'amd64'
      return { os, arch }
    })
    setCopied(null)
  }, [])

  const setArch = useCallback((arch: Arch) => {
    setPlatform((prev) => ({ ...prev, arch }))
    setCopied(null)
  }, [])

  const handleCopy = useCallback(async (key: string, text: string) => {
    await navigator.clipboard.writeText(text)
    setCopied(key)
    setTimeout(() => setCopied(null), 2000)
  }, [])

  useEffect(() => {
    if (!open) return
    function onClickOutside(e: MouseEvent) {
      if (panelRef.current && !panelRef.current.contains(e.target as Node)) {
        setOpen(false)
      }
    }
    document.addEventListener('mousedown', onClickOutside)
    return () => document.removeEventListener('mousedown', onClickOutside)
  }, [open])

  if (!serverURL) return null

  const { os, arch } = platform
  const availableArches = archOptionsForOS(os)

  const installGlobal = installCmd(os, arch, serverURL, version)
  const installChina = installCmd(os, arch, serverURL, version, CHINA_MIRROR)
  const connect = connectCmd(os, serverURL)

  return (
    <div className="relative" ref={panelRef}>
      <Tooltip>
        <TooltipTrigger asChild>
          <Button
            type="button"
            variant="ghost"
            size="icon-xs"
            aria-label={t('quickConnect')}
            onClick={() => setOpen(!open)}
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
            <span className="text-xs font-medium text-foreground">{t('quickConnectTitle')}</span>
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
              {availableArches.map((a) => (
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

          <CommandRow
            label={t('quickConnectInstall')}
            commands={[
              { key: 'install-global', tag: t('quickConnectGlobal'), text: installGlobal },
              { key: 'install-china', tag: t('quickConnectChina'), text: installChina },
            ]}
            copied={copied}
            onCopy={handleCopy}
          />

          <CommandRow
            label={t('quickConnectOnly')}
            commands={[
              { key: 'connect', text: connect },
            ]}
            copied={copied}
            onCopy={handleCopy}
            className="mt-2"
          />

          <p className="mt-2 text-[10px] text-muted-foreground">
            {t('quickConnectHint')}
          </p>
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

function CommandRow({ label, commands, copied, onCopy, className }: {
  label: string
  commands: CmdEntry[]
  copied: CopiedKey
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
            <CopyButton key={c.key} tag={c.tag} copied={copied === c.key} onClick={() => onCopy(c.key, c.text)} />
          ))}
        </div>
      </div>
    </div>
  )
}

function CopyButton({ tag, copied, onClick }: { tag?: string; copied: boolean; onClick: () => void }) {
  return (
    <button
      type="button"
      onClick={onClick}
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
