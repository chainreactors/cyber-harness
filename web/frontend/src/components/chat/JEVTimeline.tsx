import type { ReactNode } from 'react'
import { CircuitBoard, Repeat2, Layers } from 'lucide-react'
import { useTranslation } from 'react-i18next'
import { create } from '@bufbuild/protobuf'
import type { TimelineRendererConfig } from '@/viewer'
import { CodeBlock } from '@/markdown'
import type { ExtensionTimelineItem } from '@/viewer'
import { DecisionRequestSchema } from '../../gen/types/jev_pb'
import { ClaimSchema } from '../../gen/decision/claim_pb'
import { claimDefinitions, parseJEVJSON, evaluationType } from '../../lib/jev-decisions'
import type { JEVSegment, JEVCompilation, JEVCheck } from '../../lib/jev-view'
import { DecisionBatch } from './JEVDecision'
import { TokenUsageBadge } from './TokenUsageBadge'
import { JEVDefinition } from './JEVDefinition'
import { JEVReference } from './JEVReference'
import { recordWorkflows, workflowNodeUsage, type WorkflowNode } from '../../lib/workflow-view'
import { Workflow } from './Workflow'
import type { ControlStage } from '../../lib/jev-control-flow'
import { WorkflowToolContent } from './WorkflowToolContent'
import './JEVTimeline.css'

function Detail({ title, children }: { title: string; children: ReactNode }) {
  return <section className="jev-detail"><p className="text-[11px] text-muted-foreground">{title}</p><div className="mt-2 min-w-0">{children}</div></section>
}

function CompilerDiagnosticView({ output }: { output: string }) {
  const { t } = useTranslation('jev')
  const parsed = parseJEVJSON(output)
  if (!parsed || typeof parsed !== 'object' || !('diagnostic' in parsed)) return null
  const diagnostic = parsed.diagnostic
  if (!diagnostic || typeof diagnostic !== 'object') return null
  const value = diagnostic as Record<string, unknown>
  const code = String(value.code || '')
  return <div data-testid="jev-compiler-diagnostic" className="mt-2 space-y-2 text-[11px]">
    <p className="font-medium">{t('compilerDiagnostic')} · {t(`diagnosticCodes.${code}`, { defaultValue: code })}</p>
    {typeof value.message === 'string' && <p className="whitespace-pre-wrap break-words text-muted-foreground">{value.message}</p>}
    {typeof value.recorded === 'number' && <p>{t('replayProgress', { replayed: Number(value.replayed || 0), recorded: value.recorded })}</p>}
    {typeof value.action === 'string' && <p className="whitespace-pre-wrap break-words"><span className="font-medium">{t('diagnosticAction')}：</span>{value.action}</p>}
    {value.expected !== undefined && <Detail title={t('expectedBinding')}><CodeBlock code={JSON.stringify(value.expected, null, 2)} language="json" maxHeight={200} /></Detail>}
    {value.actual !== undefined && <Detail title={t('actualBinding')}><CodeBlock code={JSON.stringify(value.actual, null, 2)} language="json" maxHeight={200} /></Detail>}
  </div>
}

function Facts({ json }: { json: string }) {
  const value = parseJEVJSON(json)
  function field(item: unknown, depth = 0): ReactNode {
    if (Array.isArray(item)) return <ol className="space-y-1">{item.map((value, i) => <li key={i}>{field(value, depth + 1)}</li>)}</ol>
    if (item && typeof item === 'object' && depth < 4) return <dl className="jev-facts">{Object.entries(item).map(([key, value]) => <div key={key}>
      <dt>{key}</dt><dd>{field(value, depth + 1)}</dd></div>)}</dl>
    return <span className="whitespace-pre-wrap break-words">{item && typeof item === 'object' ? JSON.stringify(item) : String(item ?? '—')}</span>
  }
  return <div className="jev-facts-scroll">{field(value)}</div>
}

function CompilationFeedback({ reason }: { reason: string }) {
  const { t } = useTranslation('jev')
  const marker = 'Actual evaluated native bindings: '
  const [message, detail] = reason.split(marker)
  const end = detail?.lastIndexOf('}. Check every call')
  const bindings = end === undefined || end < 0 ? undefined : detail.slice(0, end + 1)
  return <div data-testid="jev-compilation-error" className="max-h-72 overflow-y-auto space-y-3">
    <p className="whitespace-pre-wrap break-words text-xs text-destructive">{message}</p>
    {bindings && <div><p className="mb-2 text-[11px] text-muted-foreground">{t('nativeBinding')}</p><Facts json={bindings} /></div>}
    {detail && <p className="whitespace-pre-wrap break-words text-[11px] text-muted-foreground">{end === undefined || end < 0 ? detail : detail.slice(end + 2)}</p>}
  </div>
}

function GeneratedClaims({ output }: { output: string }) {
  return <>{claimDefinitions(parseJEVJSON(output)).map((value, index) => <JEVDefinition key={index} compact value={value} />)}</>
}

// The graph renders each recorded payload exactly once, inside its own node.
export function WorkflowNodeContent({ node, nodes, stage }: { node: WorkflowNode; nodes: WorkflowNode[]; stage: ControlStage }) {
  const { t } = useTranslation('jev')
  const records = node.related || [], payload = node.record?.value.payload
  if (!payload) return null
  switch (payload.case) {
    case 'decisionRequest': {
      const result = records.find(record => record.value.payload.case === 'decisionResult')?.value.payload
      return <DecisionBatch bodyOnly request={payload.value} result={result?.case === 'decisionResult' ? result.value : undefined} />
    }
    case 'decisionResult': return <DecisionBatch bodyOnly request={create(DecisionRequestSchema, { requestId: payload.value.requestId, purpose: payload.value.purpose,
      claims: Object.fromEntries(Object.entries(payload.value.evaluations).map(([id, answer]) => [id, create(ClaimSchema, { type: evaluationType(answer), options: Object.keys(answer.probabilities) })])) })} result={payload.value} />
    case 'dispatch': case 'result': return <WorkflowToolContent node={node} stage={stage} />
    case 'observation': return <div className="space-y-3"><Facts json={payload.value.stateJson} />{payload.value.candidatesJson && <Facts json={payload.value.candidatesJson} />}</div>
    case 'takeover': return payload.value.definition && <div className="workflow-reflex-context"><code>{payload.value.definition.id}</code><p>{payload.value.definition.when}</p>{payload.value.definition.decide && <p>{payload.value.definition.decide}</p>}</div>
    case 'handoff': return <div className="space-y-2 text-xs"><p>{payload.value.detail || payload.value.reason}</p>{payload.value.code && <code>{payload.value.code}</code>}{payload.value.effectsJson && <Facts json={payload.value.effectsJson} />}{payload.value.resultJson && <Facts json={payload.value.resultJson} />}</div>
    case 'boundary': return <p className="text-xs leading-relaxed text-muted-foreground">{t(`reasons.${payload.value.reason}`, { defaultValue: payload.value.reason })}</p>
    case 'generation': {
      const last = records[records.length - 1]?.value.payload
      const generation = last?.case === 'generation' ? last.value : payload.value
      const output = parseJEVJSON(generation.output)
      const validation = generation.kind === 'reflex_validation' && output && typeof output === 'object' && 'diagnostic' in output ? output : undefined
      const remainingOutput = validation ? JSON.stringify(Object.fromEntries(Object.entries(validation).filter(([key]) => key !== 'diagnostic')), null, 2) : generation.output
      const diagnosticMessage = validation?.diagnostic && typeof validation.diagnostic === 'object' && 'message' in validation.diagnostic ? validation.diagnostic.message : undefined
      const drafts = generation.kind === 'claim_llm' ? claimDefinitions(output)
        : generation.kind === 'reflex_llm' && output && typeof output === 'object' && 'observe' in output && typeof output.observe === 'string' ? [output] : []
      const targets = drafts.map(draft => nodes.find(other => {
        const change = other.record?.value.payload
        if (other.sessionId !== node.sessionId || other.turnId !== node.turnId || other.timestamp < node.timestamp || change?.case !== 'libraryChange') return false
        return 'context' in draft ? change.value.state === 'claim_published' && !!change.value.claim
          && draft.type === change.value.claim.type && draft.context === change.value.claim.context && JSON.stringify(draft.options) === JSON.stringify(change.value.claim.options)
          : change.value.state === 'reflex_published' && change.value.reflex?.observe === draft.observe
      }))
      const published = generation.state === 'finished' && !generation.error && drafts.length > 0 && targets.every(Boolean)
      const publications = [...new Map(targets.filter((target): target is WorkflowNode => !!target).map(target => [target.id, target])).values()]
      return <div className="space-y-3">
        <div className="flex flex-wrap items-center justify-between gap-x-3 gap-y-1 text-xs text-muted-foreground"><span>{t(generation.state === 'started' ? 'generationStarted' : generation.error ? 'generationFailed' : 'generationFinished')}</span>
          {generation.state === 'finished' && <TokenUsageBadge usage={workflowNodeUsage(node, nodes)} />}</div>
        {(generation.attempt > 0 || generation.errorStage) && <p className="text-[11px] text-muted-foreground" data-generation-request-id={generation.requestId}>{t('generationAttempt', { count: generation.attempt })}{generation.errorStage && ` · ${t('errorStage', { stage: generation.errorStage })}`}</p>}
        {generation.error && generation.error !== diagnosticMessage && <CompilationFeedback reason={generation.error} />}
        {generation.kind === 'reflex_validation' && generation.output && <CompilerDiagnosticView output={generation.output} />}
        {published ? <div className="space-y-2"><p className="text-xs text-muted-foreground">{t('workflow.artifactAtPublication')}</p>
          {publications.map(publication => <JEVReference key={publication.id} label={t('workflow.openPublication')} onClick={() => window.dispatchEvent(new CustomEvent('cyber-workflow-select', { detail: publication.id }))} />)}</div> : remainingOutput && remainingOutput !== '{}' && (generation.kind === 'claim_llm' && !generation.error
          ? <GeneratedClaims output={remainingOutput} /> : <CodeBlock code={remainingOutput} language={generation.kind === 'reflex_llm' ? 'javascript' : 'text'} maxHeight={300} />)}
      </div>
    }
    case 'libraryChange': return <div className="space-y-3">
      {payload.value.claim && <JEVDefinition value={payload.value.claim} inline />}{payload.value.reflex && <JEVDefinition value={payload.value.reflex} inline />}
      {payload.value.errorStage && <p className="text-[11px] text-muted-foreground">{t('errorStage', { stage: payload.value.errorStage })}</p>}
      {payload.value.reason && <CompilationFeedback reason={payload.value.reason} />}
    </div>
    default: return null
  }
}

// Requests and their answers share one node. Independent heads branch in
// parallel; execution, feedback and compilation retain recorded event order.
// Compatibility timeline entries use the same graph and payload components.
function RecordedWorkflow({ owner }: { owner: JEVSegment | JEVCompilation | JEVCheck }) {
  return <>{recordWorkflows(owner).map(workflow => <Workflow key={workflow.id} workflow={workflow} renderItem={() => null} />)}</>
}

export function JEVSegmentView({ segment }: { segment: JEVSegment }) {
  return <div data-testid="jev-segment" id={`jev-${segment.id}`}><RecordedWorkflow owner={segment} /></div>
}

export function JEVCompilationView({ compilation }: { compilation: JEVCompilation }) {
  return <div data-testid="jev-compilation"><RecordedWorkflow owner={compilation} /></div>
}

function JEVCheckView({ check }: { check: JEVCheck }) {
  return <div data-testid="jev-check" id={`jev-${check.id}`}><RecordedWorkflow owner={check} /></div>
}

export const jevSegmentRenderer: TimelineRendererConfig = {
  renderer: ({ item }: { item: ExtensionTimelineItem }) => <JEVSegmentView segment={item.data.segment as JEVSegment} />,
  mark: { label: 'JEV', icon: Repeat2, dotClass: 'border-emerald-500 bg-emerald-500' },
}
export const jevCompilationRenderer: TimelineRendererConfig = {
  renderer: ({ item }: { item: ExtensionTimelineItem }) => <JEVCompilationView compilation={item.data.compilation as JEVCompilation} />,
  mark: { label: 'Reflex', icon: Layers, dotClass: 'border-border bg-muted-foreground/60' },
}
export const jevCheckRenderer: TimelineRendererConfig = {
  renderer: ({ item }: { item: ExtensionTimelineItem }) => <JEVCheckView check={item.data.check as JEVCheck} />,
  mark: { label: 'JEV', icon: CircuitBoard, dotClass: 'border-border bg-muted-foreground/60' },
}
