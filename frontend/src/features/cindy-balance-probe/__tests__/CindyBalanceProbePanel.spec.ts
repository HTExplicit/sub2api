import { enableAutoUnmount, flushPromises, mount } from '@vue/test-utils'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import CindyBalanceProbePanel from '../CindyBalanceProbePanel.vue'

enableAutoUnmount(afterEach)

const mocks = vi.hoisted(() => ({
  preview: vi.fn(),
  create: vi.fn(),
  list: vi.fn(),
  get: vi.fn(),
  listItems: vi.fn(),
  setRate: vi.fn(),
  pause: vi.fn(),
  resume: vi.fn(),
  cancel: vi.fn(),
  showError: vi.fn(),
  showSuccess: vi.fn(),
}))

vi.mock('@/api/admin/cindyBalanceProbe', async () => {
  const actual = await vi.importActual<typeof import('@/api/admin/cindyBalanceProbe')>('@/api/admin/cindyBalanceProbe')
  return {
    ...actual,
    cindyBalanceProbeAPI: {
      preview: mocks.preview,
      create: mocks.create,
      list: mocks.list,
      get: mocks.get,
      listItems: mocks.listItems,
      setRate: mocks.setRate,
      pause: mocks.pause,
      resume: mocks.resume,
      cancel: mocks.cancel,
    },
  }
})

vi.mock('@/stores/app', () => ({
  useAppStore: () => ({ showError: mocks.showError, showSuccess: mocks.showSuccess }),
}))

vi.mock('vue-i18n', async () => {
  const actual = await vi.importActual<typeof import('vue-i18n')>('vue-i18n')
  return { ...actual, useI18n: () => ({ t: (key: string, values?: { count?: number }) => values?.count == null ? key : `${key}:${values.count}` }) }
})

const counts = {
  pending: 1,
  running: 0,
  healthy: 1,
  recovered: 1,
  exhausted: 1,
  inconclusive: 0,
  skipped: 0,
}

const runningJob = {
  id: 7,
  status: 'running',
  scope: { mode: 'all' },
  rate_rps: 0.5,
  candidate_count: 4,
  candidate_fingerprint: 'job-fingerprint',
  request_count: 4,
  consecutive_upstream_failures: 0,
  created_at: '2026-08-16T00:00:00Z',
  updated_at: '2026-08-16T00:00:00Z',
  counts,
}

const preview = {
  scope: {
    mode: 'selected',
    account_ids: [9, 10],
    filters: { account_ids: [], sort_by: 'name', sort_order: 'asc' },
  },
  candidate_count: 2,
  marked_count: 1,
  unmarked_count: 1,
  candidate_fingerprint: 'preview-fingerprint',
  minimum_calls: 2,
  maximum_calls: 3,
  rate_rps: 0.5,
  minimum_eta_seconds: 2,
  maximum_eta_seconds: 4,
}

function render() {
  return mount(CindyBalanceProbePanel, {
    props: {
      selectedIds: [9, 10],
      initiallyExpanded: true,
      filters: {
        platforms: ['openai'],
        proxy_ids: [3],
        include_direct: true,
        group_id: -1,
        account_ids: [44],
        sort_by: 'name',
        sort_order: 'asc',
      },
    },
    global: {
      stubs: {
        Icon: true,
        ConfirmDialog: {
          props: ['show'],
          emits: ['confirm', 'cancel'],
          template: '<div v-if="show" data-test="cancel-confirm"><button data-test="cancel-confirm-submit" @click="$emit(\'confirm\')">confirm</button></div>',
        },
      },
    },
  })
}

describe('CindyBalanceProbePanel', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    mocks.list.mockResolvedValue({ items: [], total: 0 })
    mocks.preview.mockResolvedValue(preview)
    mocks.create.mockResolvedValue(runningJob)
    mocks.get.mockResolvedValue(runningJob)
    mocks.listItems.mockResolvedValue({ items: [], total: 0, page: 1, page_size: 20 })
    mocks.setRate.mockResolvedValue({ ...runningJob, rate_rps: 0.8 })
    mocks.pause.mockResolvedValue({ ...runningJob, status: 'paused' })
    mocks.resume.mockResolvedValue(runningJob)
    mocks.cancel.mockResolvedValue({ ...runningJob, status: 'cancel_requested' })
  })

  it('previews selected accounts before creating with the server fingerprint', async () => {
    const wrapper = render()
    await flushPromises()

    await wrapper.get('[data-test="cindy-probe-scope-selected"]').trigger('click')
    await wrapper.get('[data-test="cindy-probe-preview"]').trigger('click')
    await flushPromises()

    expect(mocks.preview).toHaveBeenCalledWith({
      scope: { mode: 'selected', account_ids: [9, 10] },
      rate_rps: 0.5,
    })
    expect(wrapper.get('[data-test="cindy-probe-preview-result"]').text()).toContain('2')

    await wrapper.get('[data-test="cindy-probe-create"]').trigger('click')
    await flushPromises()
    expect(mocks.create).toHaveBeenCalledWith({
      scope: { mode: 'selected', account_ids: [9, 10] },
      rate_rps: 0.5,
      expected_count: 2,
      candidate_fingerprint: 'preview-fingerprint',
    })
  })

  it('canonicalizes a legacy nested selected preview before create', async () => {
    mocks.preview.mockResolvedValue({
      ...preview,
      scope: { mode: 'selected', filters: { account_ids: [10, 9, 10] } },
    })
    const wrapper = render()
    await flushPromises()

    await wrapper.get('[data-test="cindy-probe-scope-selected"]').trigger('click')
    await wrapper.get('[data-test="cindy-probe-preview"]').trigger('click')
    await flushPromises()
    await wrapper.get('[data-test="cindy-probe-create"]').trigger('click')
    await flushPromises()

    expect(mocks.create).toHaveBeenCalledWith(expect.objectContaining({
      scope: { mode: 'selected', account_ids: [9, 10] },
    }))
  })

  it('passes the exact current account filters to preview', async () => {
    const wrapper = render()
    await flushPromises()

    await wrapper.get('[data-test="cindy-probe-scope-filter"]').trigger('click')
    await wrapper.get('[data-test="cindy-probe-preview"]').trigger('click')
    await flushPromises()

    expect(mocks.preview).toHaveBeenCalledWith(expect.objectContaining({
      scope: {
        mode: 'filter',
        filters: expect.objectContaining({
          platforms: ['openai'],
          proxy_ids: [3],
          include_direct: true,
          group_id: -1,
          account_ids: [44],
        }),
      },
    }))
  })

  it('loads job progress, changes rate, pauses, and pages account results', async () => {
    mocks.list.mockResolvedValue({ items: [runningJob], total: 1 })
    mocks.listItems.mockResolvedValue({
      items: [{
        id: 1,
        job_id: 7,
        account_id: 91,
        ordinal: 1,
        was_marked: false,
        state: 'healthy',
        request_count: 1,
        luna_at: '2029-08-16T00:00:00Z',
        terra_at: '2029-08-16T00:01:00Z',
        finished_at: '2030-08-16T00:02:00Z',
        created_at: '',
        updated_at: '',
      }],
      total: 21,
      page: 1,
      page_size: 20,
    })
    const wrapper = render()
    await flushPromises()

    expect(wrapper.get('[data-test="cindy-probe-job-status"]').text()).toContain('running')
    expect(wrapper.get('[data-test="cindy-probe-counts"]').text()).toContain('1')
    expect(wrapper.get('[data-test="cindy-probe-item-checked-at"]').text()).toContain('2030')
    await wrapper.get('[data-test="cindy-probe-job-rate"]').setValue('0.8')
    await wrapper.get('[data-test="cindy-probe-save-rate"]').trigger('click')
    await flushPromises()
    expect(mocks.setRate).toHaveBeenCalledWith(7, 0.8)

    await wrapper.get('[data-test="cindy-probe-pause"]').trigger('click')
    await flushPromises()
    expect(mocks.pause).toHaveBeenCalledWith(7)

    await wrapper.get('[data-test="cindy-probe-cancel"]').trigger('click')
    expect(mocks.cancel).not.toHaveBeenCalled()
    await wrapper.get('[data-test="cancel-confirm-submit"]').trigger('click')
    await flushPromises()
    expect(mocks.cancel).toHaveBeenCalledWith(7)

    await wrapper.get('[data-test="cindy-probe-items-next"]').trigger('click')
    await flushPromises()
    expect(mocks.listItems).toHaveBeenLastCalledWith(7, expect.objectContaining({ page: 2, page_size: 20 }))
  })
})
