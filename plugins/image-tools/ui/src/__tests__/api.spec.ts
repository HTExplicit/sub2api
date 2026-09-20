import { beforeEach, describe, expect, it, vi } from 'vitest'
import { createImageStudioJob, downloadImageStudioArtifact, getImageStudioJob, listEligibleImageStudioKeys } from '../api'
const invoke = vi.hoisted(() => vi.fn())
vi.mock('@sub2api/plugin-ui', () => ({ resource: invoke }))

describe('image studio named resources', () => {
  beforeEach(() => invoke.mockReset())
  it('keeps only the secret-free eligible-key DTO', async () => {
    invoke.mockResolvedValue({ items: [{ api_key: { id: 1, name: 'Images', group_id: 10, group: { id: 10, name: 'Cindy' }, key: 'must-not-enter-state' }, capabilities: [] }] })
    const result = await listEligibleImageStudioKeys()
    expect(result.items[0].api_key).toEqual({ id: 1, name: 'Images', group_id: 10, group: { id: 10, name: 'Cindy' } })
    expect(JSON.stringify(result)).not.toContain('must-not-enter-state')
    expect(invoke).toHaveBeenCalledWith('image.keys', {}, undefined)
  })
  it('creates a job with an ID and typed form instead of an API-key secret', async () => {
    invoke.mockResolvedValue({ id: 41, status: 'pending' })
    const job = await createImageStudioJob({ apiKeyId: 9, mode: 'generate', model: 'gpt-image-2', prompt: 'draw', count: 4 })
    expect(job.id).toBe(41)
    expect(invoke.mock.calls[0][0]).toBe('image.create')
    const form = new Map(invoke.mock.calls[0][1].form)
    expect(form.get('api_key_id')).toBe('9')
    expect(form.get('count')).toBe('4')
    expect(form.has('api_key')).toBe(false)
  })
  it('loads progress and binary artifacts through declared owner-scoped operations', async () => {
    invoke.mockResolvedValueOnce({ job: { id: 41, status: 'partially_succeeded' }, items: [], artifacts: [{ id: 52, job_id: 41 }] }).mockResolvedValueOnce(new Blob(['image'], { type: 'image/png' }))
    const detail = await getImageStudioJob(41)
    const blob = await downloadImageStudioArtifact(detail.artifacts[0])
    expect(detail.job.status).toBe('partially_succeeded')
    expect(blob.type).toBe('image/png')
    expect(invoke).toHaveBeenNthCalledWith(1, 'image.job', { params: { id: 41 } }, undefined)
    expect(invoke).toHaveBeenNthCalledWith(2, 'image.artifact', { params: { id: 41, artifact_id: 52 } }, undefined)
  })
})
