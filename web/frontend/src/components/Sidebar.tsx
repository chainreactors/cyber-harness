import { useMemo, useState } from 'react'
import { Shield, PanelLeftClose, PanelLeft, History, Search, X } from 'lucide-react'
import { Button, Badge, Input, Tooltip, TooltipTrigger, TooltipContent } from '@aspect/ui'
import ScanHistory from './ScanHistory'
import type { ScanJob } from '../api'

interface SidebarProps {
  open: boolean
  onToggle: () => void
  scans: ScanJob[]
  activeId?: string
  onSelectScan: (scan: ScanJob) => void
}

export default function Sidebar({ open, onToggle, scans, activeId, onSelectScan }: SidebarProps) {
  const [query, setQuery] = useState('')
  const runningCount = scans.filter(s => s.status === 'running').length
  const normalizedQuery = query.trim().toLowerCase()
  const filteredScans = useMemo(() => {
    if (!normalizedQuery) {
      return scans
    }
    return scans.filter((scan) => scan.target.toLowerCase().includes(normalizedQuery))
  }, [normalizedQuery, scans])

  return (
    <>
      {open && (
        <button
          type="button"
          aria-label="Close sidebar overlay"
          onClick={onToggle}
          className="fixed inset-0 z-30 bg-background/60 backdrop-blur-[1px] md:hidden"
        />
      )}
      <aside
        className={`flex flex-col border-r border-border bg-card/95 backdrop-blur-sm transition-all duration-200 ease-in-out shrink-0 md:bg-card/50 ${
          open
            ? 'fixed inset-y-0 left-0 z-40 w-72 shadow-xl md:relative md:inset-auto md:z-auto md:shadow-none'
            : 'w-12'
        }`}
      >
      <div className={`flex items-center border-b border-border ${open ? 'p-3 gap-3' : 'p-2 flex-col gap-2'}`}>
        {open ? (
          <>
            <Shield className="w-5 h-5 text-cyber-400 shrink-0" />
            <div className="flex-1 min-w-0">
              <h1 className="text-sm font-bold text-cyber-700 dark:text-cyber-400">AIScan</h1>
              <div className="text-[10px] text-muted-foreground">Web console</div>
            </div>
            <Button
              variant="ghost"
              size="icon"
              onClick={onToggle}
              className="h-7 w-7 text-muted-foreground"
              aria-label="Collapse sidebar"
            >
              <PanelLeftClose className="w-4 h-4" />
            </Button>
          </>
        ) : (
          <>
            <Tooltip>
              <TooltipTrigger asChild>
                <button
                  type="button"
                  onClick={onToggle}
                  aria-label="Expand sidebar"
                  className="p-1 rounded-md hover:bg-accent transition-colors"
                >
                  <Shield className="w-5 h-5 text-cyber-700 dark:text-cyber-400" />
                </button>
              </TooltipTrigger>
              <TooltipContent side="right">AIScan</TooltipContent>
            </Tooltip>
          </>
        )}
      </div>

      {open ? (
        <div className="flex-1 overflow-auto p-3 animate-fade-in">
          <div className="flex items-center justify-between mb-3">
            <h2 className="text-xs font-semibold text-muted-foreground uppercase tracking-wider">
              History
            </h2>
            {runningCount > 0 && (
              <Badge className="border-blue-300 bg-blue-500/10 px-1.5 text-[10px] text-blue-700 dark:border-blue-800 dark:bg-blue-900/50 dark:text-blue-400">
                {runningCount} running
              </Badge>
            )}
          </div>
          <div className="relative mb-3">
            <Search className="pointer-events-none absolute left-2.5 top-1/2 h-3.5 w-3.5 -translate-y-1/2 text-muted-foreground" />
            <Input
              value={query}
              onChange={(event) => setQuery(event.target.value)}
              placeholder="Search targets"
              aria-label="Search scan targets"
              className="h-8 pl-8 pr-8 text-xs"
            />
            {query && (
              <button
                type="button"
                aria-label="Clear target search"
                onClick={() => setQuery('')}
                className="absolute right-1.5 top-1/2 inline-flex h-5 w-5 -translate-y-1/2 items-center justify-center rounded text-muted-foreground hover:bg-accent hover:text-foreground"
              >
                <X className="h-3 w-3" />
              </button>
            )}
          </div>
          <ScanHistory
            scans={filteredScans}
            activeId={activeId}
            onSelect={onSelectScan}
            emptyMessage={normalizedQuery ? 'No matching targets.' : 'No scans yet.'}
          />
        </div>
      ) : (
        <div className="flex flex-col items-center gap-2 pt-3">
          <Tooltip>
            <TooltipTrigger asChild>
              <button
                type="button"
                onClick={onToggle}
                aria-label={`${scans.length} scans in history`}
                className="p-1.5 rounded-md hover:bg-accent transition-colors relative"
              >
                <History className="w-4 h-4 text-muted-foreground" />
                {scans.length > 0 && (
                  <span className="absolute -top-0.5 -right-0.5 w-3.5 h-3.5 bg-cyber-600 rounded-full text-[8px] font-bold flex items-center justify-center text-white">
                    {scans.length > 9 ? '9+' : scans.length}
                  </span>
                )}
              </button>
            </TooltipTrigger>
            <TooltipContent side="right">{`${scans.length} scans`}</TooltipContent>
          </Tooltip>
          <Tooltip>
            <TooltipTrigger asChild>
              <Button
                variant="ghost"
                size="icon"
                onClick={onToggle}
                className="h-7 w-7 text-muted-foreground"
                aria-label="Expand sidebar"
              >
                <PanelLeft className="w-3.5 h-3.5" />
              </Button>
            </TooltipTrigger>
            <TooltipContent side="right">Expand sidebar</TooltipContent>
          </Tooltip>
        </div>
      )}
      </aside>
    </>
  )
}
