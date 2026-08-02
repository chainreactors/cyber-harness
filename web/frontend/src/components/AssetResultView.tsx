import { createContext, useContext, useEffect, useMemo, useState, type MouseEvent, type ReactNode } from 'react'
import { useTranslation } from 'react-i18next'
import { AlertCircle, ChevronRight, CornerDownRight, File, Fingerprint, Folder, FolderOpen, Globe, Link2, Network, Server } from 'lucide-react'
import {
  statusCodeTone,
  type BadgeTone,
  type SCOHostGroup,
  type SCOMetrics,
  type SCOPortNode,
  type SCOResultModel,
} from '../lib/scan-result'
import type { Url, Vuln } from '@cyber/cstx-easm'
import { cn } from '@cyber/theme'
import { badgeToneClass } from '../lib/tones'
import { Badge as UIBadge, Button, EmptyState, Tooltip, TooltipContent, TooltipTrigger } from '@cyber/ui'

const AnchorPrefixContext = createContext('')

interface AssetResultViewProps {
  model: SCOResultModel
  anchorPrefix?: string
}

type AssetPanel = {
  id: string
  labelKey: string
  count?: number
  preferred?: boolean
  render: () => ReactNode
}

// Scan results are SCO-native end to end: the hub persists a completed scan as
// SCO nodes keyed by scan_id, and this view renders the model built from them.
export default function AssetResultView({ model, anchorPrefix = '' }: AssetResultViewProps) {
  const { t } = useTranslation('findings')
  return (
    <AnchorPrefixContext.Provider value={anchorPrefix}>
      <div className="space-y-6">
        <SCOMetricsGrid metrics={model.metrics} />
        <Section title={t('hosts')}>
          {model.hosts.length > 0 ? (
            <SCOHostList hosts={model.hosts} />
          ) : (
            <EmptyState compact title={t('noHosts')} />
          )}
        </Section>
      </div>
    </AnchorPrefixContext.Provider>
  )
}

function SCOMetricsGrid({ metrics }: { metrics: SCOMetrics }) {
  const { t } = useTranslation('findings')
  const cells: Array<{ icon: typeof Network; label: string; value: number }> = [
    { icon: Network, label: t('ips'), value: metrics.ips },
    { icon: Server, label: t('ports'), value: metrics.ports },
    { icon: Globe, label: t('apps'), value: metrics.apps },
    { icon: File, label: t('urls'), value: metrics.urls },
    { icon: Fingerprint, label: t('frameworks'), value: metrics.frameworks },
    { icon: AlertCircle, label: t('vulns'), value: metrics.vulns },
  ]
  return (
    <div className="grid grid-cols-3 gap-3 sm:grid-cols-6">
      {cells.map((cell) => (
        <div key={cell.label} className="rounded-md border border-border/60 bg-muted/10 px-3 py-2 text-center">
          <cell.icon className="mx-auto h-4 w-4 text-muted-foreground" />
          <div className="mt-1 text-lg font-semibold tabular-nums text-foreground">{cell.value}</div>
          <div className="text-[11px] text-muted-foreground">{cell.label}</div>
        </div>
      ))}
    </div>
  )
}

function SCOHostList({ hosts }: { hosts: SCOHostGroup[] }) {
  return (
    <div className="divide-y divide-border/70">
      {hosts.map((host) => (
        <SCOHostPanel key={host.ip.cstx_id} host={host} />
      ))}
    </div>
  )
}

function SCOHostPanel({ host }: { host: SCOHostGroup }) {
  const { t } = useTranslation('findings')
  const anchorPrefix = useContext(AnchorPrefixContext)
  const [open, setOpen] = useState(true)
  const anchor = assetAnchor(anchorPrefix, 'host', host.ip.cstx_id)

  return (
    <details
      id={anchor}
      className="group scroll-mt-24 py-3 first:pt-0 last:pb-0"
      open={open}
      onToggle={(event) => setOpen(event.currentTarget.open)}
    >
      <summary className="flex cursor-pointer list-none items-start gap-2 [&::-webkit-details-marker]:hidden">
        <ChevronRight className="mt-0.5 h-3.5 w-3.5 shrink-0 text-muted-foreground transition-transform group-open:rotate-90" />
        <Network className="mt-0.5 h-3.5 w-3.5 shrink-0 text-primary" />
        <div className="min-w-0 flex-1">
          <div className="flex min-w-0 flex-wrap items-center gap-x-2 gap-y-1">
            <span className="break-all font-mono text-sm font-semibold text-foreground">{host.ip.ip}</span>
            <AnchorLink id={anchor} label={t('linkTo', { name: host.ip.ip })} />
            {host.ip.country && <Badge tone="muted">{host.ip.country}</Badge>}
            {host.ip.cdn_name && <Badge tone="yellow">{host.ip.cdn_name}</Badge>}
          </div>
        </div>
      </summary>

      <div className="ml-6 mt-3 border-l border-border/70 pl-3">
        <div className="divide-y divide-border/60">
          {host.ports.map((node) => (
            <SCOPortRow key={node.port.cstx_id} node={node} />
          ))}
        </div>
      </div>
    </details>
  )
}

function SCOPortRow({ node }: { node: SCOPortNode }) {
  const { t } = useTranslation('findings')
  const anchorPrefix = useContext(AnchorPrefixContext)
  const hasSitemap = node.urls.length > 0
  const hasVulns = node.vulns.length > 0
  const expandable = hasSitemap || hasVulns
  const [open, setOpen] = useState(true)
  const anchor = assetAnchor(anchorPrefix, 'port', node.port.cstx_id)

  const panels = useMemo<AssetPanel[]>(() => {
    const p: AssetPanel[] = []
    if (hasSitemap) {
      p.push({
        id: 'sitemap',
        labelKey: 'sitemap',
        count: node.urls.length,
        preferred: true,
        render: () => <SCOSitemapBlock urls={node.urls} />,
      })
    }
    if (hasVulns) {
      p.push({
        id: 'vulns',
        labelKey: 'vulns',
        count: node.vulns.length,
        preferred: !hasSitemap,
        render: () => <SCOVulnsBlock vulns={node.vulns} />,
      })
    }
    return p
  }, [hasSitemap, hasVulns, node.urls, node.vulns])

  const [activePanelID, setActivePanelID] = useState(() => defaultPanelID(panels))
  const activePanel = panels.find((p) => p.id === activePanelID) || panels[0]
  const showPanelTabs = panels.length > 1

  useEffect(() => {
    if (!panels.some((p) => p.id === activePanelID)) {
      setActivePanelID(defaultPanelID(panels))
    }
  }, [activePanelID, panels])

  const selectPanel = (panelID: string) => (event: MouseEvent<HTMLButtonElement>) => {
    event.preventDefault()
    event.stopPropagation()
    setActivePanelID(panelID)
    setOpen(true)
  }

  if (!expandable) {
    return (
      <div id={anchor} className="scroll-mt-24 py-3 first:pt-0 last:pb-0">
        <SCOPortLine node={node} />
      </div>
    )
  }

  return (
    <details
      id={anchor}
      className="group/service scroll-mt-24 py-3 first:pt-0 last:pb-0"
      open={open}
      onToggle={(event) => setOpen(event.currentTarget.open)}
    >
      <summary className="cursor-pointer list-none [&::-webkit-details-marker]:hidden">
        <SCOPortLine node={node} expandable />
      </summary>

      {showPanelTabs && (
        <div className="mt-2 flex flex-wrap gap-4 border-b border-border/50 sm:ml-6">
          {panels.map((panel) => (
            <TabChip
              key={panel.id}
              active={open && activePanel?.id === panel.id}
              label={t(panel.labelKey)}
              count={panel.count}
              onClick={selectPanel(panel.id)}
            />
          ))}
        </div>
      )}

      {activePanel && (
        <div className="mt-3 sm:ml-6">
          {!showPanelTabs && (
            <div className="mb-2 flex items-center gap-2 text-[11px] font-medium text-muted-foreground">
              <span>{t(activePanel.labelKey)}</span>
              {typeof activePanel.count === 'number' && activePanel.count > 0 && (
                <span className="tabular-nums text-muted-foreground/70">{activePanel.count}</span>
              )}
            </div>
          )}
          {activePanel.render()}
        </div>
      )}
    </details>
  )
}

function SCOPortLine({ node, expandable = false }: { node: SCOPortNode; expandable?: boolean }) {
  return (
    <div className="min-w-0">
      <div className="flex min-w-0 items-start gap-2">
        {expandable ? (
          <ChevronRight className="mt-1 h-3.5 w-3.5 shrink-0 text-muted-foreground transition-transform group-open/service:rotate-90" />
        ) : (
          <span className="h-3.5 w-3.5 shrink-0" />
        )}
        <span className="w-12 shrink-0 break-words font-mono text-sm font-semibold leading-5 text-foreground">
          {node.port.port}
        </span>
        <div className="min-w-0 flex-1">
          <div className="flex min-w-0 flex-wrap items-center gap-x-2 gap-y-1">
            {node.app ? (
              <Globe className="h-3.5 w-3.5 shrink-0 text-primary" />
            ) : node.frameworks.length > 0 ? (
              <Fingerprint className="h-3.5 w-3.5 shrink-0 text-warning" />
            ) : (
              <Server className="h-3.5 w-3.5 shrink-0 text-muted-foreground" />
            )}
            <span className="font-medium text-foreground">{node.port.protocol}</span>
            {node.app?.midware && <Badge tone="muted">{node.app.midware}</Badge>}
            {node.app?.title && (
              <span className="min-w-0 break-words text-xs text-muted-foreground">{node.app.title}</span>
            )}
          </div>
          <div className="mt-1 flex min-w-0 flex-wrap items-center gap-x-2 gap-y-1 text-[11px] text-muted-foreground">
            {node.app?.status_code != null && node.app.status_code > 0 && (
              <Badge tone={statusCodeTone(String(node.app.status_code))}>{node.app.status_code}</Badge>
            )}
            {node.frameworks.map((fw) => (
              <Badge key={fw.cstx_id} tone="yellow">{fw.name}</Badge>
            ))}
          </div>
        </div>
      </div>
    </div>
  )
}

type SCOPathNode = {
  id: string
  name: string
  children: SCOPathNode[]
  urls: Url[]
}

function buildSCOPathTree(urls: Url[]): SCOPathNode[] {
  const nodeMap = new Map<string, SCOPathNode>()

  function getOrCreate(key: string, name: string): SCOPathNode {
    let node = nodeMap.get(key)
    if (!node) {
      node = { id: key, name, children: [], urls: [] }
      nodeMap.set(key, node)
    }
    return node
  }

  function ensurePath(segments: string[]): SCOPathNode {
    if (segments.length === 0) return getOrCreate('/', '/')
    const key = '/' + segments.join('/')
    const node = getOrCreate(key, segments[segments.length - 1])
    const parent = ensurePath(segments.slice(0, -1))
    if (!parent.children.includes(node)) parent.children.push(node)
    return node
  }

  for (const url of urls) {
    const parts = (url.path || '/').split('/').filter(Boolean)
    if (parts.length <= 1) {
      ensurePath([]).urls.push(url)
    } else {
      ensurePath(parts.slice(0, -1)).urls.push(url)
    }
  }

  const root = nodeMap.get('/')
  return root ? [root] : []
}

function collectSCOFolderIDs(nodes: SCOPathNode[]): string[] {
  const ids: string[] = []
  for (const node of nodes) {
    if (node.children.length > 0) {
      ids.push(node.id)
      ids.push(...collectSCOFolderIDs(node.children))
    }
  }
  return ids
}

function scoPathFileName(url: Url): string {
  const parts = (url.path || '/').split('/').filter(Boolean)
  return parts[parts.length - 1] || '/'
}

function severityTone(severity?: string): BadgeTone {
  switch (severity?.toLowerCase()) {
    case 'critical': return 'red'
    case 'high': return 'red'
    case 'medium': return 'yellow'
    case 'low': return 'green'
    case 'info': return 'muted'
    default: return 'muted'
  }
}

function SCOSitemapBlock({ urls }: { urls: Url[] }) {
  const { t } = useTranslation('findings')
  const tree = useMemo(() => buildSCOPathTree(urls), [urls])
  const folderIDs = useMemo(() => collectSCOFolderIDs(tree), [tree])
  const [openIDs, setOpenIDs] = useState<Set<string>>(() => new Set(folderIDs))

  useEffect(() => {
    setOpenIDs(new Set(collectSCOFolderIDs(tree)))
  }, [tree])

  const toggleNode = (id: string) => {
    setOpenIDs((current) => {
      const next = new Set(current)
      if (next.has(id)) {
        next.delete(id)
      } else {
        next.add(id)
      }
      return next
    })
  }

  return (
    <div className="overflow-hidden rounded-md border border-border/60 bg-muted/10">
      {folderIDs.length > 0 && (
        <div className="flex items-center justify-end gap-1 border-b border-border/60 px-2 py-1">
          <IconButton label={t('expandAll')} onClick={() => setOpenIDs(new Set(folderIDs))}>
            <FolderOpen className="h-3.5 w-3.5" />
          </IconButton>
          <IconButton label={t('collapseAll')} onClick={() => setOpenIDs(new Set())}>
            <Folder className="h-3.5 w-3.5" />
          </IconButton>
        </div>
      )}
      <div>
        {tree.map((node) => (
          <SCOPathTreeNode
            key={node.id}
            node={node}
            depth={0}
            openIDs={openIDs}
            onToggle={toggleNode}
          />
        ))}
      </div>
    </div>
  )
}

function SCOPathTreeNode({
  node,
  depth,
  openIDs,
  onToggle,
}: {
  node: SCOPathNode
  depth: number
  openIDs: Set<string>
  onToggle: (id: string) => void
}) {
  const isFolder = node.children.length > 0
  const isOpen = openIDs.has(node.id)
  const paddingLeft = `${0.6 + Math.min(depth, 4) * 1.15}rem`
  const count = node.children.length + node.urls.length
  const urls = node.urls

  if (isFolder) {
    return (
      <div>
        <button
          type="button"
          aria-expanded={isOpen}
          className="flex w-full items-center gap-2 py-1.5 pr-3 text-left text-xs hover:bg-secondary/40"
          style={{ paddingLeft }}
          onClick={() => onToggle(node.id)}
        >
          <ChevronRight className={cn(
            'h-3 w-3 shrink-0 text-muted-foreground transition-transform',
            isOpen && 'rotate-90',
          )} />
          {isOpen ? (
            <FolderOpen className="h-3.5 w-3.5 shrink-0 text-primary/80" />
          ) : (
            <Folder className="h-3.5 w-3.5 shrink-0 text-muted-foreground" />
          )}
          <span className="min-w-0 flex-1 truncate font-mono text-foreground">{node.name}</span>
          <span className="shrink-0 text-muted-foreground">{count}</span>
        </button>
        {isOpen && (
          <div>
            {urls.map((url, idx) => (
              <SCOUrlEntry key={`${url.cstx_id}:${idx}`} url={url} depth={depth + 1} />
            ))}
            {node.children.map((child) => (
              <SCOPathTreeNode
                key={child.id}
                node={child}
                depth={depth + 1}
                openIDs={openIDs}
                onToggle={onToggle}
              />
            ))}
          </div>
        )}
      </div>
    )
  }

  return (
    <>
      {urls.map((url, idx) => (
        <SCOUrlEntry key={`${url.cstx_id}:${idx}`} url={url} depth={depth} />
      ))}
    </>
  )
}

function SCOUrlEntry({ url, depth }: { url: Url; depth: number }) {
  const paddingLeft = `${0.6 + Math.min(depth, 4) * 1.15}rem`
  const filename = scoPathFileName(url)
  const statusCode = url.status_code != null && url.status_code > 0 ? String(url.status_code) : ''

  return (
    <div
      className="flex items-center gap-2 py-1.5 pr-3 text-xs hover:bg-secondary/30"
      style={{ paddingLeft }}
    >
      <File className="h-3.5 w-3.5 shrink-0 text-muted-foreground" />
      <span className="min-w-0 flex-1 truncate font-mono text-foreground">{filename}</span>
      {url.title && <span className="hidden min-w-0 truncate text-muted-foreground sm:inline">{url.title}</span>}
      {url.redirect_url && (
        <span title={`-> ${url.redirect_url}`} className="shrink-0 text-muted-foreground/60">
          <CornerDownRight className="h-3 w-3" />
        </span>
      )}
      {url.content_type && (
        <span className="shrink-0 rounded-[3px] px-1 py-px font-mono text-[10px] font-semibold uppercase bg-secondary text-muted-foreground">
          {url.content_type}
        </span>
      )}
      {statusCode && (
        <span className={cn('shrink-0 rounded-[3px] px-1 py-px font-mono text-[10px] font-semibold', badgeToneClass[statusCodeTone(statusCode)])}>
          {statusCode}
        </span>
      )}
    </div>
  )
}

function SCOVulnsBlock({ vulns }: { vulns: Vuln[] }) {
  return (
    <div className="space-y-2">
      {vulns.map((vuln, idx) => (
        <SCOVulnEntry key={`${vuln.cstx_id}:${idx}`} vuln={vuln} />
      ))}
    </div>
  )
}

function SCOVulnEntry({ vuln }: { vuln: Vuln }) {
  const { t } = useTranslation('findings')
  const hasDetail = Boolean(vuln.request || vuln.response)
  const tone = severityTone(vuln.severity)
  const isWeakpass = Boolean(vuln.username)
  const displayName = vuln.vuln_id || vuln.name || vuln.value

  return (
    <div className={cn(
      'rounded-md border p-3 text-xs',
      vuln.severity?.toLowerCase() === 'critical' || vuln.severity?.toLowerCase() === 'high'
        ? 'border-destructive/20 bg-destructive/5'
        : 'border-border/70 bg-background/30',
    )}>
      <div className="flex flex-wrap items-center gap-2">
        <AlertCircle className="h-3.5 w-3.5 text-destructive" />
        {vuln.severity && <Badge tone={tone}>{vuln.severity}</Badge>}
        <span className="break-all font-mono text-sm font-medium text-foreground">{displayName}</span>
        {vuln.pocname && <Badge tone="muted">{vuln.pocname}</Badge>}
      </div>
      {isWeakpass && (
        <div className="mt-1 flex flex-wrap items-center gap-x-2 gap-y-1 text-[11px] text-muted-foreground">
          <span className="font-mono">{vuln.username}</span>
          {vuln.password && (
            <>
              <span>/</span>
              <span className="font-mono">{vuln.password}</span>
            </>
          )}
        </div>
      )}
      {hasDetail && (
        <details className="mt-2">
          <summary className="cursor-pointer text-[11px] text-muted-foreground hover:text-foreground">
            {t('details')}
          </summary>
          <div className="mt-2 max-h-96 overflow-auto rounded-md border border-border/50 bg-background/50 p-3 text-muted-foreground">
            {vuln.request && (
              <div>
                <div className="mb-1 text-[10px] font-semibold uppercase text-muted-foreground/70">Request</div>
                <pre className="whitespace-pre-wrap font-mono text-[11px]">{vuln.request}</pre>
              </div>
            )}
            {vuln.response && (
              <div className={vuln.request ? 'mt-3' : ''}>
                <div className="mb-1 text-[10px] font-semibold uppercase text-muted-foreground/70">Response</div>
                <pre className="whitespace-pre-wrap font-mono text-[11px]">{vuln.response}</pre>
              </div>
            )}
          </div>
        </details>
      )}
    </div>
  )
}

function defaultPanelID(panels: AssetPanel[]) {
  return panels.find((panel) => panel.preferred)?.id || panels[0]?.id || ''
}

function AnchorLink({ id, label }: { id: string; label: string }) {
  return (
    <a
      href={`#${id}`}
      aria-label={label}
      title={label}
      onClick={(event) => event.stopPropagation()}
      className="inline-flex h-4 w-4 shrink-0 items-center justify-center rounded text-muted-foreground opacity-60 hover:bg-accent hover:text-foreground hover:opacity-100"
    >
      <Link2 className="h-3 w-3" />
    </a>
  )
}

function assetAnchor(namespace: string, prefix: string, value: string) {
  return ['asset', namespace && anchorSlug(namespace), prefix, anchorSlug(value)].filter(Boolean).join('-')
}

function anchorSlug(value: string) {
  const slug = value
    .trim()
    .toLowerCase()
    .replace(/<[^>]*>/g, '')
    .replace(/&[a-z0-9#]+;/g, '')
    .replace(/[^a-z0-9一-龥]+/g, '-')
    .replace(/^-+|-+$/g, '')

  return (slug || 'section').slice(0, 96)
}

function IconButton({
  children,
  label,
  onClick,
}: {
  children: ReactNode
  label: string
  onClick: () => void
}) {
  return (
    <Tooltip>
      <TooltipTrigger asChild>
        <Button
          type="button"
          variant="ghost"
          size="icon-xs"
          aria-label={label}
          onClick={onClick}
          className="text-muted-foreground"
        >
          {children}
        </Button>
      </TooltipTrigger>
      <TooltipContent>{label}</TooltipContent>
    </Tooltip>
  )
}

function TabChip({
  active,
  count,
  label,
  onClick,
}: {
  active: boolean
  count?: number
  label: string
  onClick: (event: MouseEvent<HTMLButtonElement>) => void
}) {
  return (
    <button
      type="button"
      aria-pressed={active}
      onClick={onClick}
      className={cn(
        '-mb-px inline-flex items-center gap-1 border-b-2 px-0 py-1.5 text-[11px] font-medium transition-colors',
        active
          ? 'border-primary text-foreground'
          : 'border-transparent text-muted-foreground hover:text-foreground',
      )}
    >
      {label}
      {typeof count === 'number' && count > 0 && <span className="opacity-70">{count}</span>}
    </button>
  )
}

function Section({ title, children }: { title: string; children: ReactNode }) {
  return (
    <section>
      <h4 className="border-b border-border/60 pb-2 text-xs font-semibold text-foreground">{title}</h4>
      <div className="pt-1">{children}</div>
    </section>
  )
}

function Badge({ children, tone = 'muted' }: { children: ReactNode; tone?: BadgeTone }) {
  // Compact chip shape now comes from the cyber-ui Badge (size="sm"); the tone →
  // token colour map stays here in the domain layer (lib/tones.ts BadgeTone).
  return (
    <UIBadge size="sm" className={cn('border-transparent', badgeToneClass[tone])}>
      {children}
    </UIBadge>
  )
}
