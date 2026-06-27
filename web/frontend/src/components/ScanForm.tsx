import { useEffect, useState, type ReactNode } from 'react'
import { Brain, Crosshair, Loader2, Play, Radar, Search } from 'lucide-react'
import type { ScanOptions } from '../api'
import { Input, Button, ToggleGroup, ToggleGroupItem } from '@aspect/ui'
import { cn } from '@aspect/theme'

interface ScanFormProps {
  onSubmit: (target: string, mode: string, options: ScanOptions) => void
  disabled: boolean
  analysisAvailable: boolean
  status?: ReactNode
  actions?: ReactNode
}

export default function ScanForm({ onSubmit, disabled, analysisAvailable, status, actions }: ScanFormProps) {
  const [target, setTarget] = useState('')
  const [mode, setMode] = useState('quick')
  const [options, setOptions] = useState<ScanOptions>({ verify: false, sniper: false, deep: false })

  useEffect(() => {
    if (!analysisAvailable) {
      setOptions({ verify: false, sniper: false, deep: false })
    }
  }, [analysisAvailable])

  const handleSubmit = (e: React.FormEvent) => {
    e.preventDefault()
    if (!target.trim()) return
    onSubmit(target.trim(), mode, options)
  }

  const toggleOption = (key: keyof ScanOptions) => {
    setOptions((current) => ({ ...current, [key]: !current[key] }))
  }

  const analysisOptions: Array<{
    key: keyof ScanOptions
    label: string
    icon: ReactNode
    activeClass: string
  }> = [
    {
      key: 'verify',
      label: 'Verify',
      icon: <Brain className="h-4 w-4" />,
      activeClass: 'border-cyber-500/40 bg-cyber-500/15 text-cyber-700 dark:text-cyber-300',
    },
    {
      key: 'sniper',
      label: 'Sniper',
      icon: <Crosshair className="h-4 w-4" />,
      activeClass: 'border-red-400/40 bg-red-400/15 text-red-700 dark:text-red-300',
    },
    {
      key: 'deep',
      label: 'Deep',
      icon: <Radar className="h-4 w-4" />,
      activeClass: 'border-yellow-400/40 bg-yellow-400/15 text-yellow-700 dark:text-yellow-300',
    },
  ]

  return (
    <form
      onSubmit={handleSubmit}
      className="grid w-full grid-cols-[minmax(0,1fr)_auto] items-center gap-2 sm:flex sm:flex-wrap sm:gap-3"
    >
      <div className="relative col-start-1 row-start-1 min-w-0 sm:min-w-[16rem] sm:flex-1">
        <Search className="absolute left-3 top-1/2 -translate-y-1/2 w-4 h-4 text-muted-foreground" />
        <Input
          value={target}
          onChange={(e) => setTarget(e.target.value)}
          placeholder="Target — IP, hostname, or URL"
          disabled={disabled}
          autoFocus
          aria-label="Scan target"
          className="pl-9 h-10 font-mono text-sm bg-secondary/50 border-border focus-visible:ring-cyber-500/50"
        />
      </div>

      <div className="col-span-full row-start-2 flex min-w-0 items-center gap-2 sm:contents">
        <ToggleGroup value={mode} onValueChange={setMode} disabled={disabled} ariaLabel="Scan mode">
          <ToggleGroupItem value="quick">Quick</ToggleGroupItem>
          <ToggleGroupItem value="full">Full</ToggleGroupItem>
        </ToggleGroup>

        <div className="inline-flex min-w-0 items-center gap-1.5 sm:gap-2">
          {analysisOptions.map((item) => {
            const active = options[item.key]
            const optionDisabled = disabled || !analysisAvailable
            return (
              <button
                key={item.key}
                type="button"
                aria-pressed={active}
                aria-label={`${item.label} analysis`}
                title={analysisAvailable ? item.label : 'LLM Offline'}
                disabled={optionDisabled}
                onClick={() => toggleOption(item.key)}
                className={cn(
                  'inline-flex h-10 w-10 shrink-0 items-center justify-center gap-2 rounded-md border px-0 text-xs font-medium transition-colors sm:w-auto sm:px-3',
                  active ? item.activeClass : 'border-input bg-secondary/50 text-muted-foreground hover:text-foreground',
                  optionDisabled && 'cursor-not-allowed opacity-50',
                )}
              >
                {item.icon}
                <span className="hidden sm:inline">{item.label}</span>
              </button>
            )
          })}
        </div>

        <Button
          type="submit"
          disabled={disabled || !target.trim()}
          aria-label={disabled ? 'Scanning target' : 'Start scan'}
          className="h-10 w-10 shrink-0 px-0 bg-cyber-600 text-white hover:bg-cyber-500 sm:w-auto sm:px-5"
        >
          {disabled ? (
            <Loader2 className="w-4 h-4 animate-spin" />
          ) : (
            <Play className="w-4 h-4" />
          )}
          <span className="hidden sm:inline">{disabled ? 'Scanning' : 'Scan'}</span>
        </Button>
      </div>

      <div className="col-start-2 row-start-1 flex items-center justify-end gap-2 sm:contents">
        {status}
        {actions}
      </div>
    </form>
  )
}
