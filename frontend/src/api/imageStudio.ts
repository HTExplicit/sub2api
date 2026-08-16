import { apiClient, buildGatewayUrl } from './client'

export type ImageStudioEndpoint = 'images.generations' | 'images.edits' | string

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
  max_input_tokens?: number
  max_output_tokens?: number
  pricing_source?: string
  controls?: {
    generation?: ImageRequestControls
    edit?: ImageRequestControls
  }
}

export interface ModelCapabilitiesResponse {
  object: 'list' | string
  catalog_version: string
  data: ModelCapability[]
}

export interface EligibleImageStudioKeyGroup {
  id: number
  name: string
}

export interface EligibleImageStudioAPIKey {
  id: number
  name: string
  key: string
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

export interface GeneratedImage {
  blob: Blob
  mimeType: string
  revisedPrompt?: string
}

export interface GenerateImagesInput {
  model: string
  prompt: string
  n?: number
  size?: string
  quality?: string
  signal?: AbortSignal
}

export interface EditImagesInput extends GenerateImagesInput {
  image: File | Blob
  imageName?: string
  mask?: File | Blob | null
  maskName?: string
}

export const MAX_IMAGE_BYTES = 20 * 1024 * 1024

const MAX_BASE64_CHARACTERS = Math.ceil(MAX_IMAGE_BYTES / 3) * 4
const ALLOWED_IMAGE_MIME_TYPES = ['image/png', 'image/jpeg', 'image/webp'] as const

export type AllowedImageMimeType = (typeof ALLOWED_IMAGE_MIME_TYPES)[number]

const allowedImageMimeTypes = new Set<string>(ALLOWED_IMAGE_MIME_TYPES)

function authHeaders(apiKey: string, extra?: HeadersInit): HeadersInit {
  return {
    Authorization: `Bearer ${apiKey}`,
    ...extra,
  }
}

async function gatewayError(response: Response): Promise<Error> {
  let message = response.statusText || `HTTP ${response.status}`
  let code: string | number = response.status
  try {
    const body = await response.json()
    message = body?.error?.message || body?.message || message
    code = body?.error?.code || body?.code || code
  } catch {
    // Preserve the HTTP status when an upstream proxy returns non-JSON content.
  }
  const error = new Error(message)
  Object.assign(error, { status: response.status, code })
  return error
}

export async function listModelCapabilities(
  apiKey: string,
  signal?: AbortSignal,
): Promise<ModelCapabilitiesResponse> {
  const response = await fetch(buildGatewayUrl('/v1/models/capabilities'), {
    headers: authHeaders(apiKey),
    signal,
  })
  if (!response.ok) throw await gatewayError(response)
  return response.json()
}

export async function listEligibleImageStudioKeys(
  signal?: AbortSignal,
): Promise<EligibleImageStudioKeysResponse> {
  const response = await apiClient.get<unknown>('/image-studio/eligible-keys', { signal })
  const body = response.data
  const payload = isRecord(body) && Array.isArray(body.items)
    ? body
    : isRecord(body) && isRecord(body.data)
      ? body.data
      : null
  const items = payload && Array.isArray(payload.items)
    ? payload.items.map(parseEligibleImageStudioKey).filter((item): item is EligibleImageStudioKey => item !== null)
    : []
  return { items }
}

function isRecord(value: unknown): value is Record<string, unknown> {
  return typeof value === 'object' && value !== null && !Array.isArray(value)
}

function isPositiveID(value: unknown): value is number {
  return typeof value === 'number' && Number.isSafeInteger(value) && value > 0
}

function parseEligibleImageStudioKey(value: unknown): EligibleImageStudioKey | null {
  if (!isRecord(value) || !isRecord(value.api_key) || !Array.isArray(value.capabilities)) return null

  const apiKey = value.api_key
  const group = apiKey.group
  if (
    !isPositiveID(apiKey.id)
    || typeof apiKey.name !== 'string'
    || typeof apiKey.key !== 'string'
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
      key: apiKey.key,
      group_id: apiKey.group_id,
      group: { id: group.id, name: group.name },
    },
    capabilities: value.capabilities as ModelCapability[],
  }
}

function canonicalImageMimeType(value: string): AllowedImageMimeType {
  const normalized = value.trim().toLowerCase()
  if (!allowedImageMimeTypes.has(normalized)) {
    throw new Error('The image API returned an unsupported image format')
  }
  return normalized as AllowedImageMimeType
}

function outputFormatMimeType(value: string): AllowedImageMimeType {
  switch (value.trim().toLowerCase()) {
    case 'png':
      return 'image/png'
    case 'jpg':
    case 'jpeg':
      return 'image/jpeg'
    case 'webp':
      return 'image/webp'
    default:
      throw new Error('The image API returned an unsupported image format')
  }
}

function splitBase64Value(value: string): { encoded: string, dataUrlMimeType?: AllowedImageMimeType } {
  if (!value.startsWith('data:')) return { encoded: value }

  const match = /^data:([^;,]+);base64,(.*)$/s.exec(value)
  if (!match) throw new Error('The image API returned malformed base64 image data')
  return {
    encoded: match[2],
    dataUrlMimeType: canonicalImageMimeType(match[1]),
  }
}

function decodeBase64(value: string): { bytes: Uint8Array, dataUrlMimeType?: AllowedImageMimeType } {
  const { encoded, dataUrlMimeType } = splitBase64Value(value)
  if (!encoded || encoded.length > MAX_BASE64_CHARACTERS || encoded.length % 4 === 1) {
    throw new Error('The image API returned invalid or oversized base64 image data')
  }
  if (!/^[A-Za-z0-9+/]+={0,2}$/.test(encoded) || (encoded.includes('=') && encoded.length % 4 !== 0)) {
    throw new Error('The image API returned malformed base64 image data')
  }

  const unpadded = encoded.replace(/=+$/, '')
  const padded = unpadded.padEnd(Math.ceil(unpadded.length / 4) * 4, '=')
  let binary: string
  try {
    binary = atob(padded)
  } catch {
    throw new Error('The image API returned malformed base64 image data')
  }
  if (binary.length > MAX_IMAGE_BYTES || btoa(binary).replace(/=+$/, '') !== unpadded) {
    throw new Error('The image API returned invalid or oversized base64 image data')
  }
  const bytes = new Uint8Array(binary.length)
  for (let index = 0; index < binary.length; index += 1) {
    bytes[index] = binary.charCodeAt(index)
  }
  return { bytes, dataUrlMimeType }
}

function declaredMimeTypeForImageItem(item: Record<string, unknown>): AllowedImageMimeType | undefined {
  const declared: AllowedImageMimeType[] = []
  const explicit = typeof item.mime_type === 'string' ? item.mime_type.trim() : ''
  if (explicit) declared.push(canonicalImageMimeType(explicit))
  const outputFormat = typeof item.output_format === 'string' ? item.output_format.trim().toLowerCase() : ''
  if (outputFormat) declared.push(outputFormatMimeType(outputFormat))
  if (declared.some(mimeType => mimeType !== declared[0])) {
    throw new Error('The image API returned conflicting image formats')
  }
  return declared[0]
}

function detectImageMimeType(bytes: Uint8Array): AllowedImageMimeType | null {
  if (bytes.length >= 8 &&
    bytes[0] === 0x89 && bytes[1] === 0x50 && bytes[2] === 0x4e && bytes[3] === 0x47 &&
    bytes[4] === 0x0d && bytes[5] === 0x0a && bytes[6] === 0x1a && bytes[7] === 0x0a) {
    return 'image/png'
  }
  if (bytes.length >= 3 && bytes[0] === 0xff && bytes[1] === 0xd8 && bytes[2] === 0xff) {
    return 'image/jpeg'
  }
  if (bytes.length >= 12 &&
    bytes[0] === 0x52 && bytes[1] === 0x49 && bytes[2] === 0x46 && bytes[3] === 0x46 &&
    bytes[8] === 0x57 && bytes[9] === 0x45 && bytes[10] === 0x42 && bytes[11] === 0x50) {
    return 'image/webp'
  }
  return null
}

function readBlobArrayBuffer(blob: Blob): Promise<ArrayBuffer> {
  if (typeof blob.arrayBuffer === 'function') return blob.arrayBuffer()
  return new Promise((resolve, reject) => {
    const reader = new FileReader()
    reader.onload = () => {
      if (reader.result instanceof ArrayBuffer) resolve(reader.result)
      else reject(new Error('The image data could not be read'))
    }
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

  if (typeof Image === 'undefined') return
  const image = new Image()
  if (typeof image.decode !== 'function' || typeof URL.createObjectURL !== 'function') return

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

export async function validateImageBlob(
  blob: Blob,
  declaredMimeType: string | undefined = blob.type,
): Promise<AllowedImageMimeType> {
  if (blob.size < 1 || blob.size > MAX_IMAGE_BYTES) {
    throw new Error('The image is empty or exceeds the 20 MB limit')
  }

  const declared = declaredMimeType ? canonicalImageMimeType(declaredMimeType) : undefined
  const header = new Uint8Array(await readBlobArrayBuffer(blob.slice(0, 12)))
  const detected = detectImageMimeType(header)
  if (!detected) throw new Error('The image data does not contain a supported image')
  if (declared && declared !== detected) throw new Error('The image MIME type does not match its contents')

  const verifiedBlob = blob.type === detected ? blob : new Blob([blob], { type: detected })
  await verifyBrowserCanDecode(verifiedBlob)
  return detected
}

async function parseImagesResponse(response: Response, expectedCount: number): Promise<GeneratedImage[]> {
  if (!response.ok) throw await gatewayError(response)
  const body = await response.json()
  const items = Array.isArray(body?.data) ? body.data : []
  if (items.length !== expectedCount) {
    throw new Error(`The image API returned ${items.length} images; expected ${expectedCount}`)
  }
  const images: GeneratedImage[] = []

  for (const rawItem of items) {
    const item = (rawItem && typeof rawItem === 'object' ? rawItem : {}) as Record<string, unknown>
    const revisedPrompt = typeof item.revised_prompt === 'string' ? item.revised_prompt : undefined
    if (typeof item.b64_json === 'string' && item.b64_json.trim()) {
      const declaredMimeType = declaredMimeTypeForImageItem(item)
      const { bytes, dataUrlMimeType } = decodeBase64(item.b64_json)
      if (declaredMimeType && dataUrlMimeType && declaredMimeType !== dataUrlMimeType) {
        throw new Error('The image API returned conflicting image formats')
      }
      const claimedMimeType = declaredMimeType || dataUrlMimeType
      const unverifiedBlob = new Blob([bytes], { type: claimedMimeType || '' })
      const mimeType = await validateImageBlob(unverifiedBlob, claimedMimeType)
      images.push({ blob: new Blob([bytes], { type: mimeType }), mimeType, revisedPrompt })
      continue
    }
    // Image Studio requests b64_json and never follows an upstream-provided
    // URL. This keeps credentials and browser network access pinned to the
    // configured gateway even if an upstream response is malformed.
  }

  if (images.length !== expectedCount) {
    throw new Error(`The image API returned ${images.length} decodable images; expected ${expectedCount}`)
  }
  return images
}

function expectedImageCount(requestedCount?: number): number {
  const expected = requestedCount === undefined ? 1 : requestedCount
  if (!Number.isInteger(expected) || expected < 1 || expected > 4) {
    throw new Error('Image output count must be between 1 and 4')
  }
  return expected
}

export async function generateImages(apiKey: string, input: GenerateImagesInput): Promise<GeneratedImage[]> {
  const expectedCount = expectedImageCount(input.n)
  const body: Record<string, unknown> = {
    model: input.model,
    prompt: input.prompt,
    response_format: 'b64_json',
  }
  if (input.n !== undefined) body.n = input.n
  if (input.size) body.size = input.size
  if (input.quality) body.quality = input.quality

  const response = await fetch(buildGatewayUrl('/v1/images/generations'), {
    method: 'POST',
    headers: authHeaders(apiKey, { 'Content-Type': 'application/json' }),
    body: JSON.stringify(body),
    signal: input.signal,
  })
  return parseImagesResponse(response, expectedCount)
}

export async function editImages(apiKey: string, input: EditImagesInput): Promise<GeneratedImage[]> {
  const expectedCount = expectedImageCount(input.n)
  const body = new FormData()
  body.set('model', input.model)
  body.set('prompt', input.prompt)
  if (input.n !== undefined) body.set('n', String(input.n))
  if (input.size) body.set('size', input.size)
  if (input.quality) body.set('quality', input.quality)
  body.set('response_format', 'b64_json')
  body.set('image', input.image, input.imageName || 'reference.png')
  if (input.mask) body.set('mask', input.mask, input.maskName || 'mask.png')

  const response = await fetch(buildGatewayUrl('/v1/images/edits'), {
    method: 'POST',
    headers: authHeaders(apiKey),
    body,
    signal: input.signal,
  })
  return parseImagesResponse(response, expectedCount)
}
