import { create } from '@bufbuild/protobuf'
import { anyPack, anyUnpack, timestampNow } from '@bufbuild/protobuf/wkt'
import {
  ArtifactSchema,
  EventSchema,
  RefSchema,
  type Event as AOPEvent,
} from '@cyber/aop'
import {
  CSTX_ABI_VERSION,
  CSTXArtifactNormalizer,
  type CanonicalSCONode,
} from '@cyber/cstx'
import type { SCONode } from '@cyber/cstx-easm'
import { syncArtifactEvents } from '../api'

const databaseName = 'cyber-cstx'
const databaseVersion = 1
const nodesStore = 'nodes'
const observationsStore = 'observations'
const metaStore = 'meta'
const abiKey = 'abi'
const cursorKey = 'archive_cursor'
const changedEvent = 'cyber:cstx-changed'

type Observation = {
  id: string
  operation_id: string
  cstx_id: string
}

type Meta = {
  key: string
  value: string
}

let databasePromise: Promise<IDBDatabase> | undefined
let runtimePromise: Promise<CSTXArtifactNormalizer> | undefined
let syncing: Promise<number> | undefined

async function database(): Promise<IDBDatabase> {
  if (!databasePromise) {
    databasePromise = new Promise<IDBDatabase>((resolve, reject) => {
      const request = indexedDB.open(databaseName, databaseVersion)
      request.onupgradeneeded = () => {
        const db = request.result
        for (const name of Array.from(db.objectStoreNames)) db.deleteObjectStore(name)
        db.createObjectStore(nodesStore, { keyPath: 'cstx_id' })
        const observations = db.createObjectStore(observationsStore, { keyPath: 'id' })
        observations.createIndex('operation_id', 'operation_id')
        db.createObjectStore(metaStore, { keyPath: 'key' })
      }
      request.onsuccess = () => resolve(request.result)
      request.onerror = () => reject(request.error ?? new Error('Failed to open CSTX database'))
      request.onblocked = () => reject(new Error('CSTX database upgrade is blocked'))
    }).then(async (db) => {
      const abi = await getMeta(db, abiKey)
      if (abi !== CSTX_ABI_VERSION) {
        const transaction = db.transaction([nodesStore, observationsStore, metaStore], 'readwrite')
        transaction.objectStore(nodesStore).clear()
        transaction.objectStore(observationsStore).clear()
        transaction.objectStore(metaStore).clear()
        transaction.objectStore(metaStore).put({ key: abiKey, value: CSTX_ABI_VERSION } satisfies Meta)
        transaction.objectStore(metaStore).put({ key: cursorKey, value: '0' } satisfies Meta)
        await transactionDone(transaction)
      }
      return db
    }).catch((error) => {
      databasePromise = undefined
      throw error
    })
  }
  return databasePromise!
}

async function cstxRuntime(): Promise<CSTXArtifactNormalizer> {
  if (!runtimePromise) {
    runtimePromise = Promise.all([CSTXArtifactNormalizer.create(), database()]).then(async ([runtime, db]) => {
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
  operationId = 'import',
): Promise<{ status: string; nodes: number; artifact: string; duplicates: number }> {
  const now = timestampNow()
  const event = create(EventSchema, {
    id: newID(),
    emittedAt: now,
    emitter: 'web',
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
    extensions: [anyPack(RefSchema, create(RefSchema, { callId: operationId }))],
  })
  const nodes = await syncCSTXArtifacts([event])
  return { status: 'ok', nodes, artifact, duplicates: 0 }
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

export async function listSCONodes(opts?: { type?: string; scanId?: string; limit?: number }): Promise<SCONode[]> {
  const db = await database()
  let nodes = await getAll<CanonicalSCONode>(db, nodesStore)
  if (opts?.scanId) {
    const observations = await getAll<Observation>(db, observationsStore)
    const ids = new Set(observations
      .filter((observation) => observation.operation_id === opts.scanId)
      .map((observation) => observation.cstx_id))
    nodes = nodes.filter((node) => ids.has(node.cstx_id))
  }
  if (opts?.type) nodes = nodes.filter((node) => node.cstx_type === opts.type)
  if (opts?.limit && opts.limit > 0) nodes = nodes.slice(0, opts.limit)
  return nodes as unknown as SCONode[]
}

export function subscribeCSTXChanges(listener: () => void): () => void {
  window.addEventListener(changedEvent, listener)
  return () => window.removeEventListener(changedEvent, listener)
}

async function syncArchive(upload: AOPEvent[]): Promise<number> {
  const [runtime, db] = await Promise.all([cstxRuntime(), database()])
  let cursor = await getMeta(db, cursorKey) || '0'
  let stored = 0
  let appended = upload

  for (;;) {
    const deliveries = await syncArtifactEvents(cursor, appended)
    appended = []
    if (deliveries.length === 0) break
    for (const delivery of deliveries) {
      if (!delivery.cursor) continue
      try {
        const event = delivery.event
        if (!event || event.payload.case !== 'extension') {
          throw new Error('archive entry is not an extension event')
        }
        const artifact = anyUnpack(event.payload.value, ArtifactSchema)
        if (!artifact) throw new Error('archive entry is not an aop.tool.Artifact')
        const operationID = operationIDOf(event)
        const result = runtime.normalizeAOPArtifact({
          artifact: artifact.tool,
          producer: artifact.tool,
          kind: artifact.kind,
          resultId: artifact.resultId,
          data: artifact.data,
        })
        await persistResult(db, delivery.cursor, operationID, result.nodes)
        stored += result.nodes.length
      } catch (error) {
        console.error(`CSTX artifact event ${delivery.event?.id || delivery.cursor} failed`, error)
        await putMeta(db, cursorKey, delivery.cursor)
      }
      cursor = delivery.cursor
    }
  }

  if (stored > 0) window.dispatchEvent(new Event(changedEvent))
  return stored
}

function operationIDOf(event: AOPEvent): string {
  for (const extension of event.extensions) {
    const ref = anyUnpack(extension, RefSchema)
    if (ref) return ref.callId || ref.operationId || event.turnId || event.sessionId || 'unscoped'
  }
  return event.turnId || event.sessionId || 'unscoped'
}

async function persistResult(
  db: IDBDatabase,
  cursor: string,
  operationID: string,
  nodes: readonly CanonicalSCONode[],
): Promise<void> {
  const transaction = db.transaction([nodesStore, observationsStore, metaStore], 'readwrite')
  const nodeRecords = transaction.objectStore(nodesStore)
  const observations = transaction.objectStore(observationsStore)
  for (const node of nodes) {
    nodeRecords.put(node)
    observations.put({
      id: `${operationID}\0${node.cstx_id}`,
      operation_id: operationID,
      cstx_id: node.cstx_id,
    } satisfies Observation)
  }
  transaction.objectStore(metaStore).put({ key: cursorKey, value: cursor } satisfies Meta)
  await transactionDone(transaction)
}

async function getMeta(db: IDBDatabase, key: string): Promise<string | undefined> {
  const value = await requestValue<Meta | undefined>(db.transaction(metaStore).objectStore(metaStore).get(key))
  return value?.value
}

async function putMeta(db: IDBDatabase, key: string, value: string): Promise<void> {
  const transaction = db.transaction(metaStore, 'readwrite')
  transaction.objectStore(metaStore).put({ key, value } satisfies Meta)
  await transactionDone(transaction)
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
