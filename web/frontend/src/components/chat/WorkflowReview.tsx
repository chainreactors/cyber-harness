import { useState } from 'react'
import { useTranslation } from 'react-i18next'
import { workflowNodeSummary, workflowNodeUsage, type WorkflowNode } from '../../lib/workflow-view'
import { formatTokenCount, mergeTokenUsage } from '../../../cyber-ui/packages/viewer/src/lib/token-usage'

export function WorkflowReview({ nodes, onSelect }: { nodes: WorkflowNode[]; onSelect: (id: string) => void }) {
  const { t } = useTranslation('jev')
  const [order, setOrder] = useState<'time' | 'tokens'>('time')
  const [open, setOpen] = useState(false)
  const entries = nodes.map((node, index) => ({ node, index, usage: workflowNodeUsage(node, nodes) }))
  const total = mergeTokenUsage(...entries.map(entry => entry.usage))
  const tools = nodes.filter(node => node.kind === 'tool')
  const counts = (['completed', 'failed', 'pending', 'interrupted'] as const)
    .map(state => ({ state, count: tools.filter(node => node.state === state).length })).filter(value => value.count)
  const sorted = order === 'tokens' ? [...entries].sort((a, b) => (b.usage?.totalTokens || 0) - (a.usage?.totalTokens || 0) || a.index - b.index) : entries
  return <details className="workflow-review" data-testid="workflow-review" onToggle={event => setOpen(event.currentTarget.open)}>
    <summary><strong>{t('workflow.review')}</strong><span>{counts.map(({ state, count }) => t(`workflow.reviewStates.${state}`, { count })).join(' · ')}</span>
      {total && <span className="workflow-review-total">{formatTokenCount(total.totalTokens)} tok</span>}</summary>
    {open && <><div className="workflow-review-order" role="group" aria-label={t('workflow.reviewOrder')}>
      {(['time', 'tokens'] as const).map(value => <button key={value} type="button" aria-pressed={order === value} onClick={() => setOrder(value)}>{t(`workflow.order.${value}`)}</button>)}
    </div>
    <ol className="workflow-review-list">
      {sorted.map(({ node, index, usage }) => {
        const payload = node.related?.find(record => record.value.payload.case === 'result')?.value.payload
        const native = node.item?.kind === 'tool_call' ? node.item.toolCall : undefined
        const result = native?.toolResult || (payload?.case === 'result' ? payload.value.result : undefined)
        const duration = Number(result?.durationMs || (payload?.case === 'result' ? payload.value.elapsedMs : 0))
        const evidence = result?.output.flatMap(content => content.value.case === 'text' ? [content.value.value.text] : []).join(' ') || native?.result
        const reported = node.item?.kind === 'assistant_response' ? node.item.response?.metadata?.usage as Record<string, unknown> | undefined : undefined
        const requests = Number(reported?.requests || 0)
        const read = usage?.cacheReadInputTokens ?? usage?.cachedInputTokens
        const hit = read !== undefined && usage && usage.inputTokens > 0 ? Math.min(100, read / usage.inputTokens * 100) : undefined
        return <li key={node.id} data-review-node={node.id} data-state={node.state}>
          <button type="button" className="workflow-review-entry" onClick={() => onSelect(node.id)}>
            <span className="workflow-review-index">{index + 1}</span>
            <span className="workflow-review-description"><strong>{node.literal ? node.label : t(node.label)} <small>{node.actor}{node.background ? ` · ${t('workflow.background')}` : ''}</small></strong>
              <span>{workflowNodeSummary(node)}</span>{evidence && <span className="workflow-review-evidence">{evidence.replace(/\s+/g, ' ').slice(0, 180)}</span>}
            </span>
            <span className="workflow-review-metrics"><span>{t(`workflow.states.${node.state}`)}</span>
              {duration > 0 && <span>{duration < 1000 ? `${duration}ms` : `${(duration / 1000).toFixed(1)}s`}</span>}
              {usage && <span data-testid="workflow-review-usage">↓{formatTokenCount(usage.inputTokens)} ↑{formatTokenCount(usage.outputTokens)}
                {hit !== undefined && ` · ${t('workflow.cache', { percent: hit.toFixed(0) })}`}</span>}
              {requests > 1 && <span>{t('workflow.requests', { count: requests })}</span>}
            </span>
          </button>
        </li>
      })}
    </ol>
    <p className="workflow-review-note">{t('workflow.reviewNote')}</p></>}
  </details>
}
