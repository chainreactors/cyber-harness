import React from 'react'
import { createRoot } from 'react-dom/client'
import '../../src/i18n'
import { aopClient } from '../../src/api'
import { useChatSession } from '../../src/hooks/useChatSession'

const state = window as any
state.openRequests = []
;(aopClient as any).request = async (_schema: unknown, message: any, opts: any) => {
  state.openRequests.push({ id: opts.id, sessionID: message.message.value.sessionId, nodeID: message.message.value.nodeId })
  throw new Error('connection lost after request')
}
function Fixture() {
  const session = useChatSession()
  state.session = session
  return <div>{session.agents.length} nodes</div>
}
createRoot(document.getElementById('root')!).render(<Fixture />)
