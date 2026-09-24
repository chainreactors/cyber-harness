import { create } from '@bufbuild/protobuf'
import { anyPack, anyUnpack, timestampNow } from '@bufbuild/protobuf/wkt'
import {
  ArtifactSchema,
  LootSchema,
  Correlation,
  EventSchema,
  RefSchema,
  type Event as AOPEvent,
} from '@cyber/aop'
import {
  CSTXArtifactNormalizer,
  type CanonicalSCONode,
} from '@cyber/cstx'
import type { SCONode } from '@cyber/cstx-easm'
import { syncArtifactEvents } from '../api'

const databaseName = 'cyber-cstx'
const databaseVersion = 2
const nodesStore = 'nodes'
const observationsStore = 'observations'
const metaStore = 'meta'
const evidenceStore = 'evidence'
const failuresStore = 'failures'
const operationsStore = 'operations'
const abiKey = 'abi'
const cursorKey = 'archive_cursor'
const changedEvent = 'cyber:cstx-changed'

type Observation = {
  id: string
  operation_id: string
  cstx_id: string
  node: CanonicalSCONode
}

export type ResultEvidence = {
  id: string
  operation_id: string
  result_id: string
  node_ids: string[]
  source: string
  status: string
  description: string
}
export type ObservedOperation = {
  id: string
  parent_id: string
  call_id: string
  session_id: string
  target: string
  tool: string
  timestamp: string
}
type ParseFailure = { id: string; event: AOPEvent; error: string; operation_id: string }

type Meta = {
  key: string
  value: string
}

let databasePromise: Promise<IDBDatabase> | undefined
let databaseABI: string | undefined
let runtimePromise: Promise<CSTXArtifactNormalizer> | undefined
let syncing: Promise<number> | undefined

async function database(abiVersion: string): Promise<IDBDatabase> {
  if (databaseABI && databaseABI !== abiVersion) {
    throw new Error(`CSTX database is already open for ABI ${databaseABI}`)
  }
  if (!databasePromise) {
    databaseABI = abiVersion
    databasePromise = new Promise<IDBDatabase>((resolve, reject) => {
      const request = indexedDB.open(databaseName, databaseVersion)
      request.onupgradeneeded = () => {
        const db = request.result
        for (const name of Array.from(db.objectStoreNames)) db.deleteObjectStore(name)
        db.createObjectStore(nodesStore, { keyPath: 'cstx_id' })
        const observations = db.createObjectStore(observationsStore, { keyPath: 'id' })
        observations.createIndex('operation_id', 'operation_id')
        db.createObjectStore(metaStore, { keyPath: 'key' })
        db.createObjectStore(evidenceStore, { keyPath: 'id' })
        db.createObjectStore(failuresStore, { keyPath: 'id' })
        db.createObjectStore(operationsStore, { keyPath: 'id' })
      }
      request.onsuccess = () => resolve(request.result)
      request.onerror = () => reject(request.error ?? new Error('Failed to open CSTX database'))
      request.onblocked = () => reject(new Error('CSTX database upgrade is blocked'))
    }).then(async (db) => {
      const abi = await getMeta(db, abiKey)
      if (abi !== abiVersion) {
        const transaction = db.transaction([nodesStore, observationsStore, metaStore, evidenceStore, failuresStore, operationsStore], 'readwrite')
        transaction.objectStore(nodesStore).clear()
        transaction.objectStore(observationsStore).clear()
        transaction.objectStore(evidenceStore).clear()
        transaction.objectStore(failuresStore).clear()
        transaction.objectStore(operationsStore).clear()
        transaction.objectStore(metaStore).clear()
        transaction.objectStore(metaStore).put({ key: abiKey, value: abiVersion } satisfies Meta)
        transaction.objectStore(metaStore).put({ key: cursorKey, value: '0' } satisfies Meta)
        await transactionDone(transaction)
      }
      return db
    }).catch((error) => {
      databasePromise = undefined
      databaseABI = undefined
      throw error
    })
  }
  return databasePromise!
}

async function cstxRuntime(): Promise<CSTXArtifactNormalizer> {
  if (!runtimePromise) {
    runtimePromise = CSTXArtifactNormalizer.create().then(async (runtime) => {
      const db = await database(runtime.abiVersion)
      runtime.hydrate(await getAll<CanonicalSCONode>(db, nodesStore))
      return runtime
    }).catch((error) => {
      runtimePromise = undefined
      throw error
    })
  }
  return runtimePromise
}

export async function getSupportedCSTXArtifacts(): Promise<string[]> {
  return (await cstxRuntime()).supportedArtifacts()
}

export async function importCSTXArtifact(
  file: File,
  artifact: string,
  operationId = newID(),
): Promise<number> {
  const now = timestampNow()
  const eventID = newID()
  const event = create(EventSchema, {
    id: eventID,
    emittedAt: now,
    payload: {
      case: 'extension',
      value: anyPack(ArtifactSchema, create(ArtifactSchema, {
        tool: artifact,
        kind: 'import',
        target: file.name,
        data: new Uint8Array(await file.arrayBuffer()),
        mediaType: file.type || 'application/octet-stream',
        timestamp: now,
        resultId: newID(),
      })),
    },
    extensions: [anyPack(RefSchema, create(RefSchema, { operationId, correlation: Correlation.EXPLICIT }))],
  })
  return syncCSTXArtifacts([event])
}

export function syncCSTXArtifacts(artifacts: AOPEvent[] = []): Promise<number> {
  if (syncing) {
    return artifacts.length === 0
      ? syncing
      : syncing.then(() => syncCSTXArtifacts(artifacts))
  }
  syncing = syncArchive(artifacts).finally(() => {
    syncing = undefined
  })
  return syncing
}

// scanId may name a scan call or an operation. Only explicit references bind observations.
export async function listSCONodes(opts?: { type?: string; scanId?: string; limit?: number; afterCursor?: string }): Promise<{ items: SCONode[]; total: number; nextCursor: string }> {
  const runtime = await cstxRuntime()
  const db = await database(runtime.abiVersion)
  const observations = await getAll<Observation>(db, observationsStore)
  const operations = await getAll<ObservedOperation>(db, operationsStore)
  const selected = operationIDs(operations, opts?.scanId)
  let nodes: CanonicalSCONode[]
  if (opts?.scanId) {
    // Merge only the selected execution's own observations, never the global graph.
    const isolated = await CSTXArtifactNormalizer.create()
    try {
      const own = observations.filter((o) => selected.has(o.operation_id)).map((o) => o.node)
      isolated.hydrate(own)
      nodes = isolated.nodes([...new Set(own.map((node) => node.cstx_id))])
    } finally { isolated.close() }
  } else { nodes = await getAll<CanonicalSCONode>(db, nodesStore) }
  if (opts?.type) nodes = nodes.filter((node) => node.cstx_type === opts.type)
  const evidence = (await getAll<ResultEvidence>(db, evidenceStore)).filter((e) => !opts?.scanId || selected.has(e.operation_id))
  const byNode = new Map<string, ResultEvidence[]>()
  for (const item of evidence) for (const id of item.node_ids) byNode.set(id, [...(byNode.get(id) || []), item])
  nodes.sort((a, b) => a.cstx_id.localeCompare(b.cstx_id))
  const offset = opts?.afterCursor ? nodes.findIndex((node) => node.cstx_id === opts.afterCursor) + 1 : 0
  const page = opts?.limit ? nodes.slice(offset, offset + opts.limit) : nodes.slice(offset)
  return {
    items: page.map((node) => ({ ...node, _evidence: byNode.get(node.cstx_id) || [] })) as unknown as SCONode[],
    total: nodes.length,
    nextCursor: offset + page.length < nodes.length ? page[page.length - 1]?.cstx_id || '' : '',
  }
}

function operationIDs(operations: ObservedOperation[], id?: string): Set<string> {
  if (!id) return new Set(operations.map((op) => op.id))
  const ids = new Set([id])
  // Call identity selects all child command operations, without replacing their identities.
  for (const op of operations) if (op.call_id === id) ids.add(op.id)
  for (let previous = -1; previous !== ids.size;) {
    previous = ids.size
    for (const op of operations) if (op.parent_id && ids.has(op.parent_id)) ids.add(op.id)
  }
  return ids
}

export async function listCSTXOperations(): Promise<ObservedOperation[]> {
  const runtime = await cstxRuntime()
  return (await getAll<ObservedOperation>(await database(runtime.abiVersion), operationsStore)).sort((a, b) => b.timestamp.localeCompare(a.timestamp))
}

export async function cstxFailures(scanId?: string): Promise<ParseFailure[]> {
  const runtime = await cstxRuntime()
  const db = await database(runtime.abiVersion)
  const selected = operationIDs(await getAll<ObservedOperation>(db, operationsStore), scanId)
  return (await getAll<ParseFailure>(db, failuresStore)).filter((failure) => !scanId || selected.has(failure.operation_id))
}

export function retryCSTXFailures(): Promise<void> {
  if (syncing) return syncing.then(() => retryCSTXFailures())
  syncing = (async () => {
    const runtime = await cstxRuntime()
    try {
      const db = await database(runtime.abiVersion)
      for (const failure of await getAll<ParseFailure>(db, failuresStore)) await consumeEvent(db, runtime, failure.event)
      return 0
    } catch (error) { runtime.close(); runtimePromise = undefined; throw error }
    finally { window.dispatchEvent(new Event(changedEvent)) }
  })().finally(() => { syncing = undefined })
  return syncing.then(() => undefined)
}

export function subscribeCSTXChanges(listener: () => void): () => void {
  window.addEventListener(changedEvent, listener)
  return () => window.removeEventListener(changedEvent, listener)
}

async function syncArchive(upload: AOPEvent[]): Promise<number> {
  const runtime = await cstxRuntime()
  const db = await database(runtime.abiVersion)
  let cursor = await getMeta(db, cursorKey) || '0'
  let stored = 0
  let appended = upload
  try {
    for (;;) {
      const deliveries = await syncArtifactEvents(cursor, appended)
      appended = []
      if (!deliveries.length) break
      for (const delivery of deliveries) {
        if (!delivery.cursor || !delivery.event) throw new Error('Invalid artifact archive delivery')
        stored += await consumeEvent(db, runtime, delivery.event, delivery.cursor)
        cursor = delivery.cursor
      }
    }
  } catch (error) {
    // Discard in-memory changes when persistence failed; the cursor still permits replay.
    runtime.close()
    runtimePromise = undefined
    throw error
  } finally { window.dispatchEvent(new Event(changedEvent)) }
  return stored
}

function operationRef(event: AOPEvent) {
  for (const extension of event.extensions) {
    const ref = anyUnpack(extension, RefSchema)
    if (ref && ref.correlation !== Correlation.UNATTRIBUTED && ref.operationId) return ref
  }
  return undefined
}

async function consumeEvent(db: IDBDatabase, runtime: CSTXArtifactNormalizer, event: AOPEvent, cursor?: string): Promise<number> {
  const ref = operationRef(event)
  const operationID = ref?.operationId || ''
  const artifact = event.payload.case === 'extension' ? anyUnpack(event.payload.value, ArtifactSchema) : undefined
  const loot = event.payload.case === 'extension' ? anyUnpack(event.payload.value, LootSchema) : undefined
  let nodes: CanonicalSCONode[] = []
  let observed: CanonicalSCONode[] = []
  let failure = ''
  if (artifact) {
    // Unattributed records remain independent; never group them under a session or latest call.
    const key = operationID || `unattributed:${event.id}`
    const existing = await requestValue<Observation[]>(db.transaction(observationsStore).objectStore(observationsStore).index('operation_id').getAll(key))
    const isolated = await CSTXArtifactNormalizer.create()
    try {
      isolated.hydrate(existing.map((o) => o.node))
      observed = isolated.normalize({ artifact: artifact.tool, data: artifact.data }).nodes
      nodes = runtime.normalize({ artifact: artifact.tool, data: artifact.data }).nodes
    } catch (error) { failure = String(error) }
    finally { isolated.close() }
  } else if (!loot) { failure = 'Archive entry is not an Artifact or Loot' }

  // Parsing failures and the original event commit together with the cursor.
  // Storage failures escape this function and MUST NOT advance it.
  const transaction = db.transaction([nodesStore, observationsStore, evidenceStore, failuresStore, operationsStore, metaStore], 'readwrite')
  const done = transactionDone(transaction)
  try {
    const key = operationID || `unattributed:${event.id}`
    if (operationID) transaction.objectStore(operationsStore).put({
      id: operationID, parent_id: ref?.parentOperationId || '', call_id: ref?.callId || '',
      session_id: event.sessionId, target: artifact?.target || loot?.target || '', tool: artifact?.tool || loot?.tool || '',
      timestamp: event.emittedAt ? new Date(Number(event.emittedAt.seconds) * 1000).toISOString() : '',
    } satisfies ObservedOperation)
    if (failure) {
      transaction.objectStore(failuresStore).put({ id: event.id, event, error: failure, operation_id: key } satisfies ParseFailure)
    } else {
      transaction.objectStore(failuresStore).delete(event.id)
      for (const node of nodes) transaction.objectStore(nodesStore).put(node)
      for (const node of observed) transaction.objectStore(observationsStore).put({ id: `${key}\0${node.cstx_id}`, operation_id: key, cstx_id: node.cstx_id, node } satisfies Observation)
      const resultID = artifact?.resultId || loot?.resultId
      if (resultID) {
        const store = transaction.objectStore(evidenceStore)
        const id = `${key}\0${resultID}`
        const request = store.get(id)
        request.onsuccess = () => {
          const prior = request.result as ResultEvidence | undefined
          store.put({
            id, operation_id: key, result_id: resultID,
            node_ids: artifact ? observed.map((node) => node.cstx_id) : prior?.node_ids || [],
            source: artifact?.tool || loot?.tool || prior?.source || '',
            status: loot?.verificationStatus ?? prior?.status ?? '',
            description: loot?.description ?? prior?.description ?? '',
          } satisfies ResultEvidence)
        }
      }
    }
    if (cursor) transaction.objectStore(metaStore).put({ key: cursorKey, value: cursor } satisfies Meta)
    await done
  } catch (error) {
    try { transaction.abort() } catch { /* already completed or aborted */ }
    await done.catch(() => undefined)
    throw error
  }
  return observed.length
}

async function getMeta(db: IDBDatabase, key: string): Promise<string | undefined> {
  const value = await requestValue<Meta | undefined>(db.transaction(metaStore).objectStore(metaStore).get(key))
  return value?.value
}

function getAll<T>(db: IDBDatabase, store: string): Promise<T[]> {
  return requestValue<T[]>(db.transaction(store).objectStore(store).getAll())
}

function requestValue<T>(request: IDBRequest<T>): Promise<T> {
  return new Promise((resolve, reject) => {
    request.onsuccess = () => resolve(request.result)
    request.onerror = () => reject(request.error ?? new Error('IndexedDB request failed'))
  })
}

function transactionDone(transaction: IDBTransaction): Promise<void> {
  return new Promise((resolve, reject) => {
    transaction.oncomplete = () => resolve()
    transaction.onerror = () => reject(transaction.error ?? new Error('IndexedDB transaction failed'))
    transaction.onabort = () => reject(transaction.error ?? new Error('IndexedDB transaction aborted'))
  })
}

function newID(): string {
  if (typeof crypto.randomUUID === 'function') return crypto.randomUUID()
  const bytes = crypto.getRandomValues(new Uint8Array(16))
  return Array.from(bytes, (value) => value.toString(16).padStart(2, '0')).join('')
}

// Compare observed attributes. Collection bookkeeping is not an asset change.
export function compareSCONodes(base: SCONode[], head: SCONode[]): { node: SCONode; before?: SCONode; change: 'added' | 'missing' | 'changed' }[] {
  const previous = new Map(base.map((node) => [node.cstx_id, node]))
  const result: { node: SCONode; before?: SCONode; change: 'added' | 'missing' | 'changed' }[] = []
  for (const node of head) {
    const before = previous.get(node.cstx_id)
    if (!before) result.push({ node, change: 'added' })
    else if (JSON.stringify(observedAttributes(before)) !== JSON.stringify(observedAttributes(node))) result.push({ node, before, change: 'changed' })
    previous.delete(node.cstx_id)
  }
  for (const node of previous.values()) result.push({ node, change: 'missing' })
  return result
}
function observedAttributes(value: unknown): unknown {
  if (Array.isArray(value)) return value.map(observedAttributes).sort((a, b) => JSON.stringify(a).localeCompare(JSON.stringify(b)))
  if (!value || typeof value !== 'object') return value
  const metadata = new Set(['_evidence', 'created_at', 'updated_at', 'first_seen', 'last_seen', 'timestamp', 'operation_id', 'call_id', 'scan_id'])
  return Object.fromEntries(Object.entries(value).filter(([key]) => !metadata.has(key)).sort(([a], [b]) => a.localeCompare(b)).map(([key, item]) => [key, observedAttributes(item)]))
}
