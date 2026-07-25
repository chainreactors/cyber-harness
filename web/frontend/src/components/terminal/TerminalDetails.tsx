import { useTranslation } from 'react-i18next'
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
} from '@cyber/terminal'

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
  const { t } = useTranslation('agent')
  const runtime = agent.runtime || {}
  const agentStatus = agent.status || { bound: false }
  const stats = agent.stats || {}
  const running = taskSessions.filter((s) => s.state === 'running').length
  const closed = taskSessions.length - running

  return (
    <DetailPanel title={t('tdDetails')} onClose={onClose}>
      <DetailGroup title={t('tdAgent')}>
        <DetailRow label={t('tdName')} value={agent.name} />
        <DetailRow label="ID" value={agent.id} mono />
        <DetailRow label={t('tdState')} value={agent.busy ? t('busy') : t('idle')} />
        <DetailRow label={t('tdConnected')} value={formatDateTime(agent.connected_at)} />
        <DetailRow label={t('tdHost')} value={runtime.hostname} />
        <DetailRow label={t('tdUser')} value={runtime.username} />
        <DetailRow label={t('tdRuntime')} value={[runtime.os, runtime.arch].filter(Boolean).join('/')} />
        <DetailRow label="PID" value={runtime.pid} />
        <DetailRow label="CWD" value={runtime.working_dir} mono />
        <DetailRow label="LLM" value={[agentStatus.provider, agentStatus.model].filter(Boolean).join(' / ') || t('offline')} />
        <DetailRow label={t('tdSpace')} value={agentStatus.space} />
      </DetailGroup>

      <DetailGroup title={t('tdActiveSession')}>
        <DetailRow label={t('tdConsole')} value={status} />
        {session ? (
          <>
            <DetailRow label={t('tdTitle')} value={sessionTitle(session)} />
            <DetailRow label="ID" value={session.id} mono />
            <DetailRow label={t('tdKind')} value={session.kind} />
            <DetailRow label={t('tdState')} value={stateLabel(session.state || '') || session.state} />
            <DetailRow label={t('tdCommand')} value={session.command} mono />
            <DetailRow label="PID" value={positiveNumber(session.pid)} />
            <DetailRow label={t('tdStarted')} value={formatDateTime(session.started_at)} />
            <DetailRow label={t('tdActivity')} value={formatDateTime(session.last_activity_at)} />
            <DetailRow label={t('tdEnded')} value={formatDateTime(session.ended_at)} />
            <DetailRow label={t('tdExit')} value={session.state === 'running' ? undefined : session.exit_code} />
            <DetailRow label={t('tdKill')} value={session.kill_cause} />
            <DetailRow label={t('tdOutput')} value={formatBytes(session.output_bytes)} />
          </>
        ) : (
          <DetailRow label={t('tdState')} value={t('starting')} />
        )}
      </DetailGroup>

      <DetailGroup title={t('tdTasks')}>
        <DetailRow label={t('tdTotal')} value={taskSessions.length} />
        <DetailRow label={t('tdRunning')} value={running} />
        <DetailRow label={t('tdClosed')} value={closed} />
        <DetailRow label={t('tdCommands')} value={agent.commands?.join(', ')} />
        <DetailRow label={t('tdCapabilities')} value={runtime.capabilities?.join(', ')} />
      </DetailGroup>

      <DetailGroup title={t('tdStats')}>
        <DetailRow label={t('tdTurns')} value={stats.turns} />
        <DetailRow label={t('tdTools')} value={stats.tool_calls} />
        <DetailRow label={t('tdRunning')} value={stats.running_tools} />
        <DetailRow label={t('tdTokens')} value={stats.total_tokens} />
        <DetailRow label={t('tdAssets')} value={stats.assets} />
        <DetailRow label={t('tdLoots')} value={stats.loots} />
        <DetailRow label={t('tdLast')} value={stats.last_event} />
      </DetailGroup>
    </DetailPanel>
  )
}
