import { clearImageStudioHistory, deleteImageStudioHistory, listImageStudioHistory, saveImageStudioHistory } from '@/features/image-studio/history'
import type { ImageStudioHistoryRecord } from '@/types/imageStudio'

function historyRecord(value: unknown): ImageStudioHistoryRecord {
  if (!value || typeof value !== 'object') throw new Error('Invalid local image record')
  const record = value as ImageStudioHistoryRecord
  if (typeof record.id !== 'string' || !record.id || record.id.length > 160 || !Number.isFinite(record.createdAt) ||
      typeof record.prompt !== 'string' || record.prompt.length > 32000 || typeof record.model !== 'string' ||
      !['generate', 'edit'].includes(record.mode) || !Array.isArray(record.images) || record.images.length > 4) throw new Error('Invalid local image record')
  for (const blob of [record.sourceImage, record.maskImage, ...record.images.map(image => image.blob)]) {
    if (blob !== undefined && (!(blob instanceof Blob) || blob.size > 20 * 1024 * 1024)) throw new Error('Invalid local image data')
  }
  return record
}

export async function callLocalResource(name: string, actorID: number, input: unknown) {
  if (!Number.isSafeInteger(actorID) || actorID <= 0) throw new Error('Authenticated user required')
  const owner = `user:${actorID}`
  switch (name) {
    case 'image.history.list': return listImageStudioHistory(owner)
    case 'image.history.save': return saveImageStudioHistory(owner, historyRecord(input))
    case 'image.history.delete': {
      if (typeof input !== 'string' || input.length > 160) throw new Error('Invalid local record ID')
      return deleteImageStudioHistory(owner, input)
    }
    case 'image.history.clear': return clearImageStudioHistory(owner)
    default: throw new Error('Unknown local resource')
  }
}
