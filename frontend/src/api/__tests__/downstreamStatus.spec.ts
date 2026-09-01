import { afterEach, describe, expect, it, vi } from 'vitest'

import { clearDownstreamStatusCache, fetchDownstreamStatus } from '@/api/downstreamStatus'

const response = (body: unknown) => Promise.resolve({ ok: true, json: async () => body } as Response)

describe('downstream release status', () => {
  afterEach(() => {
    vi.unstubAllGlobals()
    clearDownstreamStatusCache()
  })

  it('reports review-required candidate with direct links', async () => {
    vi.stubGlobal('fetch', vi.fn()
      .mockImplementationOnce(() => response({ tag_name: 'v0.1.184-codexrip.1', html_url: 'https://release' }))
      .mockImplementationOnce(() => response([{
        number: 105,
        title: 'merge upstream',
        html_url: 'https://candidate',
        head: { ref: 'sync/upstream-0.1.185' },
        labels: [{ name: 'upstream-review-required' }]
      }]))
      .mockImplementationOnce(() => response({ workflow_runs: [] })))

    const result = await fetchDownstreamStatus('0.1.184-codexrip.1', '0.1.185')
    expect(result.status).toBe('review_required')
    expect(result.candidate_pr?.url).toBe('https://candidate')
    expect(result.links.some(link => link.label === 'official')).toBe(true)
  })

  it('distinguishes release-ready, approval, deployment and failure states', async () => {
    for (const [run, expected] of [
      [undefined, 'release_ready'],
      [{ status: 'in_progress', conclusion: null, jobStatus: 'waiting' }, 'approval_pending'],
      [{ status: 'in_progress', conclusion: null }, 'deploying'],
      [{ status: 'completed', conclusion: 'failure' }, 'failed']
    ] as const) {
      clearDownstreamStatusCache()
      vi.stubGlobal('fetch', vi.fn()
        .mockImplementationOnce(() => response({ tag_name: 'v0.1.185-codexrip.1', html_url: 'https://release' }))
        .mockImplementationOnce(() => response([]))
        .mockImplementationOnce(() => response({ workflow_runs: run ? [{
          id: 1,
          display_title: 'Deploy v0.1.185-codexrip.1 (preserve)',
          html_url: 'https://production',
          ...run
        }] : [] }))
        .mockImplementationOnce(() => response({ jobs: run && 'jobStatus' in run
          ? [{ name: 'deploy', status: run.jobStatus }]
          : [{ name: 'deploy', status: 'in_progress' }] })))
      expect((await fetchDownstreamStatus('0.1.184-codexrip.1', '0.1.185', true)).status).toBe(expected)
    }
  })
})
