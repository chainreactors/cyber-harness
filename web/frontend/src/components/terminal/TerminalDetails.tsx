import type { AgentInfo } from '../../api'
import {
  type PTYSession,
  type TerminalStatus,
  DetailPanel,
  DetailGroup,
  DetailRow,
  formatBytes,
  formatDateTime,
  positiveNumber,
  sessionTitle,
  stateLabel,
} from '@aspect/terminal'

export function TerminalDetails({
  agent,
  onClose,
  session,
  status,
  taskSessions,
}: {
  agent: AgentInfo
  onClose: () => void
  session: PTYSession | null
  status: TerminalStatus
  taskSessions: PTYSession[]
}) {
  const identity = agent.identity || {}
  const stats = agent.stats || {}
  const running = taskSessions.filter((s) => s.state === 'running').length
  const closed = taskSessions.length - running

  return (
    <DetailPanel title="Details" onClose={onClose}>
      <DetailGroup title="Agent">
        <DetailRow label="Name" value={agent.name} />
        <DetailRow label="ID" value={agent.id} mono />
        <DetailRow label="State" value={agent.busy ? 'busy' : 'idle'} />
        <DetailRow label="Connected" value={formatDateTime(agent.connected_at)} />
        <DetailRow label="Host" value={identity.hostname} />
        <DetailRow label="User" value={identity.username} />
        <DetailRow label="Runtime" value={[identity.os, identity.arch].filter(Boolean).join('/')} />
        <DetailRow label="PID" value={identity.pid} />
        <DetailRow label="CWD" value={identity.working_dir} mono />
        <DetailRow label="LLM" value={[identity.provider, identity.model].filter(Boolean).join(' / ') || 'offline'} />
        <DetailRow label="Space" value={identity.space} />
      </DetailGroup>

      <DetailGroup title="Active Session">
        <DetailRow label="Console" value={status} />
        {session ? (
          <>
            <DetailRow label="Title" value={sessionTitle(session)} />
            <DetailRow label="ID" value={session.id} mono />
            <DetailRow label="Kind" value={session.kind} />
            <DetailRow label="State" value={stateLabel(session.state || '') || session.state} />
            <DetailRow label="Command" value={session.command} mono />
            <DetailRow label="PID" value={positiveNumber(session.pid)} />
            <DetailRow label="Started" value={formatDateTime(session.started_at)} />
            <DetailRow label="Activity" value={formatDateTime(session.last_activity_at)} />
            <DetailRow label="Ended" value={formatDateTime(session.ended_at)} />
            <DetailRow label="Exit" value={session.state === 'running' ? undefined : session.exit_code} />
            <DetailRow label="Kill" value={session.kill_cause} />
            <DetailRow label="Output" value={formatBytes(session.output_bytes)} />
          </>
        ) : (
          <DetailRow label="State" value="starting" />
        )}
      </DetailGroup>

      <DetailGroup title="Tasks">
        <DetailRow label="Total" value={taskSessions.length} />
        <DetailRow label="Running" value={running} />
        <DetailRow label="Closed" value={closed} />
        <DetailRow label="Commands" value={agent.commands?.join(', ')} />
        <DetailRow label="Capabilities" value={identity.capabilities?.join(', ')} />
      </DetailGroup>

      <DetailGroup title="Stats">
        <DetailRow label="Turns" value={stats.turns} />
        <DetailRow label="Tools" value={stats.tool_calls} />
        <DetailRow label="Running" value={stats.running_tools} />
        <DetailRow label="Tokens" value={stats.total_tokens} />
        <DetailRow label="Assets" value={stats.assets} />
        <DetailRow label="Loots" value={stats.loots} />
        <DetailRow label="Last" value={stats.last_event} />
      </DetailGroup>
    </DetailPanel>
  )
}
