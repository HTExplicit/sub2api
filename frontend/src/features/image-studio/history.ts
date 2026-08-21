export type ImageStudioMode = 'generate' | 'edit'

export interface ImageStudioHistoryImage {
  id: string
  blob: Blob
  mimeType: string
  revisedPrompt?: string
}

export interface ImageStudioHistoryRecord {
  id: string
  jobId?: number
  status?: import('@/api/imageStudio').ImageStudioJobStatus
  errorMessage?: string
  createdAt: number
  mode: ImageStudioMode
  model: string
  prompt: string
  size: string
  quality: string
  count: number
  sourceImage?: Blob
  sourceImageName?: string
  maskImage?: Blob
  maskImageName?: string
  images: ImageStudioHistoryImage[]
}

const DB_NAME = 'sub2api-image-studio'
const DB_VERSION = 2
const STORE_NAME = 'runs'
const OWNER_INDEX = 'ownerKey'

type StoredImageStudioHistoryRecord = ImageStudioHistoryRecord & { ownerKey: string }

let databasePromise: Promise<IDBDatabase | null> | null = null

function openDatabase(): Promise<IDBDatabase | null> {
  if (databasePromise) return databasePromise
  if (typeof indexedDB === 'undefined') return Promise.resolve(null)

  const opening = new Promise<IDBDatabase | null>((resolve) => {
    const request = indexedDB.open(DB_NAME, DB_VERSION)
    let settled = false
    const finish = (database: IDBDatabase | null) => {
      if (settled) {
        database?.close()
        return
      }
      settled = true
      if (!database && databasePromise === opening) databasePromise = null
      resolve(database)
    }
    request.onupgradeneeded = (event) => {
      const database = request.result
      // Version 1 records had no owner and cannot be safely attributed. Drop
      // them instead of exposing one signed-in user's images to another.
      if (event.oldVersion < 2 && database.objectStoreNames.contains(STORE_NAME)) {
        database.deleteObjectStore(STORE_NAME)
      }
      if (!database.objectStoreNames.contains(STORE_NAME)) {
        const store = database.createObjectStore(STORE_NAME, { keyPath: ['ownerKey', 'id'] })
        store.createIndex('createdAt', 'createdAt')
        store.createIndex(OWNER_INDEX, OWNER_INDEX)
      }
    }
    request.onsuccess = () => {
      request.result.onversionchange = () => request.result.close()
      finish(request.result)
    }
    request.onerror = () => finish(null)
    // A stale tab can temporarily block the v2 privacy migration. Let the
    // current operation fail without permanently caching that state; if the
    // original open later succeeds, finish() closes its superseded handle.
    request.onblocked = () => finish(null)
  })
  databasePromise = opening
  return opening
}

interface RequestResult<T> {
  ok: boolean
  value?: T
}

function runRequest<T>(
  mode: IDBTransactionMode,
  operation: (store: IDBObjectStore) => IDBRequest<T>,
): Promise<RequestResult<T>> {
  return openDatabase().then(database => new Promise((resolve) => {
    if (!database) {
      resolve({ ok: false })
      return
    }

    let transaction: IDBTransaction
    let request: IDBRequest<T>
    try {
      transaction = database.transaction(STORE_NAME, mode)
      request = operation(transaction.objectStore(STORE_NAME))
    } catch {
      resolve({ ok: false })
      return
    }

    let requestSucceeded = false
    let requestFailed = false
    let requestResult: T | undefined
    let settled = false
    const finish = (result: RequestResult<T>) => {
      if (settled) return
      settled = true
      resolve(result)
    }

    request.onsuccess = () => {
      requestSucceeded = true
      requestResult = request.result
    }
    request.onerror = () => {
      requestFailed = true
      finish({ ok: false })
    }
    transaction.oncomplete = () => {
      finish(requestSucceeded && !requestFailed
        ? { ok: true, value: requestResult }
        : { ok: false })
    }
    transaction.onabort = () => finish({ ok: false })
    transaction.onerror = () => finish({ ok: false })
  }))
}

function normalizedOwnerKey(ownerKey: string): string {
  return typeof ownerKey === 'string' ? ownerKey.trim() : ''
}

export async function listImageStudioHistory(ownerKey: string): Promise<ImageStudioHistoryRecord[]> {
  const owner = normalizedOwnerKey(ownerKey)
  if (!owner) return []
  const result = await runRequest<StoredImageStudioHistoryRecord[]>(
    'readonly',
    store => store.index(OWNER_INDEX).getAll(owner),
  )
  return (result.ok && Array.isArray(result.value) ? result.value : [])
    .map(({ ownerKey: _ownerKey, ...record }) => record)
    .sort((left, right) => right.createdAt - left.createdAt)
}

export async function saveImageStudioHistory(
  ownerKey: string,
  record: ImageStudioHistoryRecord,
): Promise<boolean> {
  const owner = normalizedOwnerKey(ownerKey)
  if (!owner) return false
  return (await runRequest<IDBValidKey>('readwrite', store => store.put({ ...record, ownerKey: owner }))).ok
}

export async function deleteImageStudioHistory(ownerKey: string, id: string): Promise<boolean> {
  const owner = normalizedOwnerKey(ownerKey)
  if (!owner || !id) return false
  return (await runRequest<undefined>('readwrite', store => store.delete([owner, id]))).ok
}

export async function clearImageStudioHistory(ownerKey: string): Promise<boolean> {
  const owner = normalizedOwnerKey(ownerKey)
  if (!owner) return false
  const database = await openDatabase()
  if (!database) return false

  return new Promise((resolve) => {
    let transaction: IDBTransaction
    let request: IDBRequest<IDBCursor | null>
    try {
      transaction = database.transaction(STORE_NAME, 'readwrite')
      const store = transaction.objectStore(STORE_NAME)
      request = store.index(OWNER_INDEX).openKeyCursor(IDBKeyRange.only(owner))
      request.onsuccess = () => {
        const cursor = request.result
        if (!cursor) return
        store.delete(cursor.primaryKey)
        cursor.continue()
      }
    } catch {
      resolve(false)
      return
    }

    let settled = false
    const finish = (value: boolean) => {
      if (settled) return
      settled = true
      resolve(value)
    }
    request.onerror = () => finish(false)
    transaction.oncomplete = () => finish(true)
    transaction.onabort = () => finish(false)
    transaction.onerror = () => finish(false)
  })
}

export function resetImageStudioHistoryDatabaseForTest(): void {
  if (databasePromise) {
    void databasePromise.then((database) => {
      if (database && typeof database.close === 'function') database.close()
    })
  }
  databasePromise = null
}
