import { createContext, useContext, useEffect, useMemo, useState, type MouseEvent, type ReactNode } from 'react'
import { useTranslation } from 'react-i18next'
import { AlertCircle, Brain, CheckCircle2, ChevronRight, CornerDownRight, Crosshair, File, FileCode2, Fingerprint, Folder, FolderOpen, Globe, Link2, Network, Radar, Server } from 'lucide-react'
import type { AssetItem, ScanResult } from '../api'
import {
  assetItemContent,
  buildResultModel,
  buildSitemapTree,
  collectSitemapFolderIDs,
  contentToken,
  defaultOpenSitemapNodes,
  endpointFileName,
  findingTargetURL,
  isSCOModel,
  itemFactValues,
  itemFacts,
  itemKindTone,
  itemStateTone,
  itemTitle,
  isAnalysisItem,
  pathIdentity,
  pathSearch,
  redirectTarget,
  sameTarget,
  serviceAIStatus,
  statusCodeTone,
  tagBadges,
  type BadgeTone,
  type HostGroup,
  type SCOHostGroup,
  type SCOMetrics,
  type SCOPortNode,
  type SCOResultModel,
  type ServiceNode,
  type SitemapNode,
  type ViewAsset,
} from '../lib/scan-result'
import type { Ip, Port, App, Url, Framework, Vuln } from '@cyber/cstx-easm'
import { cn } from '@cyber/theme'
import { MarkdownContent } from '@/markdown'
import { badgeToneClass } from '../lib/tones'
import { Badge as UIBadge, Button, EmptyState, Tooltip, TooltipContent, TooltipTrigger } from '@cyber/ui'
import { AiPanel } from '@/components/AiPanel'

const AnchorPrefixContext = createContext('')

interface AssetResultViewProps {
  result: ScanResult
  anchorPrefix?: string
}

type AssetPanel = {
  id: string
  labelKey: string
  count?: number
  preferred?: boolean
  render: () => ReactNode
}

export default function AssetResultView({ result, anchorPrefix = '' }: AssetResultViewProps) {
  const { t } = useTranslation('findings')
  const model = useMemo(() => buildResultModel(result), [result])

  if (isSCOModel(model)) {
    return <SCOView model={model} anchorPrefix={anchorPrefix} />
  }

  return (
    <AnchorPrefixContext.Provider value={anchorPrefix}>
      <div>
        <Section title={t('hosts')}>
          {model.hosts.length > 0 ? (
            <HostList hosts={model.hosts} />
          ) : (
            <EmptyState compact title={t('noHosts')} />
          )}
        </Section>
      </div>
    </AnchorPrefixContext.Provider>
  )
}

function HostList({ hosts }: { hosts: HostGroup[] }) {
  return (
    <div className="divide-y divide-border/70">
      {hosts.map((host) => (
        <HostPanel key={host.id} host={host} />
      ))}
    </div>
  )
}

function HostPanel({ host }: { host: HostGroup }) {
  const { t } = useTranslation('findings')
  const anchorPrefix = useContext(AnchorPrefixContext)
  const [open, setOpen] = useState(true)
  const anchor = assetAnchor(anchorPrefix, 'host', host.id)

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
            <span className="break-all font-mono text-sm font-semibold text-foreground">{host.host}</span>
            <AnchorLink id={anchor} label={t('linkTo', { name: host.host })} />
          </div>
        </div>
      </summary>

      <div className="ml-6 mt-3 border-l border-border/70 pl-3">
        <ServiceList services={host.services} />
      </div>
    </details>
  )
}

function ServiceList({ services }: { services: ServiceNode[] }) {
  return (
    <div className="divide-y divide-border/60">
      {services.map((service) => (
        <ServiceRow key={service.id} service={service} />
      ))}
    </div>
  )
}

function ServiceRow({ service }: { service: ServiceNode }) {
  const { t } = useTranslation('findings')
  const anchorPrefix = useContext(AnchorPrefixContext)
  const panels = useMemo(() => servicePanels(service), [service])
  const [open, setOpen] = useState(true)
  const [activePanelID, setActivePanelID] = useState(() => defaultPanelID(panels))
  const activePanel = panels.find((panel) => panel.id === activePanelID) || panels[0]
  const showPanelTabs = panels.length > 1
  const anchor = assetAnchor(anchorPrefix, 'service', service.id)

  useEffect(() => {
    if (!panels.some((panel) => panel.id === activePanelID)) {
      setActivePanelID(defaultPanelID(panels))
    }
  }, [activePanelID, panels])

  const selectPanel = (panelID: string) => (event: MouseEvent<HTMLButtonElement>) => {
    event.preventDefault()
    event.stopPropagation()
    setActivePanelID(panelID)
    setOpen(true)
  }

  if (panels.length === 0) {
    return (
      <div id={anchor} className="scroll-mt-24 py-3 first:pt-0 last:pb-0">
        <ServiceLine service={service} />
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
        <ServiceLine service={service} expandable />
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

function ServiceLine({
  service,
  expandable = false,
}: {
  service: ServiceNode
  expandable?: boolean
}) {
  const { t } = useTranslation('findings')
  const anchorPrefix = useContext(AnchorPrefixContext)
  const displayTarget = service.web ? service.asset.target : service.target
  const aiStatus = serviceAIStatus(service)
  const port = service.port && service.port.toLowerCase() !== (service.service || service.protocol).toLowerCase()
    ? service.port
    : '—'

  return (
    <div className="min-w-0">
      <div className="flex min-w-0 items-start gap-2">
        {expandable ? (
          <ChevronRight className="mt-1 h-3.5 w-3.5 shrink-0 text-muted-foreground transition-transform group-open/service:rotate-90" />
        ) : (
          <span className="h-3.5 w-3.5 shrink-0" />
        )}
        <span className="w-12 shrink-0 break-words font-mono text-sm font-semibold leading-5 text-foreground">
          {port}
        </span>
        <div className="min-w-0 flex-1">
          <div className="flex min-w-0 flex-wrap items-center gap-x-2 gap-y-1">
            <ServiceIcon service={service} />
            <span className="font-medium text-foreground">{service.service || service.protocol || 'service'}</span>
            <AnchorLink id={assetAnchor(anchorPrefix, 'service', service.id)} label={t('linkTo', { name: service.target || service.service || service.port })} />
            {service.protocol && service.protocol !== service.service && <Badge>{service.protocol}</Badge>}
            {service.web && service.pathCount === 0 && <span className="text-[11px] text-muted-foreground">{t('web')}</span>}
            {aiStatus === 'verified' && (
              <UIBadge size="sm" variant="success"><CheckCircle2 className="h-3 w-3" />{t('aiVerified')}</UIBadge>
            )}
            {aiStatus === 'sniper' && (
              <UIBadge size="sm" variant="danger"><Crosshair className="h-3 w-3" />{t('cveIntel')}</UIBadge>
            )}
            {aiStatus === 'deep' && (
              <UIBadge size="sm" variant="warning"><Radar className="h-3 w-3" />{t('deepTest')}</UIBadge>
            )}
            {service.title && (
              <span className="min-w-0 break-words text-xs text-muted-foreground">{service.title}</span>
            )}
          </div>
          <div className="mt-1 flex min-w-0 flex-wrap items-center gap-x-2 gap-y-1 text-[11px] text-muted-foreground">
            {displayTarget && <span className="break-all font-mono">{displayTarget}</span>}
            {service.summary && <span className="break-words">{service.summary}</span>}
            {service.pathCount === 0 && service.statuses.slice(0, 5).map((status) => (
              <Badge key={`http:${status}`} tone={statusCodeTone(status)}>{status}</Badge>
            ))}
            {service.states.slice(0, 3).map((state) => (
              <Badge key={`state:${state}`} tone={itemStateTone(state)}>{state}</Badge>
            ))}
            <FingerChips fingers={service.fingers} />
          </div>
        </div>
      </div>
    </div>
  )
}

function ServiceIcon({ service }: { service: ServiceNode }) {
  if (service.web) {
    return <Globe className="h-3.5 w-3.5 shrink-0 text-primary" />
  }
  if (service.fingers.length > 0) {
    return <Fingerprint className="h-3.5 w-3.5 shrink-0 text-warning" />
  }
  return <Server className="h-3.5 w-3.5 shrink-0 text-muted-foreground" />
}

function servicePanels(service: ServiceNode): AssetPanel[] {
  const panels: AssetPanel[] = []
  if (service.paths.length > 0) {
    panels.push({
      id: 'sitemap',
      labelKey: 'sitemap',
      count: service.paths.length,
      preferred: true,
      render: () => <SitemapBlock items={service.paths} />,
    })
  }
  if (service.analysisItems.length > 0) {
    panels.push({
      id: 'analysis',
      labelKey: 'analysis',
      count: service.analysisItems.length,
      render: () => <AssetItemsBlock asset={service.asset} items={service.analysisItems} />,
    })
  }
  return panels
}

function defaultPanelID(panels: AssetPanel[]) {
  return panels.find((panel) => panel.preferred)?.id || panels[0]?.id || ''
}

function ItemFactLine({ item, search, className }: { item: AssetItem; search?: string; className?: string }) {
  const facts = itemFacts(item)
  if (facts.statuses.length === 0 && facts.states.length === 0 && facts.fingers.length === 0 && !search) {
    return null
  }
  return (
    <div className={cn('flex min-w-0 flex-wrap items-center gap-x-2 gap-y-1 text-[11px]', className)}>
      {facts.statuses.map((status) => (
        <Badge key={`http:${status}`} tone={statusCodeTone(status)}>{status}</Badge>
      ))}
      {facts.states.map((state) => (
        <Badge key={`state:${state}`} tone={itemStateTone(state)}>{state}</Badge>
      ))}
      <FingerChips fingers={facts.fingers} />
      {search && <span className="break-all font-mono text-muted-foreground">{search}</span>}
    </div>
  )
}

function AssetItemsBlock({ asset, items }: { asset: ViewAsset; items: AssetItem[] }) {
  return (
    <div className="space-y-2">
      {items.map((item, idx) => (
        <AssetItemRow key={`${item.kind}-${item.source}-${item.target}-${item.title}-${idx}`} item={item} asset={asset} />
      ))}
    </div>
  )
}

function AssetItemRow({ item, asset }: { item: AssetItem; asset: ViewAsset }) {
  const { t } = useTranslation('findings')
  const anchorPrefix = useContext(AnchorPrefixContext)
  const markdown = isAnalysisItem(item)
  const title = markdown ? firstText(item.summary, item.title) : itemTitle(item)
  const detail = itemContent(item)
  const anchor = assetAnchor(anchorPrefix, 'item', itemAnchorValue(item, asset))
  const showTarget = item.target && !sameTarget(item.target, asset.target)
  const headerBadges = [
    { id: `kind:${item.kind}`, label: item.kind, tone: itemKindTone(item.kind) },
  ]
  const tags = tagBadges(item.tags, [...headerBadges.map((badge) => badge.label), ...itemFactValues(item)])
  const isAI = item.source === 'verify' || item.source === 'sniper' || item.source === 'deep'

  return (
    <div id={anchor} className={cn(
      'scroll-mt-24 rounded-md border p-3 text-xs',
      isAI && item.status === 'confirmed' && 'border-l-4 border-l-success',
      isAI && item.source === 'sniper' && 'border-l-4 border-l-destructive',
      isAI && item.source === 'deep' && 'border-l-4 border-l-warning',
      item.kind === 'error'
        ? 'border-destructive/20 bg-destructive/10'
        : item.kind === 'loot'
          ? 'border-destructive/20 bg-destructive/5'
          : 'border-border/70 bg-background/30',
    )}>
      <div className="flex flex-wrap items-center gap-2">
        <ItemIcon kind={item.kind} />
        {headerBadges.map((badge) => (
          <Badge key={badge.id} tone={badge.tone}>{badge.label}</Badge>
        ))}
        <VerificationBadge source={item.source} status={item.status} />
        <AnchorLink id={anchor} label={t('linkTo', { name: title || item.kind })} />
        {showTarget && <span className="break-all font-mono text-muted-foreground">{item.target}</span>}
      </div>
      {title && <div className="mt-1 break-words text-foreground">{title}</div>}
      <ItemFactLine item={item} className="mt-2" />
      {detail && (
        isAI ? (
          <AiPanel
            label={item.source === 'verify' ? t('aiVerification') : item.source === 'sniper' ? t('cveIntelligence') : t('dynamicAnalysis')}
            className="mt-2 max-h-96 overflow-auto"
          >
            {markdown ? (
              <MarkdownContent content={detail} compact muted />
            ) : (
              <div className="whitespace-pre-wrap">{detail}</div>
            )}
          </AiPanel>
        ) : (
          <div className="mt-2 max-h-96 overflow-auto rounded-md border border-border bg-background/50 p-3 text-muted-foreground">
            {markdown ? (
              <MarkdownContent content={detail} compact muted />
            ) : (
              <div className="whitespace-pre-wrap">{detail}</div>
            )}
          </div>
        )
      )}
      {tags.length > 0 && (
        <div className="mt-2 flex flex-wrap gap-1.5">
          {tags.map((badge) => (
            <Badge key={badge.id} tone={badge.tone}>{badge.label}</Badge>
          ))}
        </div>
      )}
    </div>
  )
}

function VerificationBadge({ source, status }: { source?: string; status?: string }) {
  const { t } = useTranslation('findings')
  if (source === 'verify') {
    if (status === 'confirmed') {
      return <UIBadge size="sm" variant="success"><CheckCircle2 className="h-3 w-3" />{t('confirmed')}</UIBadge>
    }
    if (status === 'not_confirmed') {
      return <UIBadge size="sm" variant="muted">{t('notConfirmed')}</UIBadge>
    }
    if (status === 'inconclusive') {
      return <UIBadge size="sm" variant="warning">{t('inconclusive')}</UIBadge>
    }
    return <UIBadge size="sm" variant="info">{t('info')}</UIBadge>
  }
  if (source === 'sniper') {
    return <UIBadge size="sm" variant="danger"><Crosshair className="h-3 w-3" />{t('cveIntel')}</UIBadge>
  }
  if (source === 'deep') {
    return <UIBadge size="sm" variant="warning"><Radar className="h-3 w-3" />{t('deepTest')}</UIBadge>
  }
  return null
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

function itemAnchorValue(item: AssetItem, asset: ViewAsset) {
  return [
    asset.key,
    item.kind,
    item.source,
    item.target,
    item.status,
    item.title,
    item.summary,
  ].filter(Boolean).join('|')
}

function anchorSlug(value: string) {
  const slug = value
    .trim()
    .toLowerCase()
    .replace(/<[^>]*>/g, '')
    .replace(/&[a-z0-9#]+;/g, '')
    .replace(/[^a-z0-9\u4e00-\u9fa5]+/g, '-')
    .replace(/^-+|-+$/g, '')

  return (slug || 'section').slice(0, 96)
}

function itemContent(item: AssetItem) {
  return assetItemContent(item)
}

function firstText(...values: Array<string | undefined>) {
  return values.find((value) => value && value.trim())?.trim() || ''
}

function ItemIcon({ kind }: { kind: string }) {
  if (kind === 'loot') {
    return <AlertCircle className="h-3.5 w-3.5 text-destructive" />
  }
  if (kind === 'note' || kind === 'response') {
    return <Brain className="h-3.5 w-3.5 text-ai" />
  }
  if (kind === 'fingerprint') {
    return <Fingerprint className="h-3.5 w-3.5 text-warning" />
  }
  return <Server className="h-3.5 w-3.5 text-muted-foreground" />
}

function SitemapBlock({ items }: { items: AssetItem[] }) {
  const { t } = useTranslation('findings')
  const tree = useMemo(() => buildSitemapTree(items), [items])
  const folderIDs = useMemo(() => collectSitemapFolderIDs(tree), [tree])
  const [openIDs, setOpenIDs] = useState<Set<string>>(() => defaultOpenSitemapNodes(tree))

  useEffect(() => {
    setOpenIDs(defaultOpenSitemapNodes(tree))
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
          <SitemapTreeNode
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

function SitemapTreeNode({
  node,
  depth,
  openIDs,
  onToggle,
}: {
  node: SitemapNode
  depth: number
  openIDs: Set<string>
  onToggle: (id: string) => void
}) {
  const isFolder = node.children.length > 0
  const isOpen = openIDs.has(node.id)
  const paddingLeft = `${0.6 + Math.min(depth, 4) * 1.15}rem`
  const count = node.children.length + node.items.length

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
            {node.items.map((item, idx) => (
              <EndpointFile key={`${pathIdentity(item)}:${idx}`} item={item} depth={depth + 1} />
            ))}
            {node.children.map((child) => (
              <SitemapTreeNode
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
      {node.items.map((item, idx) => (
        <EndpointFile key={`${pathIdentity(item)}:${idx}`} item={item} depth={depth} />
      ))}
    </>
  )
}

function EndpointFile({ item, depth }: { item: AssetItem; depth: number }) {
  const { t } = useTranslation('findings')
  const paddingLeft = `${0.6 + Math.min(depth, 4) * 1.15}rem`
  // endpointFileName folds the query onto the basename; strip it so the search
  // string can render dimmed on its own, matching the deck's sitemap tree.
  const filename = endpointFileName(item).split('?')[0] || '/'
  const search = pathSearch(item)
  const status = pathStatus(item)
  const token = contentToken(item)
  const redirect = redirectTarget(item)
  const url = findingTargetURL(item.target) || findingTargetURL(endpointDataURL(item))
  const isJS = token === 'JS'

  const body = (
    <>
      {isJS ? (
        <FileCode2 className="h-3.5 w-3.5 shrink-0 text-warning" />
      ) : (
        <File className="h-3.5 w-3.5 shrink-0 text-muted-foreground" />
      )}
      <span className="min-w-0 flex-1 truncate font-mono text-foreground">
        {filename}
        {search && <span className="text-muted-foreground">{search}</span>}
      </span>
      {item.title && <span className="hidden min-w-0 truncate text-muted-foreground sm:inline">{item.title}</span>}
      {redirect && (
        <span title={`→ ${redirect}`} className="shrink-0 text-muted-foreground/60">
          <CornerDownRight className="h-3 w-3" />
          <span className="sr-only">{t('redirectsTo', { url: redirect })}</span>
        </span>
      )}
      {token && (
        <span className={cn(
          'shrink-0 rounded-[3px] px-1 py-px font-mono text-[10px] font-semibold uppercase',
          token === 'JS'
            ? 'bg-warning/15 text-warning'
            : token === 'JSON' || token === 'XML'
              ? 'bg-info/15 text-info'
              : 'bg-secondary text-muted-foreground',
        )}>
          {token}
        </span>
      )}
      {status && (
        <span className={cn('shrink-0 rounded-[3px] px-1 py-px font-mono text-[10px] font-semibold', badgeToneClass[statusCodeTone(status)])}>
          {status}
        </span>
      )}
    </>
  )

  const rowClass = 'flex items-center gap-2 py-1.5 pr-3 text-xs hover:bg-secondary/30'
  if (url) {
    return (
      <a
        href={url}
        target="_blank"
        rel="noreferrer"
        title={t('openTargetUrl', { url })}
        className={rowClass}
        style={{ paddingLeft }}
      >
        {body}
      </a>
    )
  }
  return (
    <div className={rowClass} style={{ paddingLeft }}>
      {body}
    </div>
  )
}

function pathStatus(item: AssetItem): string {
  if (item.status) return item.status
  const raw = item.data?.status
  if (typeof raw === 'string') return raw
  if (typeof raw === 'number' && raw > 0) return String(raw)
  return ''
}

function endpointDataURL(item: AssetItem): string | undefined {
  const raw = item.data?.url
  return typeof raw === 'string' ? raw : undefined
}

function FingerChips({ fingers }: { fingers: string[] }) {
  const { t } = useTranslation('findings')
  if (fingers.length === 0) {
    return null
  }

  const visible = fingers.slice(0, 5)
  const hidden = fingers.length - visible.length

  return (
    <Tooltip>
      <TooltipTrigger asChild>
        <span className="inline-flex min-w-0 flex-wrap items-center gap-1 text-warning">
          <Fingerprint className="h-3 w-3 shrink-0" />
          {visible.map((finger) => (
            <Badge key={`finger:${finger}`} tone="yellow">{finger}</Badge>
          ))}
          {hidden > 0 && <Badge tone="yellow">+{hidden}</Badge>}
        </span>
      </TooltipTrigger>
      <TooltipContent>{t('fingerprints')}</TooltipContent>
    </Tooltip>
  )
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

// ─── SCO-native rendering ───────────────────────────────────────────────────────

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

function SCOView({ model, anchorPrefix }: { model: SCOResultModel; anchorPrefix: string }) {
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
            {node.urls.map((url, idx) => (
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
      {node.urls.map((url, idx) => (
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
          <div className="mt-2 max-h-96 overflow-auto rounded-md border border-border bg-background/50 p-3 text-muted-foreground">
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
