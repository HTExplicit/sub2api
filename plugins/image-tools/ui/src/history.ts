import { resource } from '@sub2api/plugin-ui'
import type { ImageStudioHistoryRecord } from '@sub2api/plugin-ui/media'
export type { ImageStudioMode, ImageStudioHistoryRecord } from '@sub2api/plugin-ui/media'

// Owner arguments preserve the page's late-result checks. The host derives the
// actual storage owner from its authenticated actor, never from these inputs.
export const listImageStudioHistory = (_owner: string) => resource<ImageStudioHistoryRecord[]>('image.history.list')
export const saveImageStudioHistory = (_owner: string, record: ImageStudioHistoryRecord) => resource<boolean>('image.history.save', { local_data: record })
export const deleteImageStudioHistory = (_owner: string, id: string) => resource<boolean>('image.history.delete', { local_data: id })
export const clearImageStudioHistory = (_owner: string) => resource<boolean>('image.history.clear')
