import React, { useState } from 'react'
import { createRoot } from 'react-dom/client'
import { TooltipProvider } from '@cyber/ui'
import type { ForumThread, IoaMessageRecord } from '@cyber/ioa'
import { HarnessGraph } from '../../src/components/HarnessGraph'
import { harnessGraph, parseHarness } from '../../src/lib/ioa-harness'
import '../../src/index.css'

const messages: IoaMessageRecord[] = [
  { id: 'input-42', content_type: 'text', content: { text: 'Audit release R42.' }, sender: 'reviewer' },
  {
    id: 'router-handoff-42', content_type: 'handoff', sender: 'router',
    content: { title: 'Inbox handoff to auditor', message: 'Audit release R42.' }, refs: { messages: ['input-42'] },
    meta: { version: 1, event: 'inbox.route', id: 'handoff-42', source: { node_id: 'reviewer', session_id: 'external-review' }, target: { task_id: 'task-42' } },
  },
  {
    id: 'worker-delegate-42', content_type: 'handoff', sender: 'router',
    content: { title: 'Delegate to auditor', message: 'Check the release evidence.' },
    meta: { version: 1, event: 'subagent.delegate', id: 'delegate-execution-42', source: { session_id: 'coordinator' }, target: { task_id: 'task-42', session_id: 'execution-42' } },
  },
  {
    id: 'worker-return-42', content_type: 'handoff', sender: 'router',
    content: { title: 'Return from auditor', message: 'Audit passed.' }, refs: { messages: ['worker-delegate-42'] },
    meta: { version: 1, event: 'subagent.return', id: 'return-execution-42', source: { task_id: 'task-42', session_id: 'execution-42' }, target: { session_id: 'coordinator' } },
  },
  {
    id: 'task-result-42', content_type: 'task_result', sender: 'router', content: { text: 'Audit passed.' },
    refs: { messages: ['input-42'], nodes: ['reviewer'] },
    meta: { version: 1, event: 'task.result', id: 'feedback-execution-42', source: { task_id: 'task-42', session_id: 'execution-42' }, target: { session_id: 'external-review' }, chain: ['task-42'] },
  },
]
const thread: ForumThread = {
  id: 'space:release-r42', spaceId: 'release-r42', spaceName: 'Release R42',
  root: messages[0], messages, title: 'Release R42', contentType: 'space', targets: [], messageCount: messages.length, pendingCount: 0,
}
;(window as any).harnessFixture = { messages, project: harnessGraph, parse: parseHarness }
function Fixture() {
  const [selectedMessageId, setSelectedMessageId] = useState('')
  ;(window as any).selectedGraphMessage = selectedMessageId
  return <TooltipProvider><div className="h-screen bg-background p-4 text-foreground">
    <HarnessGraph thread={thread} selectedMessageId={selectedMessageId} onSelectMessage={setSelectedMessageId} mode="dialog" />
  </div></TooltipProvider>
}
createRoot(document.getElementById('root')!).render(<Fixture />)
