import { useEffect, useMemo, useState } from 'react'
import { Monitor, Search, Terminal, Wrench } from 'lucide-react'
import { useTranslation } from 'react-i18next'
import { Badge, DisclosureCard, EmptyState, Input, Tabs, TabsContent, TabsList, TabsTrigger } from '@cyber/ui'
import type { AgentView, CommandSpec } from '../api'
import { ToolDrawer } from './layout/ToolDrawer'

interface ToolRegistryPanelProps {
  open: boolean
  agents: AgentView[]
  onClose: () => void
}

interface AgentRegistry {
  nodeID: string
  name: string
  busy: boolean
  tools: CommandSpec[]
}

export default function ToolRegistryPanel({ open, agents, onClose }: ToolRegistryPanelProps) {
  const { t } = useTranslation('tools')
  const [queries, setQueries] = useState<Record<string, string>>({})
  const [selectedAgentID, setSelectedAgentID] = useState('')
  const registries = useMemo(() => buildRegistries(agents), [agents])

  useEffect(() => {
    if (!registries.some((registry) => registry.nodeID === selectedAgentID)) {
      setSelectedAgentID(registries[0]?.nodeID || '')
    }
  }, [registries, selectedAgentID])

  const selectedAgent = registries.find((registry) => registry.nodeID === selectedAgentID) ?? null
  const query = selectedAgent ? queries[selectedAgent.nodeID] || '' : ''
  const visibleTools = useMemo(() => {
    if (!selectedAgent) return []
    const needle = query.trim().toLocaleLowerCase()
    if (!needle) return selectedAgent.tools
    return selectedAgent.tools.filter((tool) => [tool.name, tool.description, tool.usage, ...tool.aliases]
      .some((value) => value.toLocaleLowerCase().includes(needle)))
  }, [query, selectedAgent])

  const updateQuery = (value: string) => {
    if (!selectedAgent) return
    setQueries((current) => ({ ...current, [selectedAgent.nodeID]: value }))
  }

  return (
    <ToolDrawer
      open={open}
      onClose={onClose}
      icon={Wrench}
      title={t('title')}
      description={selectedAgent ? t('agentDescription', { agent: selectedAgent.name }) : t('description')}
      titleMeta={(
        <Badge variant="secondary" size="sm" className="py-0 font-mono font-normal">
          {selectedAgent?.tools.length ?? 0}
        </Badge>
      )}
    >
      {registries.length === 0 ? (
        <div className="flex h-full items-center justify-center">
          <EmptyState icon={Wrench} title={t('noAgents')} description={t('noAgentsDescription')} />
        </div>
      ) : (
        <Tabs value={selectedAgentID} onValueChange={setSelectedAgentID} className="flex h-full min-h-0 flex-col">
          <div className="shrink-0 border-b border-border/70 bg-muted/20 px-4 pt-3">
            <div className="overflow-x-auto">
              <TabsList className="h-auto min-w-max justify-start rounded-none bg-transparent p-0">
                {registries.map((registry) => (
                  <TabsTrigger
                    key={registry.nodeID}
                    value={registry.nodeID}
                    className="gap-2 rounded-b-none border-b-2 border-transparent px-3 py-2 text-xs data-[state=active]:border-primary data-[state=active]:bg-background data-[state=active]:shadow-none"
                  >
                    <Monitor className="h-3.5 w-3.5" aria-hidden="true" />
                    <span className="max-w-40 truncate">{registry.name}</span>
                    <span className="font-mono text-[10px] text-muted-foreground">{registry.tools.length}</span>
                  </TabsTrigger>
                ))}
              </TabsList>
            </div>
          </div>

          <div className="shrink-0 space-y-3 border-b border-border/70 bg-background px-4 py-3">
            <div className="relative">
              <Search className="pointer-events-none absolute left-3 top-1/2 h-3.5 w-3.5 -translate-y-1/2 text-muted-foreground" aria-hidden="true" />
              <Input
                value={query}
                onChange={(event) => updateQuery(event.target.value)}
                placeholder={t('searchPlaceholder')}
                aria-label={t('searchPlaceholder')}
                className="h-8 pl-9 text-xs"
              />
            </div>
            <div className="flex items-center justify-between gap-3 text-[11px] text-muted-foreground">
              <span>{t('resultCount', { count: visibleTools.length })}</span>
              <span className="flex items-center gap-1.5">
                <Terminal className="h-3 w-3" aria-hidden="true" />
                {t('transportHint')}
              </span>
            </div>
          </div>

          {selectedAgent && (
            <TabsContent value={selectedAgent.nodeID} className="mt-0 min-h-0 flex-1 overflow-y-auto p-4">
              <div className="mx-auto max-w-5xl">
                <AgentToolSection
                  registry={{ ...selectedAgent, tools: visibleTools }}
                  emptyMessage={query.trim() ? t('noMatchesDescription') : t('agentEmpty')}
                />
              </div>
            </TabsContent>
          )}
        </Tabs>
      )}
    </ToolDrawer>
  )
}

function AgentToolSection({ registry, emptyMessage }: { registry: AgentRegistry; emptyMessage: string }) {
  const { t } = useTranslation('tools')
  return (
    <section aria-labelledby={`tool-agent-${registry.nodeID}`}>
      <header className="mb-2 flex min-w-0 items-center gap-2 border-b border-border/60 pb-2">
        <span className="flex h-7 w-7 shrink-0 items-center justify-center rounded-md bg-primary/10 text-primary">
          <Monitor className="h-3.5 w-3.5" aria-hidden="true" />
        </span>
        <span className="min-w-0 flex-1">
          <span id={`tool-agent-${registry.nodeID}`} className="block truncate text-xs font-semibold text-foreground">
            {registry.name}
          </span>
          <span className="block truncate font-mono text-[10px] text-muted-foreground">{registry.nodeID}</span>
        </span>
        <Badge variant={registry.busy ? 'warning' : 'success'} size="sm" className="shrink-0 py-0 font-normal">
          {registry.busy ? t('busy') : t('idle')}
        </Badge>
        <Badge variant="secondary" size="sm" className="shrink-0 py-0 font-mono font-normal">
          {t('agentTools', { count: registry.tools.length })}
        </Badge>
      </header>

      {registry.tools.length === 0 ? (
        <div className="rounded-lg border border-dashed border-border/70 px-4 py-5 text-center text-xs text-muted-foreground">
          {emptyMessage}
        </div>
      ) : (
        <div className="space-y-2">
          {registry.tools.map((tool, index) => (
            <ToolCard key={`${tool.name}:${tool.usage}:${index}`} tool={tool} />
          ))}
        </div>
      )}
    </section>
  )
}

function ToolCard({ tool }: { tool: CommandSpec }) {
  const { t } = useTranslation('tools')
  const name = tool.name.replace(/^!/, '')
  const description = tool.description || t('fallbackDescription')

  return (
    <DisclosureCard
      className="border-border/70 bg-card shadow-soft"
      headerClassName="min-h-12"
      header={(
        <>
          <span className="flex h-7 w-7 shrink-0 items-center justify-center rounded-md bg-primary/10 text-primary">
            <Terminal className="h-3.5 w-3.5" aria-hidden="true" />
          </span>
          <span className="min-w-0 flex-1">
            <span className="flex min-w-0 items-center gap-2">
              <code className="truncate font-mono text-xs font-semibold text-foreground">{name}</code>
              <Badge variant="muted" size="sm" className="shrink-0 py-0 font-mono font-normal">bash</Badge>
            </span>
            <span className="mt-0.5 block truncate text-[11px] text-muted-foreground">{description}</span>
          </span>
        </>
      )}
      bodyClassName="border-t border-border/60 bg-muted/10"
    >
      <div className="space-y-4 p-4">
        <div>
          <p className="mb-1 text-[10px] font-semibold uppercase tracking-wider text-muted-foreground">{t('usage')}</p>
          <pre className="overflow-x-auto rounded-md border border-border/70 bg-background px-3 py-2 font-mono text-xs text-foreground">
            {tool.usage || tool.name}
          </pre>
        </div>
        <div>
          <p className="mb-1 text-[10px] font-semibold uppercase tracking-wider text-muted-foreground">{t('descriptionLabel')}</p>
          <p className="text-xs leading-relaxed text-foreground/85">{description}</p>
        </div>
        {tool.aliases.length > 0 && (
          <div>
            <p className="mb-1.5 text-[10px] font-semibold uppercase tracking-wider text-muted-foreground">{t('aliases')}</p>
            <div className="flex flex-wrap gap-1.5">
              {tool.aliases.map((alias) => (
                <Badge key={alias} variant="outline" size="sm" className="font-mono font-normal">{alias}</Badge>
              ))}
            </div>
          </div>
        )}
      </div>
    </DisclosureCard>
  )
}

function buildRegistries(agents: AgentView[]): AgentRegistry[] {
  return agents.map((agent) => ({
    nodeID: agent.hello?.nodeId || '',
    name: agent.hello?.name || shortNodeID(agent.hello?.nodeId || ''),
    busy: agent.busy,
    tools: (agent.commands ?? []).filter(isBashTool),
  })).filter((registry) => registry.nodeID)
}

function isBashTool(spec: CommandSpec): boolean {
  return spec.name.startsWith('!') && spec.name.length > 1
}

function shortNodeID(nodeID: string): string {
  return nodeID.length > 12 ? `${nodeID.slice(0, 12)}…` : nodeID
}
