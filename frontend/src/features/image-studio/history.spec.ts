import { afterAll, beforeAll, describe, expect, it, vi } from 'vitest'
import { IDBKeyRange, indexedDB } from 'fake-indexeddb'
import {
  clearImageStudioHistory,
  deleteImageStudioHistory,
  listImageStudioHistory,
  resetImageStudioHistoryDatabaseForTest,
  saveImageStudioHistory,
  type ImageStudioHistoryRecord,
} from './history'

describe('Image Studio IndexedDB history', () => {
  const ownerOne = 'user:101'
  const ownerTwo = 'user:202'
  const previousIndexedDB = globalThis.indexedDB
  const previousKeyRange = globalThis.IDBKeyRange

  beforeAll(() => {
    Object.defineProperty(globalThis, 'indexedDB', { configurable: true, value: indexedDB })
    Object.defineProperty(globalThis, 'IDBKeyRange', { configurable: true, value: IDBKeyRange })
    resetImageStudioHistoryDatabaseForTest()
  })

  afterAll(() => {
    Object.defineProperty(globalThis, 'indexedDB', { configurable: true, value: previousIndexedDB })
    Object.defineProperty(globalThis, 'IDBKeyRange', { configurable: true, value: previousKeyRange })
    resetImageStudioHistoryDatabaseForTest()
  })

  it('stores generated and edit source images locally, then deletes and clears them', async () => {
    const makeRecord = (id: string, createdAt: number): ImageStudioHistoryRecord => ({
      id,
      createdAt,
      mode: 'edit',
      model: 'gemini-3-pro-image',
      prompt: `prompt-${id}`,
      size: '1024x1024',
      quality: 'low',
      count: 1,
      sourceImage: new Blob(['source'], { type: 'image/png' }),
      maskImage: new Blob(['mask'], { type: 'image/png' }),
      images: [{ id: `image-${id}`, blob: new Blob(['output'], { type: 'image/png' }), mimeType: 'image/png' }],
    })

    expect(await saveImageStudioHistory(ownerOne, makeRecord('old', 1))).toBe(true)
    expect(await saveImageStudioHistory(ownerOne, makeRecord('new', 2))).toBe(true)
    let records = await listImageStudioHistory(ownerOne)
    expect(records.map(record => record.id)).toEqual(['new', 'old'])
    expect(records[0].sourceImage).toBeTruthy()
    expect(records[0].maskImage).toBeTruthy()
    expect(records[0].images[0].blob).toBeTruthy()

    expect(await deleteImageStudioHistory(ownerOne, 'new')).toBe(true)
    records = await listImageStudioHistory(ownerOne)
    expect(records.map(record => record.id)).toEqual(['old'])

    expect(await clearImageStudioHistory(ownerOne)).toBe(true)
    expect(await listImageStudioHistory(ownerOne)).toEqual([])
  })

  it('never returns or clears records owned by another authenticated user', async () => {
    const record = (id: string, prompt: string): ImageStudioHistoryRecord => ({
      id,
      createdAt: 1,
      mode: 'generate',
      model: 'gpt-image-2',
      prompt,
      size: '1024x1024',
      quality: 'low',
      count: 1,
      images: [],
    })

    expect(await saveImageStudioHistory(ownerOne, record('shared-id', 'owner one'))).toBe(true)
    expect(await saveImageStudioHistory(ownerTwo, record('shared-id', 'owner two'))).toBe(true)
    expect((await listImageStudioHistory(ownerOne)).map(item => item.prompt)).toEqual(['owner one'])
    expect((await listImageStudioHistory(ownerTwo)).map(item => item.prompt)).toEqual(['owner two'])

    expect(await deleteImageStudioHistory(ownerOne, 'shared-id')).toBe(true)
    expect(await listImageStudioHistory(ownerOne)).toEqual([])
    expect((await listImageStudioHistory(ownerTwo)).map(item => item.prompt)).toEqual(['owner two'])

    expect(await clearImageStudioHistory(ownerOne)).toBe(true)
    expect((await listImageStudioHistory(ownerTwo)).map(item => item.prompt)).toEqual(['owner two'])
    expect(await clearImageStudioHistory(ownerTwo)).toBe(true)
  })

  it('reports failure when a write request succeeds but its transaction aborts', async () => {
    const normalIndexedDB = globalThis.indexedDB
    const openRequest = {} as IDBOpenDBRequest
    const writeRequest = {} as IDBRequest<IDBValidKey>
    const transaction = {
      objectStore: () => ({
        put: () => {
          setTimeout(() => {
            Object.defineProperty(writeRequest, 'result', { configurable: true, value: 'run-aborted' })
            writeRequest.onsuccess?.(new Event('success'))
            setTimeout(() => transaction.onabort?.(new Event('abort')), 0)
          }, 0)
          return writeRequest
        },
      }),
      oncomplete: null,
      onabort: null,
      onerror: null,
    } as unknown as IDBTransaction
    const database = {
      objectStoreNames: { contains: () => true },
      transaction: () => transaction,
    } as unknown as IDBDatabase
    const abortingIndexedDB = {
      open: () => {
        setTimeout(() => {
          Object.defineProperty(openRequest, 'result', { configurable: true, value: database })
          openRequest.onsuccess?.(new Event('success'))
        }, 0)
        return openRequest
      },
    } as unknown as IDBFactory

    Object.defineProperty(globalThis, 'indexedDB', { configurable: true, value: abortingIndexedDB })
    resetImageStudioHistoryDatabaseForTest()
    try {
      const record: ImageStudioHistoryRecord = {
        id: 'run-aborted',
        createdAt: 1,
        mode: 'generate',
        model: 'gpt-image-2',
        prompt: 'must commit',
        size: '1024x1024',
        quality: 'low',
        count: 1,
        images: [],
      }

      expect(await saveImageStudioHistory(ownerOne, record)).toBe(false)
    } finally {
      Object.defineProperty(globalThis, 'indexedDB', { configurable: true, value: normalIndexedDB })
      resetImageStudioHistoryDatabaseForTest()
    }
  })

  it('retries opening IndexedDB after a blocked version upgrade', async () => {
    const normalIndexedDB = globalThis.indexedDB
    const blockedRequest = {} as IDBOpenDBRequest
    const retryRequest = {} as IDBOpenDBRequest
    const putRequest = {} as IDBRequest<IDBValidKey>
    const lateClose = vi.fn()
    const transaction = {
      objectStore: () => ({
        put: () => {
          setTimeout(() => {
            Object.defineProperty(putRequest, 'result', { configurable: true, value: ['user:101', 'retry'] })
            putRequest.onsuccess?.(new Event('success'))
            transaction.oncomplete?.(new Event('complete'))
          }, 0)
          return putRequest
        },
      }),
      oncomplete: null,
      onabort: null,
      onerror: null,
    } as unknown as IDBTransaction
    const retryDatabase = {
      objectStoreNames: { contains: () => true },
      transaction: () => transaction,
      close: vi.fn(),
    } as unknown as IDBDatabase
    const lateDatabase = {
      objectStoreNames: { contains: () => true },
      close: lateClose,
    } as unknown as IDBDatabase
    let attempts = 0
    const retryingIndexedDB = {
      open: () => {
        attempts += 1
        const request = attempts === 1 ? blockedRequest : retryRequest
        setTimeout(() => {
          if (attempts === 1) {
            request.onblocked?.(new Event('blocked'))
          } else {
            Object.defineProperty(request, 'result', { configurable: true, value: retryDatabase })
            request.onsuccess?.(new Event('success'))
          }
        }, 0)
        return request
      },
    } as unknown as IDBFactory

    Object.defineProperty(globalThis, 'indexedDB', { configurable: true, value: retryingIndexedDB })
    resetImageStudioHistoryDatabaseForTest()
    const record: ImageStudioHistoryRecord = {
      id: 'retry',
      createdAt: 1,
      mode: 'generate',
      model: 'gpt-image-2',
      prompt: 'retry blocked open',
      size: '1024x1024',
      quality: 'low',
      count: 1,
      images: [],
    }
    try {
      expect(await saveImageStudioHistory(ownerOne, record)).toBe(false)
      expect(await saveImageStudioHistory(ownerOne, record)).toBe(true)
      expect(attempts).toBe(2)

      Object.defineProperty(blockedRequest, 'result', { configurable: true, value: lateDatabase })
      blockedRequest.onsuccess?.(new Event('success'))
      expect(lateClose).toHaveBeenCalledTimes(1)
    } finally {
      Object.defineProperty(globalThis, 'indexedDB', { configurable: true, value: normalIndexedDB })
      resetImageStudioHistoryDatabaseForTest()
    }
  })
})
