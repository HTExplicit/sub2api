import { beforeEach, describe, expect, it, vi } from 'vitest'
import {
  cancelCindyBalanceProbe,
  canonicalizeCindyBalanceProbeScope,
  createCindyBalanceProbe,
  getCindyBalanceProbeJob,
  listCindyBalanceProbeItems,
  listCindyBalanceProbeJobs,
  pauseCindyBalanceProbe,
  previewCindyBalanceProbe,
  resumeCindyBalanceProbe,
  setCindyBalanceProbeRate,
} from '@/api/admin/cindyBalanceProbe'

const mocks = vi.hoisted(() => ({
  get: vi.fn(),
  post: vi.fn(),
  patch: vi.fn(),
}))

vi.mock('@/api/client', () => ({ apiClient: mocks }))

const base = '/admin/cindy/balance-probe-jobs'
const payload = { scope: { mode: 'all' as const }, rate_rps: 0.5 }

describe('Cindy balance probe admin API', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    mocks.get.mockResolvedValue({ data: {} })
    mocks.post.mockResolvedValue({ data: {} })
    mocks.patch.mockResolvedValue({ data: {} })
  })

  it('uses preview and create endpoints with the confirmed candidate fingerprint', async () => {
    await previewCindyBalanceProbe(payload)
    await createCindyBalanceProbe({
      ...payload,
      expected_count: 12,
      candidate_fingerprint: 'fingerprint',
    })

    expect(mocks.post).toHaveBeenNthCalledWith(1, `${base}/preview`, payload)
    expect(mocks.post).toHaveBeenNthCalledWith(2, base, {
      ...payload,
      expected_count: 12,
      candidate_fingerprint: 'fingerprint',
    })
  })

  it('loads recent jobs, one job, and paginated filtered items', async () => {
    await listCindyBalanceProbeJobs(10)
    await getCindyBalanceProbeJob(7)
    await listCindyBalanceProbeItems(7, { state: 'exhausted', page: 2, page_size: 20 })

    expect(mocks.get).toHaveBeenNthCalledWith(1, base, { params: { limit: 10 } })
    expect(mocks.get).toHaveBeenNthCalledWith(2, `${base}/7`)
    expect(mocks.get).toHaveBeenNthCalledWith(3, `${base}/7/items`, {
      params: { state: 'exhausted', page: 2, page_size: 20 },
    })
  })

  it('uses dedicated rate and lifecycle controls', async () => {
    await setCindyBalanceProbeRate(7, 0.8)
    await pauseCindyBalanceProbe(7)
    await resumeCindyBalanceProbe(7)
    await cancelCindyBalanceProbe(7)

    expect(mocks.patch).toHaveBeenCalledWith(`${base}/7/rate`, { rate_rps: 0.8 })
    expect(mocks.post).toHaveBeenNthCalledWith(1, `${base}/7/pause`)
    expect(mocks.post).toHaveBeenNthCalledWith(2, `${base}/7/resume`)
    expect(mocks.post).toHaveBeenNthCalledWith(3, `${base}/7/cancel`)
  })

  it('canonicalizes selected scopes from current and legacy preview responses', () => {
    expect(canonicalizeCindyBalanceProbeScope({
      mode: 'selected',
      account_ids: [10, 9, 10],
      filters: { account_ids: [99] },
    })).toEqual({ mode: 'selected', account_ids: [9, 10] })

    expect(canonicalizeCindyBalanceProbeScope({
      mode: 'selected',
      filters: { account_ids: [10, 9, 10] },
    })).toEqual({ mode: 'selected', account_ids: [9, 10] })
  })
})
