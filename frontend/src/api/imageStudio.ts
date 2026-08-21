import { apiClient } from './client'

export type ImageStudioEndpoint = 'images.generations' | 'images.edits' | string
export type ImageStudioMode = 'generate' | 'edit'
export type ImageStudioJobStatus =
  | 'pending'
  | 'preparing'
  | 'running'
  | 'succeeded'
  | 'partially_succeeded'
  | 'failed'
  | 'canceled'
  | 'canceled_with_results'

export interface ImageRequestControls {
  sizes?: string[]
  qualities?: string[]
  max_output_count?: number
  supports_reference_image?: boolean
  supports_mask?: boolean
}

export interface ModelCapability {
  object: 'model_capability' | string
  id: string
  kind: 'text' | 'image' | 'special' | string
  input_modalities: string[]
  output_modalities: string[]
  endpoints: ImageStudioEndpoint[]
  client_surfaces: string[]
  controls?: {
    generation?: ImageRequestControls
    edit?: ImageRequestControls
  }
}

export interface EligibleImageStudioKeyGroup {
  id: number
  name: string
}

export interface EligibleImageStudioAPIKey {
  id: number
  name: string
  group_id: number
  group: EligibleImageStudioKeyGroup
}

export interface EligibleImageStudioKey {
  api_key: EligibleImageStudioAPIKey
  capabilities: ModelCapability[]
}

export interface EligibleImageStudioKeysResponse {
  items: EligibleImageStudioKey[]
}

export interface ImageStudioCounts {
  processed: number
  succeeded: number
  failed: number
  canceled: number
}

export interface ImageStudioJob {
  id: number
  api_key_id: number
  mode: ImageStudioMode
  model: string
  size?: string
  quality?: string
  count: number
  status: ImageStudioJobStatus
  counts: ImageStudioCounts
  error_code?: string
  error_message?: string
  request_expires_at?: string
  created_at?: string
  updated_at?: string
}

export interface ImageStudioItem {
  id: number
  job_id: number
  ordinal: number
  status: 'pending' | 'running' | 'succeeded' | 'failed' | 'canceled'
  error_code?: string
  error_message?: string
}

export interface ImageStudioArtifact {
  id: number
  job_id: number
  item_id?: number
  kind: 'output'
  content_type: string
  byte_size: number
  revised_prompt?: string
  expires_at?: string
  download_url: string
}

export interface ImageStudioJobDetail {
  job: ImageStudioJob
  items: ImageStudioItem[]
  artifacts: ImageStudioArtifact[]
}

export interface CreateImageStudioJobInput {
  apiKeyId: number
  mode: ImageStudioMode
  model: string
  prompt: string
  count: number
  size?: string
  quality?: string
  reference?: File | Blob | null
  referenceName?: string
  mask?: File | Blob | null
  maskName?: string
  signal?: AbortSignal
}

export const MAX_IMAGE_BYTES = 20 * 1024 * 1024

const ALLOWED_IMAGE_MIME_TYPES = ['image/png', 'image/jpeg', 'image/webp'] as const
export type AllowedImageMimeType = (typeof ALLOWED_IMAGE_MIME_TYPES)[number]
const allowedImageMimeTypes = new Set<string>(ALLOWED_IMAGE_MIME_TYPES)
const terminalStatuses = new Set<ImageStudioJobStatus>([
  'succeeded', 'partially_succeeded', 'failed', 'canceled', 'canceled_with_results',
])

function isRecord(value: unknown): value is Record<string, unknown> {
  return typeof value === 'object' && value !== null && !Array.isArray(value)
}

function isPositiveID(value: unknown): value is number {
  return typeof value === 'number' && Number.isSafeInteger(value) && value > 0
}

function unwrapData(value: unknown): unknown {
  if (isRecord(value) && isRecord(value.data) && 'code' in value) return value.data
  return value
}

function parseEligibleImageStudioKey(value: unknown): EligibleImageStudioKey | null {
  if (!isRecord(value) || !isRecord(value.api_key) || !Array.isArray(value.capabilities)) return null
  const apiKey = value.api_key
  const group = apiKey.group
  if (
    !isPositiveID(apiKey.id)
    || typeof apiKey.name !== 'string'
    || !isPositiveID(apiKey.group_id)
    || !isRecord(group)
    || !isPositiveID(group.id)
    || group.id !== apiKey.group_id
    || typeof group.name !== 'string'
  ) return null
  return {
    api_key: {
      id: apiKey.id,
      name: apiKey.name,
      group_id: apiKey.group_id,
      group: { id: group.id, name: group.name },
    },
    capabilities: value.capabilities as ModelCapability[],
  }
}

export async function listEligibleImageStudioKeys(signal?: AbortSignal): Promise<EligibleImageStudioKeysResponse> {
  const response = await apiClient.get<unknown>('/image-studio/eligible-keys', { signal })
  const body = unwrapData(response.data)
  const items = isRecord(body) && Array.isArray(body.items)
    ? body.items.map(parseEligibleImageStudioKey).filter((item): item is EligibleImageStudioKey => item !== null)
    : []
  return { items }
}

export async function createImageStudioJob(input: CreateImageStudioJobInput): Promise<ImageStudioJob> {
  const body = new FormData()
  body.set('api_key_id', String(input.apiKeyId))
  body.set('mode', input.mode)
  body.set('model', input.model)
  body.set('prompt', input.prompt)
  body.set('count', String(input.count))
  if (input.size) body.set('size', input.size)
  if (input.quality) body.set('quality', input.quality)
  if (input.reference) body.set('reference', input.reference, input.referenceName || 'reference.png')
  if (input.mask) body.set('mask', input.mask, input.maskName || 'mask.png')
  const response = await apiClient.post<ImageStudioJob>('/image-studio/jobs', body, { signal: input.signal })
  return unwrapData(response.data) as ImageStudioJob
}

export async function listImageStudioJobs(signal?: AbortSignal): Promise<ImageStudioJob[]> {
  const response = await apiClient.get<unknown>('/image-studio/jobs', { signal })
  const body = unwrapData(response.data)
  return isRecord(body) && Array.isArray(body.items) ? body.items as ImageStudioJob[] : []
}

export async function getImageStudioJob(jobID: number, signal?: AbortSignal): Promise<ImageStudioJobDetail> {
  const response = await apiClient.get<ImageStudioJobDetail>(`/image-studio/jobs/${jobID}`, { signal })
  return unwrapData(response.data) as ImageStudioJobDetail
}

export async function cancelImageStudioJob(jobID: number, signal?: AbortSignal): Promise<ImageStudioJob> {
  const response = await apiClient.post<ImageStudioJob>(`/image-studio/jobs/${jobID}/cancel`, undefined, { signal })
  return unwrapData(response.data) as ImageStudioJob
}

export async function retryImageStudioJob(jobID: number, signal?: AbortSignal): Promise<ImageStudioJob> {
  const response = await apiClient.post<ImageStudioJob>(`/image-studio/jobs/${jobID}/retry`, undefined, { signal })
  return unwrapData(response.data) as ImageStudioJob
}

export async function downloadImageStudioArtifact(artifact: ImageStudioArtifact, signal?: AbortSignal): Promise<Blob> {
  const response = await apiClient.get<Blob>(`/image-studio/jobs/${artifact.job_id}/artifacts/${artifact.id}`, {
    responseType: 'blob',
    signal,
  })
  return response.data
}

export function isImageStudioJobTerminal(status: ImageStudioJobStatus): boolean {
  return terminalStatuses.has(status)
}

export async function waitForImageStudioJob(
  jobID: number,
  signal?: AbortSignal,
  pollMilliseconds = 1000,
  onProgress?: (detail: ImageStudioJobDetail) => void,
): Promise<ImageStudioJobDetail> {
  for (;;) {
    const detail = await getImageStudioJob(jobID, signal)
    onProgress?.(detail)
    if (isImageStudioJobTerminal(detail.job.status)) return detail
    await waitForImageStudioPoll(pollMilliseconds, signal)
  }
}

function waitForImageStudioPoll(milliseconds: number, signal?: AbortSignal): Promise<void> {
  if (signal?.aborted) return Promise.reject(new DOMException('Aborted', 'AbortError'))
  return new Promise((resolve, reject) => {
    const timer = window.setTimeout(() => {
      signal?.removeEventListener('abort', abort)
      resolve()
    }, milliseconds)
    const abort = () => {
      window.clearTimeout(timer)
      reject(new DOMException('Aborted', 'AbortError'))
    }
    signal?.addEventListener('abort', abort, { once: true })
  })
}

function canonicalImageMimeType(value: string): AllowedImageMimeType {
  const normalized = value.trim().toLowerCase()
  if (!allowedImageMimeTypes.has(normalized)) throw new Error('The image API returned an unsupported image format')
  return normalized as AllowedImageMimeType
}

function detectImageMimeType(bytes: Uint8Array): AllowedImageMimeType | null {
  if (bytes.length >= 8
    && bytes[0] === 0x89 && bytes[1] === 0x50 && bytes[2] === 0x4e && bytes[3] === 0x47
    && bytes[4] === 0x0d && bytes[5] === 0x0a && bytes[6] === 0x1a && bytes[7] === 0x0a) return 'image/png'
  if (bytes.length >= 3 && bytes[0] === 0xff && bytes[1] === 0xd8 && bytes[2] === 0xff) return 'image/jpeg'
  if (bytes.length >= 12
    && bytes[0] === 0x52 && bytes[1] === 0x49 && bytes[2] === 0x46 && bytes[3] === 0x46
    && bytes[8] === 0x57 && bytes[9] === 0x45 && bytes[10] === 0x42 && bytes[11] === 0x50) return 'image/webp'
  return null
}

function readBlobArrayBuffer(blob: Blob): Promise<ArrayBuffer> {
  if (typeof blob.arrayBuffer === 'function') return blob.arrayBuffer()
  return new Promise((resolve, reject) => {
    const reader = new FileReader()
    reader.onload = () => reader.result instanceof ArrayBuffer ? resolve(reader.result) : reject(new Error('The image data could not be read'))
    reader.onerror = () => reject(new Error('The image data could not be read'))
    reader.onabort = () => reject(new Error('The image data could not be read'))
    reader.readAsArrayBuffer(blob)
  })
}

async function verifyBrowserCanDecode(blob: Blob): Promise<void> {
  if (typeof globalThis.createImageBitmap === 'function') {
    let bitmap: ImageBitmap | null = null
    try {
      bitmap = await globalThis.createImageBitmap(blob)
      if (bitmap.width < 1 || bitmap.height < 1) throw new Error('invalid dimensions')
    } catch {
      throw new Error('The image data could not be decoded')
    } finally {
      bitmap?.close()
    }
    return
  }
  if (typeof Image === 'undefined' || typeof URL.createObjectURL !== 'function') return
  const image = new Image()
  if (typeof image.decode !== 'function') return
  const url = URL.createObjectURL(blob)
  try {
    image.src = url
    await image.decode()
    if (image.naturalWidth < 1 || image.naturalHeight < 1) throw new Error('invalid dimensions')
  } catch {
    throw new Error('The image data could not be decoded')
  } finally {
    URL.revokeObjectURL(url)
  }
}

export async function validateImageBlob(blob: Blob, declaredMimeType: string | undefined = blob.type): Promise<AllowedImageMimeType> {
  if (blob.size < 1 || blob.size > MAX_IMAGE_BYTES) throw new Error('The image is empty or exceeds the 20 MB limit')
  const declared = declaredMimeType ? canonicalImageMimeType(declaredMimeType) : undefined
  const header = new Uint8Array(await readBlobArrayBuffer(blob.slice(0, 12)))
  const detected = detectImageMimeType(header)
  if (!detected) throw new Error('The image data does not contain a supported image')
  if (declared && declared !== detected) throw new Error('The image MIME type does not match its contents')
  const verifiedBlob = blob.type === detected ? blob : new Blob([blob], { type: detected })
  await verifyBrowserCanDecode(verifiedBlob)
  return detected
}
