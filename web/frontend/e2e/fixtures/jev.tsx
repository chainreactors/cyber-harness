import { ClaimType } from '../../src/gen/decision/claim_pb'
import { startFixtureRuntime } from './runtime'
import React, { useState } from 'react'
import { createRoot } from 'react-dom/client'
import { create, fromBinary, fromJson } from '@bufbuild/protobuf'
import { anyPack, timestampFromDate } from '@bufbuild/protobuf/wkt'
import { CircuitBoard } from 'lucide-react'
import { TooltipProvider } from '@cyber/ui'
import { EventSchema } from '../../cyber-ui/packages/aop/src/gen/aop/event_pb'
import { RuntimeEventSchema, ProtocolMessageSchema, GetLibraryResponseSchema } from '../../src/gen/types/jev_pb'
import { RefSchema, StartedSchema } from '../../cyber-ui/packages/aop/src/gen/aop/operation/protocol_pb'
import ChatPanel from '../../src/components/ChatPanel'
import ReflexPanel from '../../src/components/ReflexPanel'
import { aopClient } from '../../src/api'
import '../../src/i18n'
import '../../src/index.css'

const runtime = await startFixtureRuntime()

const definition = { id: 'reflex-browser-evidence', when: 'Inspect a requested page, retrieve current content and report from recorded evidence.',
  decide: 'Select supplied native bindings. Inspect after effects, report with actual content, defer for new reasoning.',
  observe: 'js:function(context,args){return {report:{content:context.history.at(-1)?.text}};}', readers: { inspect: 'function(){ return {state: {content: document.body.innerText}, candidates: choices([])}; }' }, claimIds: ['claim-browser'] }
const base = { sessionId: 'session-1', turnId: 'turn-1', emitter: 'agent' }
const event = (seq: number, payload: any, fields = {}) => create(EventSchema, { ...base, id: `event-${seq}`, seq: BigInt(seq),
  emittedAt: timestampFromDate(new Date(1700000000000 + seq * 1000)), payload, ...fields })
const trace = (seq: number, payload: any, fields = {}) => event(seq, { case: 'extension', value: anyPack(RuntimeEventSchema,
  create(RuntimeEventSchema, { taskId: 'task-1', segmentId: 'segment-1', step: 1, payload, ...fields })) }, { emitter: 'jev' })
const call = { id: 'browser-open', name: 'bash', arguments: { data: new TextEncoder().encode(JSON.stringify({ command: 'playwright open https://docs.python.org/3/library/json.html --session reference' })) } }
const initial = [event(1, { case: 'turnStarted', value: {} }), event(2, { case: 'message', value: { id: 'user', role: 'user', content: [{ value: { case: 'text', value: { text: '读取 Python 官方 json 文档，说明 ensure_ascii 的行为。' } } }] } }, { emitter: 'cyber.web' }),
  trace(3, { case: 'takeover', value: { definition } }),
  trace(4, { case: 'decisionRequest', value: { requestId: 'request-1', purpose: 'jev_execution', claims: {
    scene: { type: ClaimType.choice, context: 'Which current operation advances this read-only task?' + "\ninspect: Read current page\nreport: Compose answer from evidence\ndefer: New reasoning required", options: ["inspect","report","defer"] },
  } } }),
  trace(5, { case: 'decisionResult', value: { requestId: 'request-1', evaluations: { scene: { value: { case: 'choice', value: 'inspect' }, confidence: .94, probabilities: { inspect: .94, report: .04, defer: .02 } } } } }),
  trace(6, { case: 'dispatch', value: { call, candidateId: 'reflex-browser-evidence/open' } }, { callId: call.id }),
  event(7, { case: 'extension', value: anyPack(StartedSchema, create(StartedSchema, { kind: 'command', name: 'playwright' })) },
    { emitter: 'jev', extensions: [anyPack(RefSchema, create(RefSchema, { callId: call.id, operationId: 'operation-1' }))] }),
  trace(8, { case: 'result', value: { elapsedMs: 316, result: { callId: call.id, name: 'bash', output: [{ value: { case: 'text', value: { text: 'Opened session reference. Python json documentation is available.' } } }] } } }, { callId: call.id }),
  trace(9, { case: 'dispatch', value: { call: { id: 'read', name: 'bash', arguments: { data: new TextEncoder().encode(JSON.stringify({ command: 'playwright evaluate reference "document.body.innerText"' })) } }, read: true } }, { step: 2, callId: 'read' }),
  trace(10, { case: 'result', value: { elapsedMs: 42, result: { callId: 'read', name: 'bash', output: [{ value: { case: 'text', value: { text: 'If ensure_ascii is true, the output is guaranteed to have all incoming non-ASCII characters escaped.' } } }] } } }, { step: 2, callId: 'read' }),
  trace(11, { case: 'handoff', value: { reason: 'report' } }, { step: 3 }),
  event(12, { case: 'message', value: { id: 'answer', role: 'assistant', content: [{ value: { case: 'text', value: { text: '`ensure_ascii=True` 会转义非 ASCII 字符；设为 `False` 可以直接保留中文。\n\n来源：https://docs.python.org/3/library/json.html' } } }] } }),
  event(13, { case: 'turnEnded', value: { stopReason: 'completed' } }),
  trace(14, { case: 'libraryChange', value: { state: 'reflex_published', reflex: definition } }, { background: true, segmentId: '', step: 0 }),
]
Object.defineProperty(aopClient, 'connected', { configurable: true, get: () => true })
// Fixture-only transport: production components still use their real query API.
let fixtureLibrary = create(GetLibraryResponseSchema, { mode: 'auto', status: 'ready',
  reflexes: [definition], claims: [{ id: 'claim-browser', type: ClaimType.choice, context: definition.when + '\nChoose the next operation: inspect reads fresh page state; report uses observed content; defer asks for new reasoning.',
    options: ['inspect', 'report', 'defer'], sourceTaskId: 'task-1' }] })
;(window as any).renderJEVLibrary = (value: any) => { fixtureLibrary = fromJson(GetLibraryResponseSchema, value) }
aopClient.request = async () => create(ProtocolMessageSchema, { message: { case: 'library', value: fixtureLibrary } })

function Fixture() {
  const [events, setEvents] = useState(initial), [open, setOpen] = useState(false)
  ;(window as any).renderJEVEvents = (values: number[][], append = false) => {
    const decoded = values.map(value => fromBinary(EventSchema, new Uint8Array(value)))
    setEvents(current => append ? [...current, ...decoded] : decoded)
  }
  return <TooltipProvider><div className="flex h-screen flex-col bg-background text-foreground">
    <header className="flex h-12 shrink-0 items-center justify-between border-b border-border px-5"><span className="text-sm font-semibold">cyber-scan</span>
      <button aria-label="Reflex" onClick={() => setOpen(v => !v)} data-tool-drawer-trigger className="flex h-8 w-8 items-center justify-center rounded-md hover:bg-accent"><CircuitBoard className="h-4 w-4" /></button></header>
    <div className="min-h-0 flex-1"><ChatPanel resolveExtension={runtime.slots.resolve} extensionRevision={runtime.slots.revision.getSnapshot()} timeline={[]} aopEvents={events} guardrailReviews={[]} onResolveGuardrail={async () => {}} scanResults={new Map()}
      isThinking={false} isBusy={false} canPause={false} error="" hasActiveSession activeSessionID="session-1"
      onSend={async () => true} ensureSession={async () => 'session-1'} onPause={() => {}} onClearError={() => {}} /></div>
    <ReflexPanel open={open} onClose={() => setOpen(false)} sessionID="session-1" events={events} />
  </div></TooltipProvider>
}
createRoot(document.getElementById('root')!).render(<Fixture />)
