import type { SCONode } from '@cyber/cstx-easm';
import { Code, ConnectError, createClient } from '@connectrpc/connect'
import { createConnectTransport } from '@connectrpc/connect-web'
import { create, type MessageInitShape } from '@bufbuild/protobuf'
import { anyPack } from '@bufbuild/protobuf/wkt'
import {
  AOPClient,
  AOPProtocolMessageSchema,
  FileProtocolMessageSchema,
  type AOPPayload,
  type AOPProtocolMessage,
  type Event as AOPEvent,
  type EventDelivery,
  type FileResult,
  type Session,
  type TurnReceipt,
} from '@cyber/aop';
import {
  AgentService,
  CommandProtocolMessageSchema,
  ConfigService,
  LLMProbeRequestSchema,
  ReloadProtocolMessageSchema,
  AgentRunOptionsSchema,
  ArtifactService,
  ScanProtocolMessageSchema,
  ScanService,
  ScanStatus,
  SessionBindingSchema,
  SessionService,
  SystemService,
  type AgentView,
  type AgentListMetadata,
  type CommandProtocolMessage,
  type CommandSpec,
  type ConfigView,
  type DistributeConfig,
  type LLMProbeRequest,
  type LLMProbeResult,
  type ListModelsResult,
  type TestConnectionResponse,
  type ConnectionCheck,
  type LLMProviderView,
  type Scan,
  type ScanOptions,
  type SessionRecord,
  type SystemStatus as ServerStatus,
} from './cyber-proto'

export type { SCONode };
export type { AOPEvent };

// Generated proto types are the single source of truth for the API surface.
// Re-exported here so views keep a single import site for API types.
export type { AgentListMetadata, AgentView, CommandSpec, ConfigView, DistributeConfig, EventDelivery, LLMProviderView, LLMProbeRequest, LLMProbeResult, ListModelsResult, TestConnectionResponse, ConnectionCheck, Scan, ScanOptions, SessionRecord, ServerStatus, TurnReceipt };
export type { Session as AOPSession } from '@cyber/aop';
export { ScanStatus };

// Binary framing, matching the AOP channel. The events these services return
// carry google.protobuf.Any extensions whose type set is open — SessionHistory,
// SessionScanEvent, the observe payloads, and whatever a future agent attaches.
// JSON framing expands an Any by looking its type up in a client-side registry,
// so one unregistered extension makes the entire response undecodable; binary
// framing leaves the Any as {typeUrl, value} for the caller's own anyUnpack.
const connectTransport = createConnectTransport({
  baseUrl: window.location.origin,
  useBinaryFormat: true,
})

// One Cyber facade is initialized for the application. The generated service
// clients are lightweight API groups and all share this single transport.
const cyberRPC = {
  sessions: createClient(SessionService, connectTransport),
  scans: createClient(ScanService, connectTransport),
  config: createClient(ConfigService, connectTransport),
  agents: createClient(AgentService, connectTransport),
  system: createClient(SystemService, connectTransport),
  artifacts: createClient(ArtifactService, connectTransport),
}
const aopClient = new AOPClient()
  .register(CommandProtocolMessageSchema)
  .register(ScanProtocolMessageSchema)
  .register(ReloadProtocolMessageSchema)

export const AUTH_REQUIRED_EVENT = 'cyber:auth-required'

export class APIError extends Error {
  constructor(message: string, public readonly status: number) {
    super(message)
    this.name = 'APIError'
  }
}

export async function getAuthSession(): Promise<boolean> {
  const res = await fetch('/api/auth/session', { cache: 'no-store' })
  if (!res.ok) return false
  const body = await res.json() as { authenticated?: boolean }
  return body.authenticated === true
}

export async function login(token: string): Promise<void> {
  const res = await fetch('/api/auth/login', {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ token }),
  })
  if (!res.ok) {
    throw new APIError(await errorMessage(res, 'Login failed'), res.status)
  }
}

export async function logout(): Promise<void> {
  try {
    await fetch('/api/auth/logout', { method: 'POST' })
  } finally {
    notifyAuthRequired()
  }
}

export async function getAgentConnectToken(): Promise<string> {
  const body = await apiJSON<{ token?: string }>(
    '/api/auth/agent-token',
    'Failed to configure the agent token',
    { cache: 'no-store' },
  )
  return body.token ?? ''
}

export interface IOAIdentityBinding {
  namespace: string
  subject: string
  claims?: Record<string, unknown>
}

export interface IOANode {
  id: string
  name: string
  description?: string
  meta?: Record<string, unknown>
  identities?: IOAIdentityBinding[]
}

export interface IOASpace {
  id: string
  name: string
  tags?: string[]
  nodes?: IOANode[]
  message_count: number
}

export interface IOARef {
  messages?: string[]
  nodes?: string[]
}

export interface IOAMessage {
  id: string
  space_id: string
  sender: string
  created_at: string
  content_type?: string
  content: Record<string, unknown>
  refs?: IOARef
  meta?: Record<string, unknown>
  content_schema?: Record<string, unknown>
}

export interface IOAOverview {
  nodes: IOANode[]
  spaces: IOASpace[]
  messages: IOAMessage[]
}

export async function getStatus(): Promise<ServerStatus> {
  try {
    const response = await cyberRPC.system.getStatus({})
    if (!response.status) throw new Error('Status is unavailable')
    return response.status
  } catch (error) {
    throw connectFailure(error, 'Failed to load status')
  }
}

export async function listAgents(): Promise<AgentView[]> {
  try {
    const response = await cyberRPC.agents.listAgents({})
    return response.agents
  } catch (error) {
    throw connectFailure(error, 'Failed to list agents')
  }
}

// The single "is the LLM usable" predicate: a provider client exists and it
// targets a model. An API key is deliberately not required — self-hosted
// endpoints (Ollama, vLLM) are keyless — and the server already merges the
// runtime provider with the stored settings view before answering.
export function llmConfigured(status: ServerStatus | null | undefined): boolean {
  return !!(status?.llmAvailable && status.llmModel?.trim())
}

export async function getConfigStatus(): Promise<ConfigView> {
  try {
    const response = await cyberRPC.config.getConfig({})
    if (!response.config) throw new Error('Config is unavailable')
    return response.config
  } catch (error) {
    throw connectFailure(error, 'Failed to load config')
  }
}

export async function saveConfig(config: DistributeConfig): Promise<ConfigView> {
  try {
    const response = await cyberRPC.config.updateConfig({ config })
    if (!response.config) throw new Error('Config update returned no view')
    return response.config
  } catch (error) {
    throw connectFailure(error, 'Failed to save config')
  }
}

export async function activateLLMProfile(id: string): Promise<ConfigView> {
  try {
    const response = await cyberRPC.config.activateProfile({ profileId: id })
    if (!response.config) throw new Error('Profile activation returned no view')
    return response.config
  } catch (error) {
    throw connectFailure(error, 'Failed to switch LLM profile')
  }
}

// Blank api_key asks the server to reuse the stored secret.
export async function testLLM(req: MessageInitShape<typeof LLMProbeRequestSchema>): Promise<LLMProbeResult> {
  return cyberRPC.config.testLLM(req)
}

export async function listLLMModels(req: MessageInitShape<typeof LLMProbeRequestSchema>): Promise<ListModelsResult> {
  return cyberRPC.config.listModels(req)
}

// testConn probes the external dependencies of a settings section
// (cyberhub | recon | search | ioa.client). The current form is
// sent so edits are tested; blank secrets fall back to stored values server-side.
export async function testConn(section: string, config: DistributeConfig): Promise<TestConnectionResponse> {
  return cyberRPC.config.testConnection({ section, config })
}

// --- IOA overview ---

export async function getIOAOverview(): Promise<IOAOverview> {
  const signal = AbortSignal.timeout(10_000)
  const [nodes, spaces, messages] = await Promise.all([
    apiJSON<IOANode[]>('/ioa/nodes', 'Failed to load IOA nodes', { signal }),
    apiJSON<IOASpace[]>('/ioa/spaces', 'Failed to load IOA spaces', { signal }),
    apiJSON<IOAMessage[]>('/ioa/messages', 'Failed to load IOA messages', { signal }),
  ])
  return { nodes, spaces, messages }
}

// --- Chat session API ---

export async function createChatSession(nodeID: string, title?: string, scanID?: string, opts?: { sessionID: string; requestID: string }): Promise<Session> {
  try {
	const extensions = scanID
		? [anyPack(SessionBindingSchema, create(SessionBindingSchema, { scanId: scanID }))]
		: []
	const response = await requestCore(create(AOPProtocolMessageSchema, { message: { case: 'openSessionRequest', value: { sessionId: opts?.sessionID || newRPCID(), nodeId: nodeID, title: title || '', extensions } } }), 'openSessionResponse', opts?.requestID)
    if (response.outcome.case !== 'accepted') throw rejectionError(response.outcome.value, 'Failed to create session')
    return response.outcome.value
  } catch (error) {
    throw connectFailure(error, 'Failed to create session')
  }
}

export type SessionFilters = { search: string; nodeId: string; archived: boolean; target: string; view: 'tasks' | 'nodes' | 'targets' }
export async function listChatSessions(filters?: Partial<SessionFilters>): Promise<SessionRecord[]> {
  const sessions: SessionRecord[] = []
  let afterCursor = ''
  do {
    const response = await cyberRPC.sessions.listSessions({ ...filters, afterCursor, limit: 100, includeClosed: true })
    sessions.push(...response.sessions)
    afterCursor = response.nextCursor
  } while (afterCursor)
  return [...new Map(sessions.map((record) => [record.session?.id, record])).values()]
}

export async function updateChatSession(sessionId: string, patch: { title?: string; archived?: boolean }): Promise<SessionRecord> {
  const response = await cyberRPC.sessions.updateSession({ requestId: newRPCID(), sessionId, ...patch })
  if (response.outcome.case !== 'accepted') throw rejectionError(response.outcome.value, 'Failed to update session')
  return response.outcome.value
}

export async function getChatSession(id: string): Promise<SessionRecord> {
  try {
    const response = await cyberRPC.sessions.getSession({ sessionId: id })
    if (!response.session) throw new Error('Session not found')
    return response.session
  } catch (error) {
    throw connectFailure(error, 'Session not found')
  }
}

export async function deleteChatSession(id: string): Promise<void> {
  try {
    const response = await cyberRPC.sessions.deleteSession({ requestId: newRPCID(), sessionId: id })
    if (response.outcome.case !== 'accepted') throw rejectionError(response.outcome.value, 'Failed to delete session')
  } catch (error) {
    throw connectFailure(error, 'Failed to delete session')
  }
}

export async function resetChatSession(id: string): Promise<SessionRecord> {
  try {
    const response = await cyberRPC.sessions.resetSession({ requestId: newRPCID(), sessionId: id })
    if (response.outcome.case !== 'accepted' || !response.outcome.value.current) {
      throw rejectionError(response.outcome.case === 'rejected' ? response.outcome.value : undefined, 'Failed to reset session')
    }
    return response.outcome.value.current
  } catch (error) {
    throw connectFailure(error, 'Failed to reset session')
  }
}

// The web "/" menu is built from the cyber.chat.CommandSpec list returned by
// SessionService/ListCommands (hub-scope commands merged with the bound
// agent's reported commands), so it always reflects the real command set
// instead of a hardcoded list.
export async function fetchSessionCommands(sessionID: string): Promise<CommandSpec[]> {
  try {
    const response = await cyberRPC.sessions.listCommands({ sessionId: sessionID })
    return response.commands
  } catch (error) {
    throw connectFailure(error, 'Failed to load commands')
  }
}

export async function sendChatMessage(
  sessionID: string,
  content: string,
  opts?: {
    persist?: boolean
    evalCriteria?: string
    evalRounds?: string
    messageID?: string
    turnID?: string
    requestID?: string
    continueSession?: boolean
  },
): Promise<TurnReceipt> {
  const messageID = opts?.messageID || newRPCID()
  const turnID = opts?.turnID || newRPCID()
  const extensions = []
  const criteria = opts?.persist ? opts.evalCriteria?.trim() : ''
  if (criteria) {
    const value = create(AgentRunOptionsSchema, { evalCriteria: criteria, evalRounds: opts?.evalRounds?.trim() || '' })
    extensions.push(anyPack(AgentRunOptionsSchema, value))
  }
  try {
	const request = create(AOPProtocolMessageSchema, { message: { case: 'runTurnRequest', value: {
	  sessionId: sessionID, turnId: turnID, continueSession: opts?.continueSession === true,
	  input: { id: messageID, role: 'user', content: opts?.continueSession ? [] : [{ value: { case: 'text', value: { text: content } } }] }, extensions,
	} } })
	const response = await requestCore(request, 'runTurnResponse', opts?.requestID)
    if (response.outcome.case !== 'accepted') throw rejectionError(response.outcome.value, 'Failed to send message')
    return response.outcome.value
  } catch (error) {
    throw connectFailure(error, 'Failed to send message')
  }
}

export async function executeChatCommand(sessionID: string, line: string, requestID?: string): Promise<void> {
  try {
	const request = create(CommandProtocolMessageSchema, { message: { case: 'request', value: { sessionId: sessionID, line } } })
	const response = await aopClient.request(CommandProtocolMessageSchema, request, { id: requestID })
	if (response.$typeName === 'aop.ProtocolMessage') {
		const core = response as AOPProtocolMessage
		if (core.message.case === 'protocolError') throw new Error(core.message.value.message)
	}
  } catch (error) {
	throw error instanceof Error ? error : new Error('Failed to execute command')
  }
}

export async function cancelChatSession(sessionID: string, turnID: string): Promise<void> {
  if (!turnID) throw new Error('No active turn')
  try {
	const response = await requestCore(create(AOPProtocolMessageSchema, { message: { case: 'cancelTurnRequest', value: { sessionId: sessionID, turnId: turnID, reason: 'user_requested' } } }), 'cancelTurnResponse')
    if (response.outcome.case !== 'accepted') throw rejectionError(response.outcome.value, 'Failed to pause response')
  } catch (error) {
    throw connectFailure(error, 'Failed to pause response')
  }
}

export async function closeChatSession(sessionID: string): Promise<void> {
  try {
	const response = await requestCore(create(AOPProtocolMessageSchema, { message: { case: 'closeSessionRequest', value: { sessionId: sessionID, reason: 'completed' } } }), 'closeSessionResponse')
    if (response.outcome.case !== 'accepted') throw rejectionError(response.outcome.value, 'Failed to close session')
  } catch (error) {
    throw connectFailure(error, 'Failed to close session')
  }
}

export type FileUploadResult = FileResult

export async function uploadChatFile(sessionID: string, file: File): Promise<FileUploadResult> {
  try {
	const request = create(FileProtocolMessageSchema, { message: { case: 'uploadRequest', value: { sessionId: sessionID, filename: file.name, mediaType: file.type || 'application/octet-stream', data: new Uint8Array(await file.arrayBuffer()) } } })
	const payload = await aopClient.request(FileProtocolMessageSchema, request)
	if (payload.$typeName === 'aop.file.ProtocolMessage') {
		const filePayload = payload as import('@cyber/aop').FileProtocolMessage
		if (filePayload.message.case === 'result') return filePayload.message.value
	}
	if (payload.$typeName === 'aop.ProtocolMessage') {
		const core = payload as AOPProtocolMessage
		if (core.message.case === 'protocolError') throw new Error(core.message.value.message)
	}
	throw new Error('Upload returned an unexpected response')
  } catch (error) {
    throw connectFailure(error, 'Upload failed')
  }
}

// Chat history is the raw AOP event log; views project EventDelivery records
// into their own render models (see useChatSession.deliveryToChatMessage).
export async function listChatMessages(sessionID: string): Promise<EventDelivery[]> {
  try {
	const response = await cyberRPC.sessions.listEvents({ sessionId: sessionID, limit: 500 })
    return response.events
  } catch (error) {
    throw connectFailure(error, 'Failed to list messages')
  }
}

export function subscribeAOPEvents(
  sessionID: string,
  onEvent: (event: AOPEvent) => void,
  onReconnect?: () => void,
): () => void {
	const watch = (cursor: string) => create(AOPProtocolMessageSchema, { message: { case: 'watchEventsRequest', value: { sessionId: sessionID, afterCursor: cursor } } })
	return aopClient.subscribe(AOPProtocolMessageSchema, watch(''), (payload) => {
		if (payload.$typeName !== 'aop.ProtocolMessage') return
		const core = payload as AOPProtocolMessage
		if (core.message.case === 'event') onEvent(core.message.value)
	}, { durable: true, resume: (cursor) => { onReconnect?.(); return watch(cursor) } })
}

function rejectionError(value: { code?: string; message?: string } | undefined, fallback: string): Error {
  return Object.assign(new Error(value?.message || value?.code || fallback), { rejected: true })
}

function connectFailure(error: unknown, fallback: string): Error {
  if ((error as { rejected?: boolean })?.rejected && error instanceof Error) return error
  const failure = ConnectError.from(error)
  if (failure.code === Code.Unauthenticated) window.dispatchEvent(new Event(AUTH_REQUIRED_EVENT))
  return new Error(failure.rawMessage || failure.message || fallback)
}

type CoreMessage = AOPProtocolMessage['message']
type CoreCase = Exclude<CoreMessage['case'], undefined>
type CoreValue<C extends CoreCase> = Extract<CoreMessage, { case: C }>['value']

async function requestCore<C extends CoreCase>(request: AOPProtocolMessage, expected: C, id?: string): Promise<CoreValue<C>> {
	const payload = await aopClient.request(AOPProtocolMessageSchema, request, { id })
	if (payload.$typeName !== 'aop.ProtocolMessage') throw new Error(`Unexpected AOP response ${payload.$typeName}`)
	const core = payload as AOPProtocolMessage
	if (core.message.case === 'protocolError') throw new Error(core.message.value.message)
	if (core.message.case !== expected) throw new Error(`Expected ${expected}, received ${core.message.case || 'empty'}`)
	return core.message.value as CoreValue<C>
}

function newRPCID(): string {
  const value = globalThis.crypto
  if (value && typeof value.randomUUID === 'function') {
    try { return value.randomUUID() } catch {}
  }
  if (value && typeof value.getRandomValues === 'function') {
    const bytes = new Uint8Array(16)
    value.getRandomValues(bytes)
    bytes[6] = (bytes[6] & 0x0f) | 0x40
    bytes[8] = (bytes[8] & 0x3f) | 0x80
    const hex = Array.from(bytes, (item) => item.toString(16).padStart(2, '0')).join('')
    return `${hex.slice(0, 8)}-${hex.slice(8, 12)}-${hex.slice(12, 16)}-${hex.slice(16, 20)}-${hex.slice(20)}`
  }
  return `${Date.now().toString(36)}-${Math.random().toString(36).slice(2)}`
}

export { aopClient }

export async function syncArtifactEvents(afterCursor = '', artifacts: AOPEvent[] = []): Promise<EventDelivery[]> {
  try {
    const response = await cyberRPC.artifacts.syncArtifacts({ afterCursor, artifacts })
    return response.artifacts
  } catch (error) {
    throw connectFailure(error, 'Failed to sync artifacts')
  }
}

async function apiJSON<T>(path: string, fallbackMessage: string, init?: RequestInit): Promise<T> {
  const res = await authenticatedFetch(path, init)
  if (!res.ok) {
    throw new APIError(await errorMessage(res, fallbackMessage), res.status)
  }
  return res.json();
}

async function authenticatedFetch(input: RequestInfo | URL, init?: RequestInit): Promise<Response> {
  const res = await fetch(input, init)
  if (res.status === 401) notifyAuthRequired()
  return res
}

function notifyAuthRequired() {
  window.dispatchEvent(new Event(AUTH_REQUIRED_EVENT))
}

async function errorMessage(res: Response, fallback: string) {
  try {
    const body = await res.json();
    return body?.error || fallback;
  } catch {
    return fallback;
  }
}

export async function getScan(id: string): Promise<Scan> {
  const response = await cyberRPC.scans.getScan({ scanId: id })
  if (!response.scan) throw new Error('Scan not found')
  return response.scan
}
