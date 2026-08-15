import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import {
  editImages,
  generateImages,
  listModelCapabilities,
  MAX_IMAGE_BYTES,
} from '@/api/imageStudio'

vi.mock('@/api/client', () => ({
  buildGatewayUrl: (path: string) => `https://gateway.test${path}`,
}))

const PNG_1X1 = 'iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mP8/x8AAusB9Y9ZlLsAAAAASUVORK5CYII='
const JPEG_1X1 = '/9j/4AAQSkZJRgABAQAAAQABAAD/2wBDAP//////////////////////////////////////////////////////////////////////////////////////2wBDAf//////////////////////////////////////////////////////////////////////////////////////wAARCAABAAEDASIAAhEBAxEB/8QAFQABAQAAAAAAAAAAAAAAAAAAAAX/xAAUEAEAAAAAAAAAAAAAAAAAAAAA/9oADAMBAAIQAxAAAAH/xAAUEAEAAAAAAAAAAAAAAAAAAAAA/9oACAEBAAEFAn//xAAUEQEAAAAAAAAAAAAAAAAAAAAA/9oACAEDAQE/AX//xAAUEQEAAAAAAAAAAAAAAAAAAAAA/9oACAECAQE/AX//xAAUEAEAAAAAAAAAAAAAAAAAAAAA/9oACAEBAAY/An//xAAUEAEAAAAAAAAAAAAAAAAAAAAA/9oACAEBAAE/IX//2gAMAwEAAgADAAAAEP/EABQRAQAAAAAAAAAAAAAAAAAAABD/2gAIAQMBAT8QH//EABQRAQAAAAAAAAAAAAAAAAAAABD/2gAIAQIBAT8QH//EABQQAQAAAAAAAAAAAAAAAAAAABD/2gAIAQEAAT8QH//Z'
const WEBP_1X1 = 'UklGRh4AAABXRUJQVlA4TBEAAAAvAAAAAAfQ//73v/+BiOh/AAA='

function imageResponse(data: unknown[]): Response {
  return new Response(JSON.stringify({ data }), {
    status: 200,
    headers: { 'Content-Type': 'application/json' },
  })
}

describe('imageStudio API', () => {
  beforeEach(() => {
    vi.restoreAllMocks()
    vi.stubGlobal('createImageBitmap', vi.fn(async () => ({
      width: 1,
      height: 1,
      close: vi.fn(),
    })))
  })

  afterEach(() => {
    vi.unstubAllGlobals()
  })

  it('loads model capabilities with the explicitly selected gateway key', async () => {
    const fetchMock = vi.spyOn(globalThis, 'fetch').mockResolvedValue(new Response(JSON.stringify({
      object: 'list',
      catalog_version: 'test',
      data: [],
    }), { status: 200, headers: { 'Content-Type': 'application/json' } }))

    await listModelCapabilities('sk-selected')

    expect(fetchMock).toHaveBeenCalledWith('https://gateway.test/v1/models/capabilities', expect.objectContaining({
      headers: expect.objectContaining({ Authorization: 'Bearer sk-selected' }),
    }))
  })

  it('accepts real minimal PNG, JPEG, and WebP payloads only after browser decoding', async () => {
    const fetchMock = vi.spyOn(globalThis, 'fetch').mockResolvedValue(imageResponse([
      { b64_json: PNG_1X1, output_format: 'png' },
      { b64_json: JPEG_1X1, mime_type: 'image/jpeg' },
      { b64_json: `data:image/webp;base64,${WEBP_1X1}`, output_format: 'webp' },
    ]))

    const result = await generateImages('sk-selected', {
      model: 'gpt-image-2',
      prompt: 'draw',
      n: 3,
      size: '1024x1024',
      quality: 'low',
    })

    expect(result.map(image => image.mimeType)).toEqual(['image/png', 'image/jpeg', 'image/webp'])
    expect(result.every(image => image.blob.size > 0)).toBe(true)
    expect(globalThis.createImageBitmap).toHaveBeenCalledTimes(3)
    const [, request] = fetchMock.mock.calls[0]
    expect(request?.headers).toEqual(expect.objectContaining({
      Authorization: 'Bearer sk-selected',
      'Content-Type': 'application/json',
    }))
    expect(JSON.parse(String(request?.body))).toEqual(expect.objectContaining({
      model: 'gpt-image-2',
      prompt: 'draw',
      n: 3,
      response_format: 'b64_json',
    }))
  })

  it('fails closed instead of following an upstream-provided image URL', async () => {
    const fetchMock = vi.spyOn(globalThis, 'fetch').mockResolvedValue(imageResponse([
      { url: 'https://untrusted.example/generated.png' },
    ]))

    await expect(generateImages('sk-selected', {
      model: 'gpt-image-2',
      prompt: 'draw',
      n: 1,
    })).rejects.toThrow('returned 0 decodable images; expected 1')
    expect(fetchMock).toHaveBeenCalledTimes(1)
  })

  it('rejects mixed base64 and URL items when fewer images decode than requested', async () => {
    vi.spyOn(globalThis, 'fetch').mockResolvedValue(imageResponse([
      { b64_json: PNG_1X1, mime_type: 'image/png' },
      { url: 'https://untrusted.example/generated.png' },
    ]))

    await expect(generateImages('sk-selected', {
      model: 'gpt-image-2',
      prompt: 'draw two',
      n: 2,
    })).rejects.toThrow('returned 1 decodable images; expected 2')
  })

  it('fails closed when the upstream returns fewer images than requested', async () => {
    vi.spyOn(globalThis, 'fetch').mockResolvedValue(imageResponse([{ b64_json: PNG_1X1 }]))

    await expect(generateImages('sk-selected', {
      model: 'gpt-image-2',
      prompt: 'draw four',
      n: 4,
    })).rejects.toThrow('returned 1 images; expected 4')
  })

  it.each([
    ['a PNG payload declared as WebP', { b64_json: PNG_1X1, mime_type: 'image/webp' }, 'does not match'],
    ['an SVG data URL', { b64_json: `data:image/svg+xml;base64,${btoa('<svg xmlns="http://www.w3.org/2000/svg"/>')}` }, 'unsupported'],
    ['malformed base64', { b64_json: '%%%not-base64%%%' }, 'malformed'],
    ['non-image bytes', { b64_json: btoa('not an image') }, 'supported image'],
  ])('rejects %s from the service', async (_label, item, message) => {
    vi.spyOn(globalThis, 'fetch').mockResolvedValue(imageResponse([item]))

    await expect(generateImages('sk-selected', {
      model: 'gpt-image-2',
      prompt: 'draw',
      n: 1,
    })).rejects.toThrow(message)
  })

  it('rejects a response whose decoded image exceeds the byte limit', async () => {
    vi.spyOn(globalThis, 'fetch').mockResolvedValue(imageResponse([{ b64_json: 'AAAA' }]))
    vi.spyOn(globalThis, 'atob').mockReturnValue('\0'.repeat(MAX_IMAGE_BYTES + 1))

    await expect(generateImages('sk-selected', {
      model: 'gpt-image-2',
      prompt: 'draw',
      n: 1,
    })).rejects.toThrow('oversized')
  })

  it('rejects magic-looking bytes when the browser decoder cannot decode them', async () => {
    vi.mocked(globalThis.createImageBitmap).mockRejectedValueOnce(new Error('decode failed'))
    vi.spyOn(globalThis, 'fetch').mockResolvedValue(imageResponse([{
      b64_json: btoa(String.fromCharCode(0x89, 0x50, 0x4e, 0x47, 0x0d, 0x0a, 0x1a, 0x0a)),
      mime_type: 'image/png',
    }]))

    await expect(generateImages('sk-selected', {
      model: 'gpt-image-2',
      prompt: 'draw',
      n: 1,
    })).rejects.toThrow('could not be decoded')
  })

  it('sends edit/reference/mask fields as multipart without overriding the boundary', async () => {
    const fetchMock = vi.spyOn(globalThis, 'fetch').mockResolvedValue(imageResponse([
      { b64_json: PNG_1X1, output_format: 'png' },
    ]))
    const source = new File([Uint8Array.from(atob(PNG_1X1), value => value.charCodeAt(0))], 'source.png', { type: 'image/png' })
    const mask = new File([Uint8Array.from(atob(PNG_1X1), value => value.charCodeAt(0))], 'mask.png', { type: 'image/png' })

    await editImages('sk-selected', {
      model: 'gemini-3-pro-image',
      prompt: 'replace sky',
      n: 1,
      size: '1024x1024',
      quality: 'low',
      image: source,
      mask,
    })

    const [, request] = fetchMock.mock.calls[0]
    expect(request?.headers).toEqual({ Authorization: 'Bearer sk-selected' })
    expect(request?.body).toBeInstanceOf(FormData)
    const form = request?.body as FormData
    expect(form.get('model')).toBe('gemini-3-pro-image')
    expect(form.get('image')).toBeInstanceOf(Blob)
    expect(form.get('mask')).toBeInstanceOf(Blob)
  })
})
