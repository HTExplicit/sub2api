import { describe, expect, it } from 'vitest'
import { resourceRequest, type PluginResourceDescriptor } from '../resourceClient'

const descriptor: PluginResourceDescriptor = { name: 'prompts.read', capability: 'extensions.request.v1', permission: 'admin', method: 'GET', path: '/api/v1/admin/system-prompts/:id', available: true }

describe('named plugin resources', () => {
  it('accepts only host-declared paths and identifier fields', () => {
    expect(resourceRequest(descriptor, { params: { id: 42 } }).url).toBe('/api/v1/admin/system-prompts/42')
    expect(() => resourceRequest(descriptor, { params: { id: '../users' } })).toThrow()
    expect(() => resourceRequest(descriptor, { params: { id: 42, other: 7 } })).toThrow()
    expect(() => resourceRequest({ ...descriptor, path: '//other.example/api/v1/admin' }, { params: {} })).toThrow()
    expect(() => resourceRequest({ ...descriptor, available: false }, { params: { id: 42 } })).toThrow()
  })

  it('retains uploaded capture bytes and rejects body confusion', () => {
    const capture = new File(['captured prompt'], 'capture.txt', { type: 'text/plain' })
    const upload = { ...descriptor, name: 'skills.sync.start', path: '/api/v1/admin/system-prompts/skill-registry/syncs', method: 'POST' }
    const result = resourceRequest(upload, { form: [['expected_revision', '5'], ['prompt_capture', capture]] })
    expect(result.data).toBeInstanceOf(FormData)
    expect((result.data as FormData).get('prompt_capture')).toBe(capture)
    expect(() => resourceRequest(descriptor, { params: { id: 42 }, body: { privileged: true } })).toThrow()
    expect(() => resourceRequest(upload, { body: {}, form: [['prompt_capture', capture]] })).toThrow()
  })
})
