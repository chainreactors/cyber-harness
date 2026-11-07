import { useEffect, useLayoutEffect, useMemo, useRef, useState, type ReactNode } from 'react'
import { ArrowDownToLine, ChevronDown, GitBranch, Loader2 } from 'lucide-react'
import { useTranslation } from 'react-i18next'
import type { ViewerTimelineItem } from '@/viewer'
import type { WorkflowNode, WorkflowTurn } from '../../lib/workflow-view'
import { controlFrames, controlSnapshot } from '../../lib/jev-control-flow'
import { isWorkflowAutomaticScroll, scrollWorkflowKey } from '../../lib/workflow-scroll'
import { ControlPlayback, JEVControlFlow } from './JEVControlFlow'
import { WorkflowReview } from './WorkflowReview'
import './Workflow.css'

const signature = (node: WorkflowNode) => `${node.state}:${node.related?.[node.related.length - 1]?.event.id || ''}`

export function Workflow({ workflow, renderItem }: { workflow: WorkflowTurn; renderItem: (item: ViewerTimelineItem) => ReactNode }) {
  const { t } = useTranslation('jev')
  const disclosure = useRef<HTMLDetailsElement>(null)
  const [selected, setSelected] = useState<string>()
  const [scope, setScope] = useState<'all' | 'foreground' | 'background'>('all'), [expanded, setExpanded] = useState(true)
  const [frameId, setFrameId] = useState<string>(), [animationId, setAnimationId] = useState<string>()
  const [following, setFollowing] = useState(true), [revealVersion, setRevealVersion] = useState(0)
  const focusedBreakpoint = useRef(false)
  const pendingFocus = useRef<string>()
  const [onScreen, setOnScreen] = useState(true)
  const [reducedMotion, setReducedMotion] = useState(() => matchMedia('(prefers-reduced-motion: reduce)').matches)
  const [packets, setPackets] = useState<{ id: string; target: string }[]>([])
  const previous = useRef<Map<string, string>>()
  const visible = useMemo(() => workflow.nodes.filter(node => scope === 'all' || !!node.background === (scope === 'background')), [workflow.nodes, scope])
  const liveNodes = workflow.nodes.filter(node => node.state === 'pending')
  const foreground = visible.filter(node => !node.background), pending = visible.filter(node => node.state === 'pending')
  const recordedCurrent = visible.find(node => node.id === selected) || pending.find(node => node.kind === 'guardrail') || pending.find(node => !node.background) || pending[0]
    || foreground[foreground.length - 1] || visible[visible.length - 1]
  const frames = useMemo(() => controlFrames(visible), [visible])
  const replaying = frameId !== undefined
  const playing = !workflow.live && !liveNodes.length && !replaying && selected === undefined && !reducedMotion && frames.length > 1
  const currentFrame = [...frames].reverse().find(frame => frame.nodeId === recordedCurrent?.id)
  const cursor = replaying || playing ? Math.max(0, frames.findIndex(frame => frame.id === (replaying ? frameId : animationId))) : frames.findIndex(frame => frame.id === currentFrame?.id)
  const frame = frames[cursor]
  const evidence = useMemo(() => replaying ? controlSnapshot(visible, frames, cursor) : visible, [visible, frames, cursor, replaying])
  const snapshot = replaying && selected === undefined ? evidence : visible
  // Automatic loops highlight the complete record. Only an explicit seek filters evidence.
  const current = replaying ? evidence.find(node => node.id === frame?.nodeId) : playing ? visible.find(node => node.id === frame?.nodeId) : recordedCurrent
  const followCurrent = following && !playing && !replaying && (workflow.live || liveNodes.length > 0)
  const failed = workflow.nodes.filter(node => node.state === 'failed').length
  const backgroundCount = workflow.nodes.filter(node => node.background).length
  const selectNode = (id: string, breakpoint?: string) => {
    focusedBreakpoint.current = true
    setFollowing(false)
    setFrameId(breakpoint || [...frames].reverse().find(frame => frame.nodeId === id)?.id)
    setSelected(id)
    setRevealVersion(value => value + 1)
  }
  const follow = () => {
    focusedBreakpoint.current = false
    setFollowing(true)
    setFrameId(undefined); setSelected(undefined)
    setAnimationId(frames[frames.length - 1]?.id)
    setRevealVersion(value => value + 1)
  }
  const resume = () => {
    if (!focusedBreakpoint.current) return
    focusedBreakpoint.current = false
    setAnimationId(frameId); setFrameId(undefined); setSelected(undefined)
  }
  const detach = () => setFollowing(false)
  useLayoutEffect(() => {
    if (!pendingFocus.current) return
    const card = [...(disclosure.current?.querySelectorAll<HTMLElement>('[data-control-node]') || [])].reverse()
      .find(element => element.dataset.controlNode === pendingFocus.current)
    if (card) { pendingFocus.current = undefined; card.focus({ preventScroll: true }) }
  }, [snapshot, selected, revealVersion])
  useEffect(() => {
    const media = matchMedia('(prefers-reduced-motion: reduce)')
    const change = () => setReducedMotion(media.matches)
    media.addEventListener('change', change)
    const observer = new IntersectionObserver(([entry]) => setOnScreen(entry.isIntersecting))
    if (disclosure.current) observer.observe(disclosure.current)
    return () => { media.removeEventListener('change', change); observer.disconnect() }
  }, [])
  useEffect(() => {
    if (!playing || !expanded || !onScreen || !frames.length) return
    const ended = cursor >= frames.length - 1
    const timer = setTimeout(() => setAnimationId(frames[ended ? 0 : cursor + 1].id), ended ? 1600 : 800)
    return () => clearTimeout(timer)
  }, [playing, frames, cursor, expanded, onScreen])
  useEffect(() => { if (workflow.live && disclosure.current) disclosure.current.open = true }, [workflow.live])
  useEffect(() => {
    const select = (event: Event) => {
      const id = (event as CustomEvent<string>).detail
      const node = workflow.nodes.find(node => node.id === id || node.related?.some(record => record.event.id === id))
      if (!node) return
      pendingFocus.current = node.id
      focusedBreakpoint.current = true
      setFollowing(false)
      setScope('all')
      setFrameId([...controlFrames(workflow.nodes)].reverse().find(frame => frame.nodeId === node.id)?.id)
      setSelected(node.id)
      setRevealVersion(value => value + 1)
      if (disclosure.current) { disclosure.current.open = true; disclosure.current.scrollIntoView({ block: 'center', behavior: 'instant' }) }
    }
    window.addEventListener('cyber-workflow-select', select)
    return () => window.removeEventListener('cyber-workflow-select', select)
  }, [workflow.nodes])
  useEffect(() => {
    const updates = workflow.nodes.filter(node => previous.current && previous.current.get(node.id) !== signature(node))
    previous.current = new Map(workflow.nodes.map(node => [node.id, signature(node)]))
    if (!updates.length) return
    setPackets(updates.map(node => ({ id: `${node.id}:${signature(node)}`, target: node.id })))
  }, [workflow.nodes])
  useEffect(() => {
    if (!packets.length) return
    const timer = setTimeout(() => setPackets([]), 1700)
    return () => clearTimeout(timer)
  }, [packets])
  return <details ref={disclosure} open onToggle={event => setExpanded(event.currentTarget.open)} className="agent-workflow" data-testid="agent-workflow" data-workflow-id={workflow.id}
    data-animating={playing} data-following={following}
    onWheelCapture={event => { if (event.deltaX || event.deltaY) detach() }} onTouchMoveCapture={detach}
    onScrollCapture={event => {
      const element = event.target
      if (element instanceof HTMLElement && element.matches('.control-viewport, .control-reflex-scroll')
        && !isWorkflowAutomaticScroll(element)) detach()
    }}
    onBlurCapture={event => {
      if (!(event.target instanceof HTMLElement) || !event.target.closest('[data-control-node]')) return
      const next = event.relatedTarget instanceof HTMLElement ? event.relatedTarget.closest('[data-control-node]') : null
      if (!next || !event.currentTarget.contains(next)) resume()
    }}
    onKeyDownCapture={event => {
      if (!(event.target instanceof HTMLElement) || event.target.matches('input, textarea, select, [contenteditable=true]')) return
      if (['ArrowUp', 'ArrowDown', 'ArrowLeft', 'ArrowRight', 'PageUp', 'PageDown', 'Home', 'End', ' '].includes(event.key)
        && event.target.closest('.control-viewport, .control-reflex-scroll')) {
        detach()
        if (event.target.matches('.control-viewport, .control-reflex-scroll')) {
          event.preventDefault()
          scrollWorkflowKey(event.target, event.key, event.shiftKey)
        }
      }
    }}
    onPointerDownCapture={event => {
      const element = event.target
      if (!(element instanceof HTMLElement) || !element.matches('.control-viewport, .control-reflex-scroll')) return
      const bounds = element.getBoundingClientRect()
      if (element.scrollHeight > element.clientHeight && event.clientX >= bounds.right - Math.max(12, element.offsetWidth - element.clientWidth)
        || element.scrollWidth > element.clientWidth && event.clientY >= bounds.bottom - Math.max(12, element.offsetHeight - element.clientHeight)) detach()
    }}>
    <summary className="workflow-summary">
      {workflow.live ? <Loader2 className="workflow-summary-icon animate-spin" /> : <GitBranch className="workflow-summary-icon" />}
      <span>{t('workflow.title')}</span><span className="workflow-count">{t('workflow.steps', { count: workflow.nodes.length })}</span>
      {!!failed && <span className="workflow-failure">{t('workflow.failures', { count: failed })}</span>}
      <span className="workflow-state">{t(workflow.live ? 'workflow.live' : liveNodes.some(node => node.background) ? 'workflow.backgroundLive' : 'workflow.recorded')}</span><ChevronDown className="workflow-chevron" />
    </summary>
    <div className="workflow-toolbar">
      <div className="workflow-scopes" role="group" aria-label={t('workflow.scope')}>
        {(['all', 'foreground', 'background'] as const).filter(value => value === 'all' || (value === 'background' ? backgroundCount > 0 : workflow.nodes.length > backgroundCount)).map(value => <button key={value} type="button" aria-pressed={scope === value}
          onClick={() => { setScope(value); follow() }}>{t(`workflow.${value}`)}<span>{value === 'all' ? workflow.nodes.length : value === 'background' ? backgroundCount : workflow.nodes.length - backgroundCount}</span></button>)}
      </div>
      <button type="button" className="workflow-follow" aria-pressed={following && selected === undefined && !replaying} onClick={follow}><ArrowDownToLine />{t(liveNodes.length ? 'workflow.followLive' : 'workflow.latest')}</button>
    </div>
    <WorkflowReview nodes={visible} onSelect={selectNode} />
    <div className="workflow-visual">
      <JEVControlFlow nodes={snapshot} edges={workflow.edges} current={current} frame={frame}
        moving={playing || !replaying && (current?.state === 'pending' || packets.some(packet => packet.target === current?.id))}
        replaying={replaying} playing={playing} followCurrent={followCurrent} revealVersion={revealVersion}
        onSelect={selectNode} renderItem={renderItem} />
      <ControlPlayback frames={frames} cursor={cursor}
        onSeek={index => { focusedBreakpoint.current = false; detach(); setFrameId(frames[index]?.id); setSelected(undefined); setRevealVersion(value => value + 1) }} onLive={follow} />
    </div>
  </details>
}
