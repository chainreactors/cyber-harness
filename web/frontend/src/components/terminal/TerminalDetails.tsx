import { useTranslation } from 'react-i18next'
import type { AgentView } from '../../api'
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
  agent: AgentView
  onClose: () => void
  session: PTYSession | null
  status: TerminalStatus
  taskSessions: PTYSession[]
}) {
  const { t } = useTranslation('agent')
  const runtime = agent.hello?.runtime
  const agentStatus = agent.status
  const stats = agent.stats
  const running = taskSessions.filter((s) => s.state === 'running').length
  const closed = taskSessions.length - running

  return (
    <DetailPanel title={t('tdDetails')} onClose={onClose}>
      <DetailGroup title={t('tdAgent')}>
        <DetailRow label={t('tdName')} value={agent.hello?.name} />
        <DetailRow label="ID" value={agent.hello?.agentId} mono />
        <DetailRow label={t('tdState')} value={agent.busy ? t('busy') : t('idle')} />
        <DetailRow label={t('tdConnected')} value={formatDateTime(agent.connectedAt)} />
        <DetailRow label={t('tdHost')} value={runtime?.hostname} />
        <DetailRow label={t('tdUser')} value={runtime?.username} />
        <DetailRow label={t('tdRuntime')} value={[runtime?.os, runtime?.arch].filter(Boolean).join('/')} />
        <DetailRow label="PID" value={runtime?.pid} />
        <DetailRow label="CWD" value={runtime?.workingDir} mono />
        <DetailRow label="LLM" value={[agentStatus?.provider, agentStatus?.model].filter(Boolean).join(' / ') || t('offline')} />
        <DetailRow label={t('tdSpace')} value={agentStatus?.space} />
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
            <DetailRow label={t('tdStarted')} value={formatDateTime(session.startedAt)} />
            <DetailRow label={t('tdActivity')} value={formatDateTime(session.lastActivityAt)} />
            <DetailRow label={t('tdEnded')} value={formatDateTime(session.endedAt)} />
            <DetailRow label={t('tdExit')} value={session.state === 'running' ? undefined : session.exitCode} />
            <DetailRow label={t('tdKill')} value={session.killCause} />
            <DetailRow label={t('tdOutput')} value={formatBytes(session.outputBytes)} />
          </>
        ) : (
          <DetailRow label={t('tdState')} value={t('starting')} />
        )}
      </DetailGroup>

      <DetailGroup title={t('tdTasks')}>
        <DetailRow label={t('tdTotal')} value={taskSessions.length} />
        <DetailRow label={t('tdRunning')} value={running} />
        <DetailRow label={t('tdClosed')} value={closed} />
        <DetailRow label={t('tdCommands')} value={agent.commands.map((command) => command.name).join(', ')} />
        <DetailRow label={t('tdCapabilities')} value={agent.hello?.capabilities.join(', ')} />
      </DetailGroup>

      <DetailGroup title={t('tdStats')}>
        <DetailRow label={t('tdTurns')} value={stats ? String(stats.turns) : undefined} />
        <DetailRow label={t('tdTools')} value={stats ? String(stats.toolCalls) : undefined} />
        <DetailRow label={t('tdRunning')} value={stats ? String(stats.runningTools) : undefined} />
        <DetailRow label={t('tdTokens')} value={stats ? String(stats.totalTokens) : undefined} />
        <DetailRow label={t('tdLast')} value={stats?.lastEvent} />
      </DetailGroup>
    </DetailPanel>
  )
}
