import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { IDBFactory, IDBKeyRange } from 'fake-indexeddb'
import { callLocalResource } from '../localResources'
import { resetImageStudioHistoryDatabaseForTest } from '@/features/image-studio/history'

describe('browser-local plugin resources', () => {
  beforeEach(() => { resetImageStudioHistoryDatabaseForTest(); vi.stubGlobal('indexedDB', new IDBFactory()); vi.stubGlobal('IDBKeyRange', IDBKeyRange) })
  afterEach(() => { resetImageStudioHistoryDatabaseForTest(); vi.unstubAllGlobals() })
  it('derives the history owner from the authenticated actor and preserves other users', async () => {
    const record = { id: 'same-id', ownerKey: 'user:8', createdAt: 1, mode: 'generate', model: 'fixture', prompt: 'local only', size: '', quality: '', count: 1, images: [] }
    expect(await callLocalResource('image.history.save', 7, record)).toBe(true)
    expect(await callLocalResource('image.history.list', 8, undefined)).toEqual([])
    expect(await callLocalResource('image.history.clear', 8, undefined)).toBe(true)
    expect(await callLocalResource('image.history.list', 7, undefined)).toHaveLength(1)
    await expect(callLocalResource('other.database.read', 7, undefined)).rejects.toThrow()
    await expect(callLocalResource('image.history.list', 0, undefined)).rejects.toThrow()
  })
})
