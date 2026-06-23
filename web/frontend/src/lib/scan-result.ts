import type { Asset, AssetItem, Loot, ScanResult } from '../api'

export const assetItemKind = {
  service: 'service',
  path: 'path',
  fingerprint: 'fingerprint',
  loot: 'loot',
  note: 'note',
  response: 'response',
  error: 'error',
} as const

export type ViewAsset = Asset & {
  items: AssetItem[]
}

export type BadgeTone = 'muted' | 'cyan' | 'yellow' | 'green' | 'red'

export type BadgeSpec = {
  id: string
  label: string
  tone?: BadgeTone
}

export type ResultMetrics = {
  assets: number
  hosts: number
  services: number
  web: number
  probes: number
  fingers: number
  loots: number
  errors: number
  duration: string
}

export type ResultModel = {
  hosts: HostGroup[]
  metrics: ResultMetrics
}

export type HostGroup = {
  id: string
  host: string
  services: ServiceNode[]
}

export type ServiceNode = {
  id: string
  asset: ViewAsset
  host: string
  port: string
  protocol: string
  service: string
  target: string
  title: string
  summary: string
  sources: string[]
  states: string[]
  statuses: string[]
  fingers: string[]
  paths: AssetItem[]
  analysisItems: AssetItem[]
  detailItems: AssetItem[]
  pathCount: number
  web: boolean
}

export type ItemFacts = {
  statuses: string[]
  states: string[]
  fingers: string[]
  sources: string[]
}

export type SitemapNode = {
  id: string
  name: string
  path: string
  children: SitemapNode[]
  items: AssetItem[]
}

export function buildResultModel(result: ScanResult): ResultModel {
  const assets = normalizeAssets(result.assets || [])
  const hosts = buildHostGroups(assets)

  return {
    hosts,
    metrics: {
      assets: assets.length,
      hosts: hosts.length,
      services: result.summary.services,
      web: result.summary.webs,
      probes: result.summary.probes,
      fingers: countFingerprints(assets),
      loots: result.summary.loots || result.loots?.length || countLootItems(assets),
      errors: result.summary.errors,
      duration: result.summary.duration,
    },
  }
}

export function normalizeAssets(assets: Asset[]): ViewAsset[] {
  return assets.map((asset) => ({
    ...asset,
    id: asset.id || `asset:${asset.key || asset.target || 'scan'}`,
    key: asset.key || canonicalKey(asset.target || 'scan'),
    target: asset.target || asset.key || 'Scan',
    items: asset.items || [],
  }))
}

export function buildHostGroups(assets: ViewAsset[]): HostGroup[] {
  const groups = new Map<string, HostGroup>()

  for (const asset of assets) {
    const service = serviceNode(asset)
    const key = canonicalKey(service.host || 'Scan')
    let group = groups.get(key)

    if (!group) {
      group = { id: `host:${key}`, host: service.host || 'Scan', services: [] }
      groups.set(key, group)
    }

    group.services.push(service)
  }

  return Array.from(groups.values())
    .map((group) => ({
      ...group,
      services: group.services.sort(serviceSort),
    }))
    .sort((a, b) => a.host.localeCompare(b.host))
}

export function serviceNode(asset: ViewAsset): ServiceNode {
  const serviceItem = asset.items.find((item) => item.kind === assetItemKind.service)
  const paths = asset.items.filter((item) => item.kind === assetItemKind.path)
  const analysisItems = asset.items.filter(isAnalysisItem)
  const detailItems = asset.items.filter((item) => item.kind !== assetItemKind.path && !isAnalysisItem(item))
  const protocol = firstText(dataString(serviceItem, 'protocol'), dataString(serviceItem, 'service'))
  const service = firstText(dataString(serviceItem, 'service'), serviceItem?.title, protocol)
  const host = dataString(serviceItem, 'ip') || 'Scan'
  const port = dataString(serviceItem, 'port')
  const target = firstText(serviceItem?.target, asset.target)
  const title = serviceTitle(asset, serviceItem, service)
  const summary = serviceSummary(asset, serviceItem, title, service)
  const protocolKey = protocol.toLowerCase()
  const web = paths.length > 0 || dataBool(serviceItem, 'is_web') || protocolKey.startsWith('http')

  return {
    id: asset.id || `service:${asset.key || asset.target}`,
    asset,
    host,
    port,
    protocol,
    service,
    target,
    title,
    summary,
    sources: sourceValues(asset.items),
    states: stateValues(asset.items),
    statuses: statusCodeValues(paths),
    fingers: fingerprintValues(asset.items),
    paths,
    analysisItems,
    detailItems,
    pathCount: paths.length,
    web,
  }
}

export function itemFacts(item: AssetItem): ItemFacts {
  return {
    statuses: statusCodeValues([item]),
    states: stateValues([item]),
    fingers: fingerprintValues([item]),
    sources: sourceValues([item]),
  }
}

export function itemFactValues(item: AssetItem) {
  const facts = itemFacts(item)
  return [
    ...facts.statuses,
    ...facts.states,
    ...facts.fingers,
    ...facts.sources,
  ]
}

export function isAnalysisItem(item: AssetItem) {
  if (item.kind === assetItemKind.note || item.kind === assetItemKind.response) {
    return true
  }
  if (item.kind === assetItemKind.error) {
    return true
  }
  if (item.kind !== assetItemKind.loot) {
    return false
  }

  const backendKind = dataString(item, 'kind').toLowerCase()
  if (backendKind === 'fingerprint') {
    return false
  }

  return true
}

export function buildSitemapTree(items: AssetItem[]): SitemapNode[] {
  const root: SitemapNode = { id: 'root', name: 'root', path: '/', children: [], items: [] }
  for (const item of items) {
    insertPathItem(root, item)
  }
  return sortSitemapNodes(root.children)
}

export function defaultOpenSitemapNodes(nodes: SitemapNode[]) {
  const open = new Set<string>()
  visitSitemapFolders(nodes, (node, depth) => {
    if (depth < 1) {
      open.add(node.id)
    }
  })
  return open
}

export function collectSitemapFolderIDs(nodes: SitemapNode[]) {
  const ids: string[] = []
  visitSitemapFolders(nodes, (node) => ids.push(node.id))
  return ids
}

export function endpointFileName(item: AssetItem) {
  const path = dataString(item, 'path') || webPath(item.target)
  const pathname = path.split('?')[0] || '/'
  if (pathname === '/') {
    return '/'
  }

  const parts = pathname.split('/').filter(Boolean)
  const last = parts[parts.length - 1] || '/'
  return path.includes('?') ? `${last}?${path.split('?').slice(1).join('?')}` : last
}

export function pathSearch(item: AssetItem) {
  const path = dataString(item, 'path') || webPath(item.target)
  const idx = path.indexOf('?')
  return idx >= 0 ? path.slice(idx) : ''
}

export function pathIdentity(item: AssetItem) {
  return `${canonicalKey(dataString(item, 'url') || item.target || dataString(item, 'path'))}|host=${dataString(item, 'host_header')}`
}

export function itemTitle(item: AssetItem) {
  return firstText(item.summary, item.title)
}

export function sameTarget(left?: string, right?: string) {
  return canonicalKey(urlOrigin(left) || left) === canonicalKey(urlOrigin(right) || right)
}

export function statusCodeValues(items: AssetItem[]) {
  return uniqueStrings(items
    .filter((item) => item.kind === assetItemKind.path)
    .map((item) => firstText(item.status, dataString(item, 'status')))
    .filter((value) => value && isHTTPStatusCode(value)))
}

export function stateValues(items: AssetItem[]) {
  return uniqueStrings(items
    .filter((item) => item.kind !== assetItemKind.path)
    .map((item) => item.status)
    .filter((value) => value && !isHTTPStatusCode(value)))
}

export function fingerprintValues(items: AssetItem[]) {
  return uniqueStrings(items
    .filter((item) => item.kind === assetItemKind.fingerprint || item.kind === assetItemKind.path)
    .flatMap((item) => {
      if (item.kind === assetItemKind.fingerprint) {
        return [firstText(item.title, dataString(item, 'name'))]
      }
      return dataStringArray(item, 'fingers')
    })
    .filter(Boolean))
}

export function sourceValues(items: AssetItem[]) {
  return uniqueStrings(items
    .map((item) => firstText(item.source, dataString(item, 'source')))
    .filter(Boolean))
}

export function tagBadges(tags: string[] | undefined, excluded: Array<string | undefined>, tone?: BadgeTone): BadgeSpec[] {
  const hidden = new Set(excluded.map(labelKey).filter(Boolean))
  return dedupeBadges((tags || [])
    .filter((tag) => !hidden.has(labelKey(tag)))
    .map((tag) => ({ id: `tag:${tag}`, label: tag, tone })))
}

export function itemKindTone(kind: string): BadgeTone {
  if (kind === assetItemKind.loot) return 'red'
  if (kind === assetItemKind.note || kind === assetItemKind.response) return 'cyan'
  return 'muted'
}

export function itemStateTone(status?: string): BadgeTone {
  switch ((status || '').toLowerCase()) {
    case 'confirmed':
    case 'critical':
    case 'high':
    case 'loot':
    case 'error':
    case 'failed':
      return 'red'
    case 'medium':
    case 'inconclusive':
      return 'yellow'
    case 'low':
      return 'green'
    default:
      return 'muted'
  }
}

export function statusCodeTone(status?: string): BadgeTone {
  const code = Number(status)
  if (!Number.isFinite(code)) {
    return 'muted'
  }
  if (code >= 500) return 'red'
  if (code >= 400) return 'yellow'
  if (code >= 200 && code < 400) return 'green'
  return 'muted'
}

export function formatCount(count: number, singular: string) {
  return `${count} ${count === 1 ? singular : `${singular}s`}`
}

function serviceTitle(asset: ViewAsset, serviceItem: AssetItem | undefined, service: string) {
  const assetTitle = firstText(asset.title)
  if (assetTitle && assetTitle !== asset.target && labelKey(assetTitle) !== labelKey(service)) {
    return assetTitle
  }
  return firstText(dataString(serviceItem, 'banner'), serviceItem?.summary)
}

function serviceSummary(asset: ViewAsset, serviceItem: AssetItem | undefined, title: string, service: string) {
  const values = [
    dataString(serviceItem, 'banner'),
    serviceItem?.summary,
    asset.status,
  ]
  return firstText(...values.filter((value) => labelKey(value) !== labelKey(title) && labelKey(value) !== labelKey(service)))
}

function countLootItems(assets: ViewAsset[]) {
  return assets.reduce((sum, asset) => (
    sum + asset.items.filter((item) => (
      item.kind === assetItemKind.loot && dataString(item, 'kind').toLowerCase() !== 'fingerprint'
    )).length
  ), 0)
}

function countFingerprints(assets: ViewAsset[]) {
  return uniqueStrings(assets.flatMap((asset) => fingerprintValues(asset.items))).length
}

function serviceSort(a: ServiceNode, b: ServiceNode) {
  const ap = Number.parseInt(a.port, 10)
  const bp = Number.parseInt(b.port, 10)
  if (Number.isFinite(ap) && Number.isFinite(bp) && ap !== bp) {
    return ap - bp
  }
  return `${a.port}|${a.service}|${a.target}`.localeCompare(`${b.port}|${b.service}|${b.target}`)
}

function insertPathItem(root: SitemapNode, item: AssetItem) {
  const path = dataString(item, 'path') || webPath(item.target)
  const pathname = path.split('?')[0] || '/'
  if (pathname === '/') {
    getOrCreateSitemapChild(root, '/', '/').items.push(item)
    return
  }

  const parts = pathname.split('/').filter(Boolean)
  let current = root
  let currentPath = ''
  parts.forEach((part, index) => {
    currentPath += `/${part}`
    const node = getOrCreateSitemapChild(current, part, currentPath)
    if (index === parts.length - 1) {
      node.items.push(item)
      return
    }
    current = node
  })
}

function getOrCreateSitemapChild(parent: SitemapNode, name: string, path: string) {
  let child = parent.children.find((item) => item.path === path)
  if (!child) {
    child = { id: path, name, path, children: [], items: [] }
    parent.children.push(child)
  }
  return child
}

function sortSitemapNodes(nodes: SitemapNode[]): SitemapNode[] {
  return nodes
    .map((node) => ({ ...node, children: sortSitemapNodes(node.children) }))
    .sort((a, b) => {
      const aFolder = a.children.length > 0 ? 0 : 1
      const bFolder = b.children.length > 0 ? 0 : 1
      return `${aFolder}|${a.name}`.localeCompare(`${bFolder}|${b.name}`)
    })
}

function visitSitemapFolders(nodes: SitemapNode[], visit: (node: SitemapNode, depth: number) => void, depth = 0) {
  for (const node of nodes) {
    if (node.children.length === 0) {
      continue
    }
    visit(node, depth)
    visitSitemapFolders(node.children, visit, depth + 1)
  }
}

function dataString(item: AssetItem | undefined, key: string) {
  const value = item?.data?.[key]
  if (typeof value === 'string') return value
  if (typeof value === 'number' && value > 0) return String(value)
  return ''
}

function dataBool(item: AssetItem | undefined, key: string) {
  return item?.data?.[key] === true
}

function dataStringArray(item: AssetItem, key: string) {
  const value = item.data?.[key]
  if (Array.isArray(value)) {
    return value
      .map((entry) => {
        if (typeof entry === 'string') return entry
        if (typeof entry === 'number' && entry > 0) return String(entry)
        return ''
      })
      .filter(Boolean)
  }
  if (typeof value === 'string') {
    return splitList(value)
  }
  return []
}

function splitList(value: string) {
  return value
    .split(';')
    .flatMap((part) => part.split(','))
    .map((part) => part.trim())
    .filter(Boolean)
}

function isHTTPStatusCode(value?: string) {
  const code = Number(value)
  return Number.isInteger(code) && code >= 100 && code <= 599
}

function webPath(rawURL?: string) {
  const parsed = parseURL(rawURL)
  if (!parsed) {
    return rawURL || '/'
  }
  return `${parsed.pathname || '/'}${parsed.search || ''}`
}

function urlOrigin(rawURL?: string) {
  const parsed = parseURL(rawURL)
  return parsed ? parsed.origin.toLowerCase() : ''
}

function parseURL(value?: string) {
  if (!value) {
    return null
  }
  try {
    return new URL(value)
  } catch {
    return null
  }
}

function canonicalKey(value?: string) {
  return trimTrailingSlashes((value || '').trim()).toLowerCase()
}

function trimTrailingSlashes(value: string) {
  let end = value.length
  while (end > 1 && value[end - 1] === '/') {
    end -= 1
  }
  return value.slice(0, end)
}

function firstText(...values: Array<string | undefined>) {
  return values.find((value) => value && value.trim())?.trim() || ''
}

function uniqueStrings(values: Array<string | undefined>) {
  return Array.from(new Set(values.filter((value): value is string => !!value)))
}

function dedupeBadges(badges: BadgeSpec[]) {
  const seen = new Set<string>()
  const result: BadgeSpec[] = []
  for (const badge of badges) {
    const key = labelKey(badge.label)
    if (!key || seen.has(key)) {
      continue
    }
    seen.add(key)
    result.push(badge)
  }
  return result
}

function labelKey(value?: string) {
  return String(value || '').trim().toLowerCase()
}

// --- AI Findings helpers ---

export const PRIORITY_ORDER = ['critical', 'high', 'medium', 'low', 'info'] as const
export type FindingPriority = (typeof PRIORITY_ORDER)[number]

export type FindingItem = {
  id: string
  kind: 'vuln' | 'weakpass' | 'fingerprint' | 'note' | 'other'
  priority: FindingPriority
  title: string
  target: string
  description?: string
  source?: string
  status?: string
  tags: string[]
  detail?: string
  raw?: AssetItem | Loot
}

export type FindingsSummaryModel = {
  byPriority: Record<string, FindingItem[]>
  byStatus: Record<string, FindingItem[]>
  aiVerifiedCount: number
  totalFindings: number
  topFinding?: FindingItem
}

export function serviceAIStatus(service: ServiceNode): 'verified' | 'sniper' | 'deep' | null {
  if (service.analysisItems.some(i => i.source === 'verify' && i.status === 'confirmed')) return 'verified'
  if (service.analysisItems.some(i => i.source === 'sniper')) return 'sniper'
  if (service.analysisItems.some(i => i.source === 'deep')) return 'deep'
  return null
}

export function buildFindings(result: ScanResult): FindingItem[] {
  const findings: FindingItem[] = []
  const seen = new Set<string>()

  for (const loot of result.loots || []) {
    const id = `loot:${loot.target}:${loot.kind}:${loot.description || ''}`
    if (seen.has(id)) continue
    seen.add(id)
    findings.push({
      id,
      kind: normalizeLootKind(loot.kind),
      priority: normalizePriority(loot.priority),
      title: loot.description || loot.kind || 'Finding',
      target: loot.target,
      description: loot.description,
      tags: loot.tags || [],
      source: undefined,
      status: undefined,
      detail: lootDetail(loot),
      raw: loot,
    })
  }

  for (const asset of result.assets || []) {
    for (const item of asset.items || []) {
      if (!isAnalysisItem(item)) continue
      if (item.kind === 'error') continue
      const id = `item:${asset.target}:${item.source}:${item.kind}:${item.title || item.summary || ''}`
      if (seen.has(id)) continue
      seen.add(id)

      const priority = itemToPriority(item)
      findings.push({
        id,
        kind: normalizeItemKind(item.kind),
        priority,
        title: firstText(item.summary, item.title) || item.kind,
        target: item.target || asset.target,
        description: item.summary || item.title,
        source: item.source,
        status: item.status,
        tags: item.tags || [],
        detail: assetItemContent(item),
        raw: item,
      })
    }
  }

  findings.sort((a, b) => {
    const pa = PRIORITY_ORDER.indexOf(a.priority)
    const pb = PRIORITY_ORDER.indexOf(b.priority)
    if (pa !== pb) return pa - pb
    const av = a.source === 'verify' && a.status === 'confirmed' ? 0 : 1
    const bv = b.source === 'verify' && b.status === 'confirmed' ? 0 : 1
    return av - bv
  })

  return findings
}

export function buildFindingsSummary(result: ScanResult): FindingsSummaryModel | null {
  const findings = buildFindings(result)
  if (findings.length === 0) return null

  const byPriority: Record<string, FindingItem[]> = {}
  const byStatus: Record<string, FindingItem[]> = {}

  for (const f of findings) {
    ;(byPriority[f.priority] ||= []).push(f)
    if (f.source) {
      const key = f.status || 'unknown'
      ;(byStatus[key] ||= []).push(f)
    }
  }

  const aiVerifiedCount = findings.filter(
    f => f.source === 'verify' && f.status === 'confirmed',
  ).length

  return {
    byPriority,
    byStatus,
    aiVerifiedCount,
    totalFindings: findings.length,
    topFinding: findings[0],
  }
}

function normalizeLootKind(kind?: string): FindingItem['kind'] {
  switch (kind?.toLowerCase()) {
    case 'vuln': return 'vuln'
    case 'weakpass': return 'weakpass'
    case 'fingerprint': return 'fingerprint'
    default: return 'other'
  }
}

function normalizeItemKind(kind?: string): FindingItem['kind'] {
  switch (kind?.toLowerCase()) {
    case 'loot': return 'vuln'
    case 'note': return 'note'
    default: return 'other'
  }
}

function normalizePriority(priority?: string): FindingPriority {
  const p = (priority || '').toLowerCase()
  if (PRIORITY_ORDER.includes(p as FindingPriority)) return p as FindingPriority
  return 'info'
}

function itemToPriority(item: AssetItem): FindingPriority {
  const p = (item.status || '').toLowerCase()
  if (p === 'confirmed' || p === 'critical') return 'critical'
  if (p === 'high') return 'high'
  if (p === 'medium' || p === 'inconclusive') return 'medium'
  if (p === 'low') return 'low'
  return 'info'
}

export function assetItemContent(item: AssetItem): string {
  return firstText(
    item.detail,
    dataText(item.data?.content),
    dataText(item.data?.detail),
    dataText(item.data?.markdown),
    dataText(item.data?.narrative),
    dataText(item.data?.evidence),
    dataText(item.data?.response),
    dataText(item.data?.output),
  )
}

function dataText(value: unknown) {
  return typeof value === 'string' ? value : ''
}

function lootDetail(loot: Loot): string | undefined {
  if (!loot.data) return undefined
  const text = loot.data['detail'] || loot.data['evidence'] || loot.data['markdown'] || loot.data['narrative']
  return typeof text === 'string' ? text : undefined
}
