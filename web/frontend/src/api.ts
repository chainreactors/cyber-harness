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
export type { SCONode };

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

type RawScanEventType = ScanEvent['type'] | 'output';

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

export interface AgentInfo {
  id: string;
  name: string;
  commands?: string[];
  busy: boolean;
  connected_at: string;
  identity?: AgentIdentity;
  stats?: AgentStats;
}

export interface AgentIdentity {
  node_id?: string;
  node_name?: string;
  space?: string;
  ioa_url?: string;
  hostname?: string;
  username?: string;
  working_dir?: string;
  os?: string;
  arch?: string;
  pid?: number;
  provider?: string;
  model?: string;
  capabilities?: string[];
  meta?: Record<string, unknown>;
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

// ConfigStatus — GET /api/config response (secrets masked, *_configured flags)
export interface ConfigStatus {
  config_path?: string;
  config_loaded: boolean;
  llm: { provider: string; base_url: string; api_key_configured: boolean; model: string; proxy: string };
  cyberhub: { url: string; key_configured: boolean; mode: string; proxy: string };
  recon: { fofa_email: string; fofa_key_configured: boolean; hunter_token_configured: boolean; hunter_api_key_configured: boolean; proxy: string; limit?: number };
  scan: { verify: string; verify_timeout: number };
  search: { tavily_keys_configured: boolean };
  ioa: { url: string; token_configured: boolean; node_name: string; space: string };
  agent: { tools: string[]; timeout: number; save_session: boolean };
}

// DistributeConfig — PUT /api/config request body (with secret values)
export interface DistributeConfig {
  llm: { provider: string; base_url: string; api_key: string; model: string; proxy: string };
  cyberhub: { url: string; key: string; mode: string; proxy: string };
  recon: { fofa_email: string; fofa_key: string; hunter_token: string; hunter_api_key: string; proxy: string; limit?: number };
  scan: { verify: string; verify_timeout: number };
  search: { tavily_keys: string };
  ioa: { url: string; token: string; node_name: string; space: string };
  agent: { tools: string[]; timeout: number; save_session: boolean };
}

export interface TerminalMessage {
  type: string;
  task_id?: string;
  stream_id?: string;
  data?: string;
  data_b64?: string;
  payload?: Record<string, unknown>;
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

// LLMTestRequest — POST /api/config/llm/test body. Leave api_key blank to
// reuse the key already stored on the server.
export interface LLMTestRequest {
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
  provider: string;
  base_url: string;
  api_key: string;
  proxy: string;
}

// LLMModelsResult — models the endpoint advertises via the OpenAI-compatible
// GET /models route. ok=false carries the reason in `error`.
export interface LLMModelsResult {
  ok: boolean;
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

export async function stopLocalAgent(name: string): Promise<void> {
  await apiJSON(`/api/deploy/local/${encodeURIComponent(name)}`, 'Failed to delete local agent', {
    method: 'DELETE',
  })
}

export async function submitScan(target: string, mode: string, options: ScanOptions, project?: string): Promise<ScanJob> {
  return apiJSON('/api/scans', 'Failed to submit scan', {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ target, mode, ...options, project }),
  });
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
  return apiJSON(`/api/scans/${encodeURIComponent(id)}`, 'Scan not found');
}

export async function listScans(project?: string): Promise<ScanJob[]> {
  const q = project ? `?project=${encodeURIComponent(project)}` : '';
  return apiJSON(`/api/scans${q}`, 'Failed to list scans');
}

export async function deleteScan(id: string): Promise<void> {
  await apiJSON(`/api/scans/${encodeURIComponent(id)}`, 'Failed to delete scan', { method: 'DELETE' });
}

export function subscribeScanEvents(
  id: string,
  onEvent: (event: ScanEvent) => void,
): () => void {
  const es = new EventSource(authURL(`/api/scans/${encodeURIComponent(id)}/events`));
  const handler = (type: RawScanEventType) => (e: Event) => {
    const data = 'data' in e ? (e as MessageEvent).data : undefined;
    if (typeof data !== 'string' || data === '') {
      if (type === 'error') {
        void getScan(id)
          .then((job) => {
            if (job.status === 'completed') {
              onEvent({ type: 'complete', scan_id: id, status: job.status });
              es.close();
            } else if (job.status === 'failed' || job.status === 'canceled') {
              onEvent({
                type: 'error',
                scan_id: id,
                error: job.error || `Scan ${job.status}`,
              });
              es.close();
            }
          })
          .catch(() => {});
      }
      return;
    }

    let event: ScanEvent;
    try {
      const parsed = JSON.parse(data);
      const normalizedType = type === 'output' ? 'progress' : type;
      const parsedType = parsed?.type === 'output' ? 'progress' : parsed?.type || normalizedType;
      event = {
        scan_id: id,
        ...parsed,
        type: parsedType,
      };
    } catch {
      event = { type: type === 'output' ? 'progress' : type, scan_id: id, data };
    }

    onEvent(event);
    if (event.type === 'complete' || event.type === 'error') {
      es.close();
    }
  };
  es.addEventListener('progress', handler('progress'));
  es.addEventListener('status', handler('status'));
  es.addEventListener('stats', handler('stats'));
  es.addEventListener('complete', handler('complete'));
  es.addEventListener('error', handler('error'));
  es.addEventListener('output', handler('output'));

  return () => es.close();
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
  role: 'user' | 'assistant' | 'system' | 'tool_call' | 'tool_result'
  agent_id?: string
  agent_name?: string
  content: string
  metadata?: Record<string, unknown>
  created_at: string
}

export type ChatEventType =
  | 'message'
  | 'scan_started' | 'scan_progress' | 'scan_complete' | 'scan_error'
  | 'agent_joined' | 'eval' | 'session_cleared' | 'error'

export interface ChatEvent {
  type: ChatEventType
  session_id: string
  message_id?: string
  role?: ChatMessage['role']
  agent_id?: string
  agent_name?: string
  turn?: number
  content?: string
  scan_id?: string
  result?: ScanResult
  data?: string
  error?: string
  // System/error messages carry a stable code (+ params) so the client can
  // localize them via i18n; `content`/`error` stay as English fallbacks.
  code?: string
  params?: Record<string, unknown>
  eval_round?: number
  eval_pass?: boolean
  eval_reason?: string
}

export interface AOPEvent {
  type: string
  ts: string
  session_id: string
  agent: string
  seq?: number
  data: Record<string, unknown>
  ext?: Record<string, Record<string, unknown>>
}

// --- Chat session API ---

export async function createChatSession(agentID: string, title?: string, scanID?: string): Promise<ChatSession> {
  return apiJSON('/api/chat/sessions', 'Failed to create session', {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ agent_id: agentID, title: title || '', scan_id: scanID || '' }),
  })
}

export async function listChatSessions(): Promise<ChatSession[]> {
  return apiJSON('/api/chat/sessions', 'Failed to list sessions')
}

export async function getChatSession(id: string): Promise<ChatSession> {
  return apiJSON(`/api/chat/sessions/${encodeURIComponent(id)}`, 'Session not found')
}

export async function deleteChatSession(id: string): Promise<void> {
  await apiJSON(`/api/chat/sessions/${encodeURIComponent(id)}`, 'Failed to delete session', {
    method: 'DELETE',
  })
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
  return apiJSON(`/api/chat/sessions/${encodeURIComponent(sessionID)}/commands`, 'Failed to load commands')
}

export async function sendChatMessage(
  sessionID: string,
  content: string,
  opts?: { persist?: boolean; evalCriteria?: string; evalMaxRounds?: number },
): Promise<ChatMessage> {
  const body: Record<string, unknown> = { content }
  // Goal mode: the only run-control the backend acts on is a natural-language
  // completion criteria judged by an independent evaluator for up to N rounds
  // (webagent runChatEval). Persist without criteria is just a normal message.
  if (opts?.persist) {
    const criteria = opts.evalCriteria?.trim()
    if (criteria) {
      body.persist = true
      body.eval_criteria = criteria
      if (opts.evalMaxRounds && opts.evalMaxRounds > 0) body.eval_max_rounds = opts.evalMaxRounds
    }
  }
  return apiJSON(`/api/chat/sessions/${encodeURIComponent(sessionID)}/messages`, 'Failed to send message', {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(body),
  })
}

export async function cancelChatSession(sessionID: string): Promise<void> {
  await apiJSON(`/api/chat/sessions/${encodeURIComponent(sessionID)}/cancel`, 'Failed to pause response', {
    method: 'POST',
  })
}

export interface FileUploadResult {
  filename: string
  path: string
  size: number
  error?: string
}

export async function uploadChatFile(sessionID: string, file: File): Promise<FileUploadResult> {
  const form = new FormData()
  form.append('file', file)
  const headers: Record<string, string> = {}
  const key = getAccessKey()
  if (key) headers['Authorization'] = `Bearer ${key}`
  const resp = await fetch(`/api/chat/sessions/${encodeURIComponent(sessionID)}/upload`, {
    method: 'POST',
    headers,
    body: form,
  })
  if (!resp.ok) {
    const body = await resp.text()
    throw new Error(body || `Upload failed: ${resp.status}`)
  }
  return resp.json()
}

export async function listChatMessages(sessionID: string): Promise<ChatMessage[]> {
  return apiJSON(`/api/chat/sessions/${encodeURIComponent(sessionID)}/messages`, 'Failed to list messages')
}

// Fetch a scan's markdown report, re-rendered server-side in the given language
// ('en' | 'zh'). Returns '' when the report isn't ready yet (404) so callers can
// just show a placeholder.
export async function fetchScanReport(scanID: string, lang: string): Promise<string> {
  const headers: Record<string, string> = {}
  const key = getAccessKey()
  if (key) headers['Authorization'] = `Bearer ${key}`
  const res = await fetch(`/api/scans/${encodeURIComponent(scanID)}/report?lang=${encodeURIComponent(lang)}`, { headers })
  if (!res.ok) return ''
  return res.text()
}

export function subscribeChatEvents(
  sessionID: string,
  onEvent: (event: ChatEvent) => void,
  onReconnect?: () => void,
  onAOP?: (event: AOPEvent) => void,
  onOpen?: () => void,
): () => void {
  const url = authURL(`/api/chat/sessions/${encodeURIComponent(sessionID)}/events`)
  const es = new EventSource(url)

  es.addEventListener('open', () => onOpen?.())

  const eventTypes: ChatEventType[] = [
    'message',
    'scan_started', 'scan_progress', 'scan_complete', 'scan_error',
    'agent_joined', 'eval', 'session_cleared', 'error',
  ]

  for (const type of eventTypes) {
    es.addEventListener(type, (e: Event) => {
      const data = 'data' in e ? (e as MessageEvent).data : undefined
      if (typeof data !== 'string' || data === '') return
      try {
        const parsed = JSON.parse(data)
        onEvent({ ...parsed, type })
      } catch {
        onEvent({ type, session_id: sessionID, data } as ChatEvent)
      }
    })
  }

  es.addEventListener('aop', (e: Event) => {
    const data = 'data' in e ? (e as MessageEvent).data : undefined
    if (typeof data !== 'string' || data === '') return
    try {
      const parsed = JSON.parse(data) as AOPEvent
      if (parsed.session_id && parsed.agent && parsed.type && parsed.ts && parsed.data) onAOP?.(parsed)
    } catch {
      // Ignore malformed protocol frames; platform events continue normally.
    }
  })

  es.addEventListener('error', () => {
    // EventSource reconnects automatically. Reconcile platform-domain state
    // from REST; AOP itself is replayed from durable storage by the SSE endpoint.
    onReconnect?.()
  })

  return () => es.close()
}

export function agentTerminalWebSocketURL(agentID: string): string {
  const protocol = window.location.protocol === 'https:' ? 'wss:' : 'ws:';
  const base = `${protocol}//${window.location.host}/api/agents/${encodeURIComponent(agentID)}/terminal/ws`;
  return authURL(base);
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
  const resp = await fetch('/api/sco/import', { method: 'POST', body: form });
  if (!resp.ok) {
    const err = await resp.json().catch(() => ({ error: resp.statusText }));
    throw new Error(err.error || `Import failed: ${resp.status}`);
  }
  return resp.json();
}

function getAccessKey(): string {
  return (window as any).__AISCAN_ACCESS_KEY__ || ''
}

// For SSE/WebSocket, append access_key as query param since EventSource/WebSocket can't set headers.
function authURL(path: string): string {
  const key = getAccessKey()
  if (!key) return path
  const sep = path.includes('?') ? '&' : '?'
  return `${path}${sep}access_key=${encodeURIComponent(key)}`
}

async function apiJSON<T>(path: string, fallbackMessage: string, init?: RequestInit): Promise<T> {
  const key = getAccessKey()
  const headers = new Headers(init?.headers)
  if (key) {
    headers.set('Authorization', `Bearer ${key}`)
  }
  const res = await fetch(path, { ...init, headers });
  if (!res.ok) {
    throw new Error(await errorMessage(res, fallbackMessage));
  }
  return res.json();
}

async function errorMessage(res: Response, fallback: string) {
  try {
    const body = await res.json();
    return body?.error || fallback;
  } catch {
    return fallback;
  }
}
