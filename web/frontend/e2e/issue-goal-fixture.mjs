// Deterministic provider responses exercise the real agent, tools, evaluator and
// AOP transport without depending on an external model or scanning a target.
const streamAttempts = new Map()

export async function issueStreamReply(payload, res) {
  const messages = JSON.stringify(payload.messages || [])
  if (!messages.includes('ISSUE-145-STREAM')) return false
  const reconnect = messages.includes('reconnect')
  const toolsDone = (payload.messages || []).some(message => message.role === 'tool')
  const attempt = (streamAttempts.get(messages) || 0) + 1
  streamAttempts.set(messages, attempt)
  res.writeHead(200, { 'Content-Type': 'text/event-stream', 'Cache-Control': 'no-cache' })
  const frame = delta => res.write(`data: ${JSON.stringify({ choices: [{ index: 0, delta }] })}\n\n`)
  if (reconnect && !toolsDone) {
    frame({ content: 'Intermediate step before reconnect.', tool_calls: [{ index: 0, id: 'stream-read', type: 'function', function: {
      name: 'read', arguments: JSON.stringify({ path: 'cyber://skills/cyber/okf/easm/gogo.md' }),
    } }] })
  } else if (reconnect) {
    // Keep the next request active while the browser reconnects and fetches the
    // already persisted intermediate assistant message.
    await new Promise(resolve => setTimeout(resolve, 8000))
    frame({ content: 'Reconnect finished.' })
  } else if (attempt === 1) {
    frame({ reasoning_content: 'Stale attempt thought', content: 'Stale attempt answer' })
    await new Promise(resolve => setTimeout(resolve, 1200))
    res.destroy() // Fail after partial output, forcing the real provider retry.
    return true
  } else {
    frame({ reasoning_content: 'Mixed frame thought', content: 'Replacement answer' })
    await new Promise(resolve => setTimeout(resolve, 4000))
  }
  res.write(`data: ${JSON.stringify({ choices: [{ index: 0, delta: {}, finish_reason: reconnect && !toolsDone ? 'tool_calls' : 'stop' }] })}\n\n`)
  res.end('data: [DONE]\n\n')
  return true
}

export async function issueInteractionReply(payload, res) {
  const messages = JSON.stringify(payload.messages || [])
  if (!messages.includes('CHAT-INTERACTION')) return false
  res.writeHead(200, { 'Content-Type': 'text/event-stream', 'Cache-Control': 'no-cache' })
  const frame = delta => res.write(`data: ${JSON.stringify({ choices: [{ index: 0, delta }] })}\n\n`)
  frame({ content: 'Delayed interaction response.' })
  await new Promise(resolve => setTimeout(resolve, 7000))
  frame({ content: ' completed.' })
  res.write(`data: ${JSON.stringify({ choices: [{ index: 0, delta: {}, finish_reason: 'stop' }] })}\n\n`)
  res.end('data: [DONE]\n\n')
  return true
}

export async function issueGoalReply(payload, res) {
  const messages = JSON.stringify(payload.messages || [])
  if (!messages.includes('ISSUE-143-145')) return false
  const secondRound = messages.includes('ISSUE-143-145 round two')
  const verdict = payload.tools?.some(tool => tool.function?.name === 'verdict')
  const reply = (message, finishReason = 'stop') => {
    res.writeHead(200, { 'Content-Type': 'application/json' })
    res.end(JSON.stringify({
      id: 'chatcmpl-issue-goal',
      choices: [{ message: { role: 'assistant', ...message }, finish_reason: finishReason }],
      usage: { prompt_tokens: 100, completion_tokens: 20, total_tokens: 120 },
    }))
  }
  if (verdict) {
    reply({ tool_calls: [{ id: 'verdict', type: 'function', function: {
      name: 'verdict', arguments: JSON.stringify({
        pass: secondRound, continue: !secondRound, inherit_context: secondRound,
        reason: secondRound ? 'Both regression rounds completed' : 'Run a second regression round',
        feedback: 'ISSUE-143-145 round two: finish the regression',
      }),
    } }] }, 'tool_calls')
    return true
  }
  const toolsDone = (payload.messages || []).filter(message => message.role === 'tool').length
  if (!payload.stream) {
    reply({ content: 'ISSUE-143-145 summary' })
    return true
  }
  res.writeHead(200, { 'Content-Type': 'text/event-stream', 'Cache-Control': 'no-cache' })
  const frame = delta => res.write(`data: ${JSON.stringify({ choices: [{ index: 0, delta }] })}\n\n`)
  frame({ role: 'assistant' })
  if (!toolsDone && !secondRound) {
    frame({ reasoning_content: 'ISSUE-143-145 first reasoning\n\n' + 'Review the local regression evidence.\n\n'.repeat(120) })
    await new Promise(resolve => setTimeout(resolve, 3000))
    frame({ content: 'First step: read the embedded skill.' })
    frame({ tool_calls: [{ index: 0, id: 'issue-read', type: 'function', function: {
      name: 'read', arguments: JSON.stringify({ path: 'cyber://skills/cyber/okf/easm/gogo.md' }),
    } }] })
  } else if (toolsDone === 1 && !secondRound) {
    frame({ reasoning_content: 'ISSUE-143-145 second reasoning' })
    frame({ content: 'Second step: check command composition.' })
    frame({ tool_calls: [{ index: 0, id: 'issue-bash', type: 'function', function: {
      name: 'bash', arguments: JSON.stringify({ command: 'gogo --help&&echo ISSUE-143-145-shell-ok', timeout: 15 }),
    } }] })
  } else {
    frame({ reasoning_content: secondRound ? 'ISSUE-143-145 final reasoning' : 'ISSUE-143-145 tool review' })
    frame({ content: secondRound ? 'Round two complete.' : 'Round one complete.' })
  }
  res.write(`data: ${JSON.stringify({ choices: [{ index: 0, delta: {}, finish_reason: toolsDone < 2 && !secondRound ? 'tool_calls' : 'stop' }], usage: { prompt_tokens: 100, completion_tokens: 20, total_tokens: 120 } })}\n\n`)
  res.end('data: [DONE]\n\n')
  return true
}
