import { afterEach, beforeEach, expect, it, vi } from 'vitest'
import { IDBFactory, IDBKeyRange } from 'fake-indexeddb'
import { listImageStudioHistory, resetImageStudioHistoryDatabaseForTest } from './history'

beforeEach(() => { resetImageStudioHistoryDatabaseForTest(); vi.stubGlobal('indexedDB', new IDBFactory()); vi.stubGlobal('IDBKeyRange', IDBKeyRange) })
afterEach(() => { resetImageStudioHistoryDatabaseForTest(); vi.unstubAllGlobals() })

it('retains unowned historical records without assigning them to the current user', async () => {
  await new Promise<void>((resolve, reject) => {
    const request = indexedDB.open('sub2api-image-studio', 1)
    request.onupgradeneeded = () => request.result.createObjectStore('runs', { keyPath: 'id' })
    request.onerror = () => reject(request.error)
    request.onsuccess = () => {
      const transaction = request.result.transaction('runs', 'readwrite')
      transaction.objectStore('runs').put({ id: 'legacy', prompt: 'unowned retained data' })
      transaction.oncomplete = () => { request.result.close(); resolve() }
    }
  })
  expect(await listImageStudioHistory('user:7')).toEqual([])
  const retained = await new Promise<unknown>((resolve, reject) => {
    const request = indexedDB.open('sub2api-image-studio', 2)
    request.onerror = () => reject(request.error)
    request.onsuccess = () => {
      const read = request.result.transaction('retained-unowned-runs', 'readonly').objectStore('retained-unowned-runs').get('legacy')
      read.onsuccess = () => { request.result.close(); resolve(read.result) }
    }
  })
  expect(retained).toEqual({ id: 'legacy', prompt: 'unowned retained data' })
})
