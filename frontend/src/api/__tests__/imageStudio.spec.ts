import { beforeEach, describe, expect, it, vi } from 'vitest'
import {
  createImageStudioJob,
  downloadImageStudioArtifact,
  getImageStudioJob,
  listEligibleImageStudioKeys,
} from '@/api/imageStudio'

const apiGet = vi.hoisted(() => vi.fn())
const apiPost = vi.hoisted(() => vi.fn())

vi.mock('@/api/client', () => ({
  apiClient: { get: apiGet, post: apiPost },
}))

describe('imageStudio job API', () => {
  beforeEach(() => {
    vi.clearAllMocks()
  })

  it('accepts only the secret-free eligible key DTO', async () => {
    apiGet.mockResolvedValue({ data: { items: [{
      api_key: {
        id: 1,
        name: 'Images',
        group_id: 10,
        group: { id: 10, name: 'Cindy' },
        key: 'must-not-enter-browser-state',
      },
      capabilities: [],
    }] } })

    const result = await listEligibleImageStudioKeys()

    expect(result.items).toEqual([{
      api_key: { id: 1, name: 'Images', group_id: 10, group: { id: 10, name: 'Cindy' } },
      capabilities: [],
    }])
    expect(JSON.stringify(result)).not.toContain('must-not-enter-browser-state')
    expect(JSON.stringify(result)).not.toContain('"key"')
  })

  it('creates a server-owned job without accepting an API key secret', async () => {
    apiPost.mockResolvedValue({ data: { id: 41, status: 'pending' } })

    const result = await createImageStudioJob({
      apiKeyId: 9,
      mode: 'generate',
      model: 'gpt-image-2',
      prompt: 'draw',
      count: 4,
      size: '1024x1024',
      quality: 'low',
    })

    expect(result.id).toBe(41)
    expect(apiPost).toHaveBeenCalledWith('/image-studio/jobs', expect.any(FormData), expect.objectContaining({ signal: undefined }))
    const form = apiPost.mock.calls[0]?.[1] as FormData
    expect(form.get('api_key_id')).toBe('9')
    expect(form.get('count')).toBe('4')
    expect(Array.from(form.keys())).not.toContain('api_key')
    expect(Array.from(form.keys())).not.toContain('key')
  })

  it('loads job progress and downloads an owner-scoped artifact through the session client', async () => {
    apiGet.mockResolvedValueOnce({ data: {
      job: { id: 41, status: 'partially_succeeded', count: 2, counts: { processed: 2, succeeded: 1, failed: 1, canceled: 0 } },
      items: [],
      artifacts: [{ id: 52, job_id: 41, kind: 'output', content_type: 'image/png', byte_size: 12, download_url: '/api/v1/image-studio/jobs/41/artifacts/52' }],
    } }).mockResolvedValueOnce({ data: new Blob(['image'], { type: 'image/png' }) })

    const detail = await getImageStudioJob(41)
    const blob = await downloadImageStudioArtifact(detail.artifacts[0])

    expect(detail.job.status).toBe('partially_succeeded')
    expect(blob.type).toBe('image/png')
    expect(apiGet).toHaveBeenNthCalledWith(1, '/image-studio/jobs/41', { signal: undefined })
    expect(apiGet).toHaveBeenNthCalledWith(2, '/image-studio/jobs/41/artifacts/52', { responseType: 'blob', signal: undefined })
  })
})
