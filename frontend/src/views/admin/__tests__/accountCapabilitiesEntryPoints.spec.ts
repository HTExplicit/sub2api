import { describe, expect, it } from 'vitest'
import { knownAccountFolderIDs, publicModelManagerLocation } from '../accountCapabilitiesEntryPoints'

describe('public model manager entry context', () => {
  it('keeps the existing URL and defaults to the beginner overview without imposing a scope', () => {
    expect(publicModelManagerLocation()).toEqual({
      path: '/admin/account-capabilities', query: { tab: 'overview' }
    })
  })

  it('carries exact object IDs and model versions without duplicate or invalid IDs', () => {
    expect(publicModelManagerLocation({ accountIDs: [9, 1, 9, 0, NaN], folderIDs: [8, 7], groupIDs: [42], model: ' fable-5.1 ' }).query).toEqual({
      tab: 'overview', account_ids: '1,9', folder_ids: '7,8', group_ids: '42', model: 'fable-5.1'
    })
  })

  it('uses known selected-account folders even when they cross the current folder', () => {
    expect(knownAccountFolderIDs([1, 9], [
      { id: 1, management_folder: { id: 7 } },
      { id: 9, management_folder: { id: 8 } }
    ])).toEqual([7, 8])
  })

  it('leaves folder resolution to the server for off-page or uncategorized accounts', () => {
    expect(knownAccountFolderIDs([1, 9], [{ id: 1, management_folder: { id: 7 } }])).toEqual([])
    expect(knownAccountFolderIDs([1, 9], [
      { id: 1, management_folder: { id: 7 } }, { id: 9, management_folder: null }
    ])).toEqual([])
  })
})
