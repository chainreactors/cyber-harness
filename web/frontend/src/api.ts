export interface ScanJob {
  id: string;
  target: string;
  mode: string;
  verify?: boolean;
  sniper?: boolean;
  ai?: boolean;
  deep?: boolean;
  status: 'queued' | 'running' | 'completed' | 'failed' | 'canceled';
  progress?: string;
  report?: string;
  result?: ScanResult;
  error?: string;
  project?: string;
  /** Batch targets dropped by validation on submit (transient, create only). */
  skipped?: { target: string; reason: string }[];
  created_at: string;
  updated_at: string;
}

import type { SCONode } from '@cyber/cstx-easm';
import { Code, ConnectError, createClient } from '@connectrpc/connect'
import { createConnectTransport } from '@connectrpc/connect-web'
import {
  ChatService,
  ScanService,
  ScanStatus,
  SessionService,
  type Event as AOPEvent,
  type EventDelivery,
  type Scan as ProtoScan,
  type SessionRecord,
} from '@cyber/aop';
export type { SCONode };
export type { AOPEvent };

const connectTransport = createConnectTransport({
  baseUrl: window.location.origin,
  useBinaryFormat: false,
})

// One AIScan facade is initialized for the application. The generated service
// clients are lightweight API groups and all share this single transport.
const aiscanRPC = {
  chat: createClient(ChatService, connectTransport),
  sessions: createClient(SessionService, connectTransport),
  scans: createClient(ScanService, connectTransport),
}

export interface ScanResult {
  summary: ScanResultSummary;
  assets?: Asset[];
  nodes?: SCONode[];
  services?: unknown[];
  web_probes?: unknown[];
  loots?: Loot[];
  errors?: ResultError[];
}

export interface ScanResultSummary {
  targets: number;
  services: number;
  webs: number;
  probes: number;
  loots: number;
  errors: number;
  tasks: number;
  requests: number;
  duration: string;
  started_at?: string;
  finished_at?: string;
}

export interface Asset {
  id: string;
  key: string;
  target: string;
  title?: string;
  status?: string;
  items?: AssetItem[];
}

export type AssetItemKind = 'service' | 'path' | 'fingerprint' | 'loot' | 'note' | 'response' | 'error';

export interface AssetItem {
  kind: AssetItemKind;
  source?: string;
  target?: string;
  status?: string;
  title?: string;
  summary?: string;
  detail?: string;
  tags?: string[];
  data?: Record<string, unknown>;
  raw?: string;
}

export interface Loot {
  kind: string;
  target: string;
  priority?: string;
  description?: string;
  tags?: string[];
  data?: Record<string, unknown>;
}

export interface ResultError {
  source?: string;
  message: string;
}

// One entry in the shared asset pool (deduplicated by target). Source is
// 'scan' | 'agent' | 'manual'.
export interface PoolAsset {
  id: string;
  project_id?: string;
  target: string;
  label?: string;
  source?: string;
  status?: string;
  note?: string;
  services?: number;
  webs?: number;
  loots?: number;
  last_scan_id?: string;
  first_seen: string;
  last_seen: string;
}

export interface ScanEvent {
  type: 'progress' | 'status' | 'stats' | 'complete' | 'error';
  scan_id: string;
  data?: string;
  status?: string;
  error?: string;
  result?: ScanResult;
}

export interface ScanOptions {
  verify: boolean;
  sniper: boolean;
  deep: boolean;
}

export interface ServerStatus {
  version: string;
  llm_available: boolean;
  llm_provider?: string;
  llm_model?: string;
  llm_api_key_configured?: boolean;
  config_path?: string;
  config_loaded: boolean;
  agents: number;
  ioa_url?: string;
}

export const AUTH_REQUIRED_EVENT = 'aiscan:auth-required'

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

export interface AgentInfo {
  id: string;
  name: string;
  commands?: string[];
  busy: boolean;
  connected_at: string;
  node: NodeRef;
  runtime?: AgentRuntime;
  status?: AgentStatus;
  stats?: AgentStats;
}

export interface NodeRef {
  id: string;
  authority: string;
}

export interface AgentRuntime {
  hostname?: string;
  username?: string;
  working_dir?: string;
  os?: string;
  arch?: string;
  pid?: number;
  capabilities?: string[];
  meta?: Record<string, unknown>;
}

export interface AgentStatus {
  provider?: string;
  model?: string;
  space?: string;
  bound: boolean;
}

export interface AgentStats {
  turns?: number;
  tool_calls?: number;
  running_tools?: number;
  prompt_tokens?: number;
  completion_tokens?: number;
  total_tokens?: number;
  cache_read_tokens?: number;
  cache_write_tokens?: number;
  assets?: number;
  loots?: number;
  last_event?: string;
  current_tool?: string;
  current_detail?: string;
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

export interface LLMProfileStatus {
  id: string
  name: string
  provider: string
  base_url: string
  api_key_configured: boolean
  model: string
  proxy: string
  context_window?: number
  max_tokens?: number
}

export interface LLMProviderProfile {
  id: string
  name: string
  provider: string
  base_url: string
  api_key: string
  model: string
  proxy: string
  context_window?: number
  max_tokens?: number
}

// ConfigStatus — GET /api/config response (secrets masked, *_configured flags)
export interface ConfigStatus {
  config_path?: string;
  config_loaded: boolean;
  llm: {
    provider: string
    base_url: string
    api_key_configured: boolean
    model: string
    proxy: string
    context_window?: number
    max_tokens?: number
    active_profile?: string
    profiles?: LLMProfileStatus[]
  };
  cyberhub: { url: string; key_configured: boolean; mode: string; proxy: string };
  recon: { fofa_email: string; fofa_key_configured: boolean; hunter_token_configured: boolean; hunter_api_key_configured: boolean; proxy: string; limit?: number };
  scan: { verify: string; verify_timeout: number };
  search: { tavily_keys_configured: boolean };
  ioa: { url: string; token_configured: boolean; node_name: string; space: string };
  agent: { tools: string[]; timeout: number; save_session: boolean };
}

// DistributeConfig — PUT /api/config request body (with secret values)
export interface DistributeConfig {
  llm: {
    active_profile: string
    providers: LLMProviderProfile[]
  };
  cyberhub: { url: string; key: string; mode: string; proxy: string };
  recon: { fofa_email: string; fofa_key: string; hunter_token: string; hunter_api_key: string; proxy: string; limit?: number };
  scan: { verify: string; verify_timeout: number };
  search: { tavily_keys: string };
  ioa: { url: string; token: string; node_name: string; space: string };
  agent: { tools: string[]; timeout: number; save_session: boolean };
}

export async function getStatus(): Promise<ServerStatus> {
  return apiJSON('/api/status', 'Failed to load status');
}

export async function listAgents(): Promise<AgentInfo[]> {
  return apiJSON('/api/agents', 'Failed to list agents');
}

export async function getConfigStatus(): Promise<ConfigStatus> {
  return apiJSON('/api/config', 'Failed to load config');
}

export async function saveConfig(config: DistributeConfig): Promise<ConfigStatus> {
  return apiJSON('/api/config', 'Failed to save config', {
    method: 'PUT',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(config),
  });
}

export async function activateLLMProfile(id: string): Promise<ConfigStatus> {
  return apiJSON('/api/config/llm/active', 'Failed to switch LLM profile', {
    method: 'PUT',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ id }),
  })
}

// LLMTestRequest — POST /api/config/llm/test body. Leave api_key blank to
// reuse the key already stored on the server.
export interface LLMTestRequest {
  profile_id?: string;
  provider: string;
  base_url: string;
  api_key: string;
  model: string;
  proxy: string;
}

// LLMTestResult — outcome of a connectivity probe. ok=false carries the
// failure reason in `error`; transport/HTTP errors never reject the promise.
export interface LLMTestResult {
  ok: boolean;
  provider: string;
  model: string;
  latency_ms: number;
  reply?: string;
  error?: string;
}

export async function testLLM(req: LLMTestRequest): Promise<LLMTestResult> {
  return apiJSON('/api/config/llm/test', 'Failed to test LLM', {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(req),
  });
}

// LLMModelsRequest — POST /api/config/llm/models body. Like LLMTestRequest but
// without a model (listing is what fills the model field). Leave api_key blank
// to reuse the key already stored on the server.
export interface LLMModelsRequest {
  profile_id?: string;
  provider: string;
  base_url: string;
  api_key: string;
  proxy: string;
}

// LLMModelsResult — models the endpoint advertises via the OpenAI-compatible
// GET /models route. ok=false carries the reason in `error`.
export interface LLMModelsResult {
  ok: boolean;
  supported: boolean;
  models?: string[];
  error?: string;
}

export async function listLLMModels(req: LLMModelsRequest): Promise<LLMModelsResult> {
  return apiJSON('/api/config/llm/models', 'Failed to list models', {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(req),
  });
}

// ConnCheck — outcome of probing one external dependency within a settings
// section. A section may return several checks (Recon probes FOFA and Hunter
// independently). ok=false carries the reason in `error`.
export interface ConnCheck {
  name: string; // fofa | hunter | cyberhub | tavily | ioa | recon
  ok: boolean;
  latency_ms: number;
  detail?: string;
  error?: string;
}

export interface ConnTestResponse {
  checks: ConnCheck[];
}

// testConn probes the external dependencies of a settings section
// (cyberhub | recon | search | ioa). The current (possibly unsaved) form is
// sent so edits are tested; blank secrets fall back to stored values server-side.
export async function testConn(section: string, config: DistributeConfig): Promise<ConnTestResponse> {
  return apiJSON(`/api/config/${section}/test`, 'Failed to test connection', {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(config),
  });
}

// --- Local agents (hub-hosted nodes: one-click launch + delete) ---

export interface LocalAgentView {
  name: string
  pid: number
  registered: boolean
  busy?: boolean
}

export async function launchLocalAgent(): Promise<LocalAgentView> {
  return apiJSON('/api/deploy/local', 'Failed to launch local agent', {
    method: 'POST',
  })
}

export async function listLocalAgents(): Promise<LocalAgentView[]> {
  return apiJSON('/api/deploy/local', 'Failed to list local agents')
}

export async function getIOAOverview(): Promise<IOAOverview> {
  // Polled every 3s while the IOA console is open. A stalled hub must surface
  // as an error the console can show, not a forever-pending request.
  return apiJSON('/api/ioa/overview', 'Failed to load IOA console', {
    signal: AbortSignal.timeout(10_000),
  })
}

export async function stopLocalAgent(name: string): Promise<void> {
  await apiJSON(`/api/deploy/local/${encodeURIComponent(name)}`, 'Failed to delete local agent', {
    method: 'DELETE',
  })
}

export async function submitScan(target: string, mode: string, options: ScanOptions, project?: string): Promise<ScanJob> {
	void project
	try {
		const response = await aiscanRPC.scans.submitScan({ requestId: newRPCID(), target, mode, options })
		if (response.outcome.case !== 'accepted') throw rejectionError(response.outcome.value, 'Failed to submit scan')
		return scanToView(response.outcome.value)
	} catch (error) {
		throw connectFailure(error, 'Failed to submit scan')
	}
}

export async function getAssets(project?: string): Promise<PoolAsset[]> {
  const q = project ? `?project=${encodeURIComponent(project)}` : '';
  return apiJSON(`/api/assets${q}`, 'Failed to load assets');
}

export async function addAssets(targets: string[], source?: string, label?: string, project?: string): Promise<PoolAsset[]> {
  return apiJSON('/api/assets', 'Failed to add assets', {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ targets, source, label, project }),
  });
}

export async function deleteAsset(id: string, project?: string): Promise<void> {
  const q = project ? `?project=${encodeURIComponent(project)}` : '';
  await apiJSON(`/api/assets/${encodeURIComponent(id)}${q}`, 'Failed to delete asset', { method: 'DELETE' });
}

// One passive-recon hit, normalized to an importable pool target + display bits.
export interface ReconHit {
  target: string;
  ip?: string;
  port?: string;
  host?: string;
  url?: string;
  title?: string;
  icp?: string;
}

export interface ReconSearchResult {
  source: string;
  sources: string[]; // every source the hub has credentials for (UI selector)
  hits: ReconHit[];
}

// Run a passive-recon query (FOFA / Hunter / …) via the hub. Errors — no
// credentials, bad query, upstream failure — reject with the server's message.
export async function reconSearch(source: string, query: string, limit?: number): Promise<ReconSearchResult> {
  return apiJSON('/api/recon/search', 'Recon search failed', {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ source, query, limit }),
  });
}

// --- Projects (asset-pool scope) ---

export interface Project {
  id: string
  name: string
  assets: number
  created_at: string
}

export async function getProjects(): Promise<Project[]> {
  return apiJSON('/api/projects', 'Failed to load projects');
}

export async function createProject(name: string, id?: string): Promise<Project> {
  return apiJSON('/api/projects', 'Failed to create project', {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ name, id }),
  });
}

// Cascades on the server: the project and its entire asset pool are removed.
export async function deleteProject(id: string): Promise<void> {
  await apiJSON(`/api/projects/${encodeURIComponent(id)}`, 'Failed to delete project', { method: 'DELETE' });
}

export async function getScan(id: string): Promise<ScanJob> {
	try {
		const response = await aiscanRPC.scans.getScan({ scanId: id })
		if (!response.scan) throw new Error('Scan not found')
		return scanToView(response.scan)
	} catch (error) {
		throw connectFailure(error, 'Scan not found')
	}
}

export async function listScans(project?: string): Promise<ScanJob[]> {
	void project
	try {
		const response = await aiscanRPC.scans.listScans({})
		return response.scans.map(scanToView)
	} catch (error) {
		throw connectFailure(error, 'Failed to list scans')
	}
}

export async function deleteScan(id: string): Promise<void> {
	try {
		const response = await aiscanRPC.scans.cancelScan({ requestId: newRPCID(), scanId: id })
		if (response.outcome.case !== 'accepted') throw rejectionError(response.outcome.value, 'Failed to cancel scan')
	} catch (error) {
		throw connectFailure(error, 'Failed to cancel scan')
	}
}

export function subscribeScanEvents(
  id: string,
  onEvent: (event: ScanEvent) => void,
): () => void {
	const controller = new AbortController()
	void (async () => {
		try {
			for await (const response of aiscanRPC.scans.watchScanEvents({ scanId: id }, { signal: controller.signal })) {
				const value = response.event
				if (!value) continue
				switch (value.payload.case) {
					case 'snapshot': {
						const scan = scanToView(value.payload.value)
						onEvent({ type: scan.status === 'completed' ? 'complete' : scan.status === 'failed' || scan.status === 'canceled' ? 'error' : 'status', scan_id: id, status: scan.status, error: scan.error, result: scan.result })
						break
					}
					case 'status':
						onEvent({ type: 'status', scan_id: id, status: scanStatusName(value.payload.value) })
						break
					case 'progress':
						onEvent({ type: 'progress', scan_id: id, data: value.payload.value.data })
						break
					case 'stats':
						onEvent({ type: 'stats', scan_id: id })
						break
					case 'completed':
						onEvent({ type: 'complete', scan_id: id, status: 'completed', result: decodeEncoded<ScanResult>(value.payload.value.result) })
						break
					case 'failed':
						onEvent({ type: 'error', scan_id: id, status: value.payload.value.canceled ? 'canceled' : 'failed', error: value.payload.value.message })
						break
				}
			}
		} catch (error) {
			if (!controller.signal.aborted) connectFailure(error, 'Scan event stream disconnected')
		}
	})()
	return () => controller.abort()
}

// --- Chat session types ---

export interface ChatSession {
  id: string
  agent_id: string
  agent_name?: string
  title: string
  status: 'active' | 'archived'
  scan_ids?: string[]
  created_at: string
  updated_at: string
}

export interface ChatMessage {
  id: string
  session_id: string
  role: 'user' | 'assistant' | 'system'
  agent_id?: string
  agent_name?: string
  content: string
  metadata?: Record<string, unknown>
  created_at: string
  cursor?: number
  turn_id?: string
}

// --- Chat session API ---

export async function createChatSession(agentID: string, title?: string, scanID?: string): Promise<ChatSession> {
  void scanID
  try {
    const response = await aiscanRPC.chat.openSession({
      requestId: newRPCID(),
      sessionId: newRPCID(),
      participant: agentID,
      title: title || '',
    })
    if (response.outcome.case !== 'accepted') throw rejectionError(response.outcome.value, 'Failed to create session')
    const now = new Date().toISOString()
    return {
      id: response.outcome.value.id,
      agent_id: response.outcome.value.participant,
      title: response.outcome.value.title,
      status: response.outcome.value.state === 'closed' ? 'archived' : 'active',
      created_at: now,
      updated_at: now,
    }
  } catch (error) {
    throw connectFailure(error, 'Failed to create session')
  }
}

export async function listChatSessions(): Promise<ChatSession[]> {
  try {
    const response = await aiscanRPC.sessions.listSessions({ limit: 100, includeClosed: true })
    return response.sessions.map(sessionRecordToView)
  } catch (error) {
    throw connectFailure(error, 'Failed to list sessions')
  }
}

export async function getChatSession(id: string): Promise<ChatSession> {
  try {
    const response = await aiscanRPC.sessions.getSession({ sessionId: id })
    if (!response.session) throw new Error('Session not found')
    return sessionRecordToView(response.session)
  } catch (error) {
    throw connectFailure(error, 'Session not found')
  }
}

export async function deleteChatSession(id: string): Promise<void> {
  try {
    const response = await aiscanRPC.sessions.deleteSession({ requestId: newRPCID(), sessionId: id })
    if (response.outcome.case !== 'accepted') throw rejectionError(response.outcome.value, 'Failed to delete session')
  } catch (error) {
    throw connectFailure(error, 'Failed to delete session')
  }
}

export async function resetChatSession(id: string): Promise<ChatSession> {
  try {
    const response = await aiscanRPC.sessions.resetSession({ requestId: newRPCID(), sessionId: id })
    if (response.outcome.case !== 'accepted' || !response.outcome.value.current) {
      throw rejectionError(response.outcome.case === 'rejected' ? response.outcome.value : undefined, 'Failed to reset session')
    }
    return sessionRecordToView(response.outcome.value.current)
  } catch (error) {
    throw connectFailure(error, 'Failed to reset session')
  }
}

// SlashCommandSpec mirrors pkg/slashcmd.Spec — the server's canonical view of a
// "/verb" command. The web "/" menu is built from these so it always reflects
// the hub's + bound agent's real command set instead of a hardcoded list.
export interface SlashCommandSpec {
  name: string
  aliases?: string[]
  usage?: string
  description?: string
  scope: number
  web_menu?: boolean
}

export async function fetchSessionCommands(sessionID: string): Promise<SlashCommandSpec[]> {
  try {
    const response = await aiscanRPC.sessions.listCommands({ sessionId: sessionID })
    return response.commands.map((command) => ({
      name: command.name,
      aliases: command.aliases,
      usage: command.usage,
      description: command.description,
      scope: 0,
    }))
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
    evalMaxRounds?: number
    messageID?: string
    turnID?: string
    requestID?: string
    continueSession?: boolean
  },
): Promise<ChatMessage> {
  const messageID = opts?.messageID || newRPCID()
  const turnID = opts?.turnID || newRPCID()
  const extensions = []
  const criteria = opts?.persist ? opts.evalCriteria?.trim() : ''
  if (criteria) {
    const value = JSON.stringify({ evalCriteria: criteria, evalMaxRounds: Math.max(opts?.evalMaxRounds || 0, 0) })
    extensions.push({
      namespace: 'io.chainreactors.aiscan.run',
      value: { data: new TextEncoder().encode(value), mediaType: 'application/protobuf+json' },
    })
  }
  try {
    const response = await aiscanRPC.chat.runTurn({
      requestId: opts?.requestID || newRPCID(),
      sessionId: sessionID,
      turnId: turnID,
      continueSession: opts?.continueSession === true,
      input: {
        id: messageID,
        role: 'user',
        content: opts?.continueSession ? [] : [{ value: { case: 'text', value: { text: content } } }],
      },
      extensions,
    })
    if (response.outcome.case !== 'accepted') throw rejectionError(response.outcome.value, 'Failed to send message')
    return {
      id: messageID,
      session_id: sessionID,
      role: 'user',
      content,
      created_at: new Date().toISOString(),
      turn_id: response.outcome.value.turnId,
    }
  } catch (error) {
    throw connectFailure(error, 'Failed to send message')
  }
}

export async function executeChatCommand(sessionID: string, line: string): Promise<void> {
  try {
    const response = await aiscanRPC.sessions.executeCommand({ requestId: newRPCID(), sessionId: sessionID, line })
    if (response.outcome.case !== 'accepted') throw rejectionError(response.outcome.value, 'Failed to execute command')
  } catch (error) {
    throw connectFailure(error, 'Failed to execute command')
  }
}

export async function cancelChatSession(sessionID: string, turnID: string): Promise<void> {
  if (!turnID) throw new Error('No active turn')
  try {
    const response = await aiscanRPC.chat.cancelTurn({
      requestId: newRPCID(), sessionId: sessionID, turnId: turnID, reason: 'user_requested',
    })
    if (response.outcome.case !== 'accepted') throw rejectionError(response.outcome.value, 'Failed to pause response')
  } catch (error) {
    throw connectFailure(error, 'Failed to pause response')
  }
}

export async function closeChatSession(sessionID: string): Promise<void> {
  try {
    const response = await aiscanRPC.chat.closeSession({
      requestId: newRPCID(), sessionId: sessionID, reason: 'completed',
    })
    if (response.outcome.case !== 'accepted') throw rejectionError(response.outcome.value, 'Failed to close session')
  } catch (error) {
    throw connectFailure(error, 'Failed to close session')
  }
}

export interface FileUploadResult {
  filename: string
  path: string
  size: number
  error?: string
}

export async function uploadChatFile(sessionID: string, file: File): Promise<FileUploadResult> {
  try {
    const response = await aiscanRPC.sessions.uploadSessionFile({
      requestId: newRPCID(),
      sessionId: sessionID,
      filename: file.name,
      mediaType: file.type || 'application/octet-stream',
      data: new Uint8Array(await file.arrayBuffer()),
    })
    if (response.outcome.case !== 'accepted') throw rejectionError(response.outcome.value, 'Upload failed')
    return {
      filename: response.outcome.value.filename,
      path: response.outcome.value.path,
      size: Number(response.outcome.value.size),
    }
  } catch (error) {
    throw connectFailure(error, 'Upload failed')
  }
}

export async function listChatMessages(sessionID: string): Promise<ChatMessage[]> {
  try {
    const response = await aiscanRPC.chat.listEvents({ sessionId: sessionID, limit: 500 })
    return response.events.flatMap((delivery) => deliveryToChatMessage(delivery) || [])
  } catch (error) {
    throw connectFailure(error, 'Failed to list messages')
  }
}

// Fetch a scan's markdown report, re-rendered server-side in the given language
// ('en' | 'zh'). Returns '' when the report isn't ready yet (404) so callers can
// just show a placeholder.
export async function fetchScanReport(scanID: string, lang: string): Promise<string> {
	try {
		const response = await aiscanRPC.scans.getScanReport({ scanId: scanID, language: lang })
		return response.markdown
	} catch {
		return ''
	}
}

export function subscribeAOPEvents(
  sessionID: string,
  onEvent: (event: AOPEvent) => void,
  onReconnect?: () => void,
): () => void {
  const controller = new AbortController()
  let cursor = ''
  void (async () => {
    let retry = 250
    while (!controller.signal.aborted) {
      try {
        for await (const response of aiscanRPC.chat.watchEvents(
          { sessionId: sessionID, afterCursor: cursor },
          { signal: controller.signal },
        )) {
          const delivery = response.delivery
          if (!delivery?.event) continue
          cursor = delivery.cursor
          onEvent(delivery.event)
          retry = 250
        }
      } catch (error) {
        if (controller.signal.aborted) return
        connectFailure(error, 'Event stream disconnected')
        onReconnect?.()
        await new Promise((resolve) => window.setTimeout(resolve, retry))
        retry = Math.min(retry * 2, 5000)
      }
    }
  })()
  return () => controller.abort()
}

function sessionRecordToView(record: SessionRecord): ChatSession {
  const session = record.session
  if (!session) throw new Error('Session record is missing its AOP session')
  return {
    id: session.id,
    agent_id: session.participant,
    agent_name: record.agentName || undefined,
    title: session.title,
    status: session.state === 'closed' ? 'archived' : 'active',
    scan_ids: record.scanIds,
    created_at: timestampToISOString(record.createdAt),
    updated_at: timestampToISOString(record.updatedAt),
  }
}

function timestampToISOString(value?: { seconds: bigint; nanos: number }): string {
  if (!value) return new Date(0).toISOString()
  return new Date(Number(value.seconds) * 1000 + Math.floor(value.nanos / 1_000_000)).toISOString()
}

function scanStatusName(value: ScanStatus): ScanJob['status'] {
	switch (value) {
		case ScanStatus.QUEUED: return 'queued'
		case ScanStatus.RUNNING: return 'running'
		case ScanStatus.COMPLETED: return 'completed'
		case ScanStatus.FAILED: return 'failed'
		case ScanStatus.CANCELED: return 'canceled'
		default: return 'queued'
	}
}

function decodeEncoded<T>(value?: { data: Uint8Array }): T | undefined {
	if (!value?.data?.length) return undefined
	try {
		return JSON.parse(new TextDecoder().decode(value.data)) as T
	} catch {
		return undefined
	}
}

function scanToView(scan: ProtoScan): ScanJob {
	return {
		id: scan.id,
		target: scan.target,
		mode: scan.mode,
		verify: scan.options?.verify,
		sniper: scan.options?.sniper,
		deep: scan.options?.deep,
		status: scanStatusName(scan.status),
		progress: scan.progress || undefined,
		report: scan.report || undefined,
		result: decodeEncoded<ScanResult>(scan.result),
		error: scan.error || undefined,
		created_at: timestampToISOString(scan.createdAt),
		updated_at: timestampToISOString(scan.updatedAt),
	}
}

function deliveryToChatMessage(delivery: EventDelivery): ChatMessage | null {
  const event = delivery.event
  if (!event || event.payload.case !== 'message') return null
  const message = event.payload.value
  const text = message.content
    .filter((part) => part.value.case === 'text')
    .map((part) => part.value.case === 'text' ? part.value.value.text : '')
    .join('\n')
  const webExtension = event.extensions.find((extension) => extension.namespace === 'io.chainreactors.aiscan.web')
  let metadata: Record<string, unknown> | undefined
  let agentID: string | undefined
  if (webExtension?.value?.data?.length) {
    try {
      const decoded = JSON.parse(new TextDecoder().decode(webExtension.value.data)) as Record<string, unknown>
      agentID = typeof decoded.agentId === 'string' ? decoded.agentId : undefined
      metadata = decoded.metadata && typeof decoded.metadata === 'object' ? decoded.metadata as Record<string, unknown> : undefined
    } catch {}
  }
  const role = message.role === 'assistant' || message.role === 'system' ? message.role : 'user'
  return {
    id: message.id,
    session_id: event.sessionId,
    role,
    agent_id: agentID,
    agent_name: event.emitter,
    content: text,
    metadata,
    created_at: timestampToISOString(event.emittedAt),
    cursor: delivery.cursor ? Number(delivery.cursor) : undefined,
    turn_id: event.turnId || undefined,
  }
}

function rejectionError(value: { code?: string; message?: string } | undefined, fallback: string): Error {
  return new Error(value?.message || value?.code || fallback)
}

function connectFailure(error: unknown, fallback: string): Error {
  const failure = ConnectError.from(error)
  if (failure.code === Code.Unauthenticated) window.dispatchEvent(new Event(AUTH_REQUIRED_EVENT))
  return new Error(failure.rawMessage || failure.message || fallback)
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

export function agentTerminalWebSocketURL(agentID: string): string {
  const protocol = window.location.protocol === 'https:' ? 'wss:' : 'ws:';
  return `${protocol}//${window.location.host}/api/agents/${encodeURIComponent(agentID)}/terminal/ws`;
}

// ── SCO Nodes ──

export async function listSCONodes(opts?: { type?: string; scanId?: string; limit?: number }): Promise<SCONode[]> {
  const params = new URLSearchParams();
  if (opts?.type) params.set('type', opts.type);
  if (opts?.scanId) params.set('scan_id', opts.scanId);
  if (opts?.limit) params.set('limit', String(opts.limit));
  const qs = params.toString();
  return apiJSON(`/api/sco/nodes${qs ? '?' + qs : ''}`, 'Failed to load SCO nodes');
}

export async function getSCONode(id: string): Promise<SCONode> {
  return apiJSON(`/api/sco/nodes/${encodeURIComponent(id)}`, 'SCO node not found');
}

export async function getSCOStats(): Promise<Record<string, number>> {
  return apiJSON('/api/sco/stats', 'Failed to load SCO stats');
}

export async function getSupportedArtifacts(): Promise<string[]> {
  return apiJSON('/api/sco/artifacts', 'Failed to load artifacts');
}

export async function importSCOData(
  file: File,
  artifact: string,
  scanId = 'import',
): Promise<{ status: string; nodes: number; artifact: string; duplicates: number }> {
  const form = new FormData();
  form.append('file', file);
  form.append('artifact', artifact);
  form.append('scan_id', scanId);
  const resp = await authenticatedFetch('/api/sco/import', { method: 'POST', body: form });
  if (!resp.ok) {
    const err = await resp.json().catch(() => ({ error: resp.statusText }));
    throw new Error(err.error || `Import failed: ${resp.status}`);
  }
  return resp.json();
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
