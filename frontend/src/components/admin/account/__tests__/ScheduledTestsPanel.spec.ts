import { enableAutoUnmount, flushPromises, shallowMount } from '@vue/test-utils'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import ScheduledTestsPanel from '../ScheduledTestsPanel.vue'
import type { ScheduledTestPlan, ScheduledTestResult } from '@/types'

const mocks = vi.hoisted(() => ({
  listByAccount: vi.fn(),
  listResults: vi.fn(),
  showError: vi.fn(),
  showSuccess: vi.fn()
}))

vi.mock('@/api/admin', () => ({ adminAPI: { scheduledTests: mocks } }))
vi.mock('@/stores/app', () => ({ useAppStore: () => mocks }))
vi.mock('vue-i18n', async () => ({
  ...await vi.importActual<typeof import('vue-i18n')>('vue-i18n'),
  useI18n: () => ({ t: (key: string) => key })
}))

enableAutoUnmount(afterEach)
beforeEach(() => vi.clearAllMocks())

function deferred<T>() {
  let resolve!: (value: T) => void
  let reject!: (reason: unknown) => void
  const promise = new Promise<T>((res, rej) => { resolve = res; reject = rej })
  return { promise, resolve, reject }
}

const plan = (id: number, model: string): ScheduledTestPlan => ({
  id,
  account_id: 1,
  model_id: model,
  cron_expression: '0 * * * *',
  enabled: true,
  max_results: 100,
  auto_recover: false,
  last_run_at: null,
  next_run_at: null,
  created_at: '2026-10-05T00:00:00Z',
  updated_at: '2026-10-05T00:00:00Z'
})

const result = (id: number, latency: number): ScheduledTestResult => ({
  id,
  plan_id: 1,
  status: 'success',
  response_text: '',
  error_message: '',
  latency_ms: latency,
  started_at: '2026-10-05T00:00:00Z',
  finished_at: '2026-10-05T00:00:01Z',
  created_at: '2026-10-05T00:00:00Z'
})

async function open() {
  const wrapper = shallowMount(ScheduledTestsPanel, {
    props: { show: false, accountId: 1, modelOptions: [] },
    global: {
      stubs: { BaseDialog: { props: ['show'], template: '<div v-if="show"><slot /><slot name="footer" /></div>' } }
    }
  })
  await wrapper.setProps({ show: true })
  return wrapper
}

describe('scheduled test panel requests', () => {
  it('does not show a plan list that arrives after the panel moved to another account', async () => {
    const old = deferred<ScheduledTestPlan[]>()
    mocks.listByAccount.mockReturnValueOnce(old.promise).mockResolvedValueOnce([plan(2, 'current-model')])
    const wrapper = await open()

    await wrapper.setProps({ accountId: 2 })
    await flushPromises()
    old.resolve([plan(1, 'old-model')])
    await flushPromises()

    expect(mocks.listByAccount.mock.calls).toEqual([[1], [2]])
    expect(wrapper.text()).toContain('current-model')
    expect(wrapper.text()).not.toContain('old-model')
  })

  it('stays silent when a plan list fails after the panel was closed', async () => {
    const old = deferred<ScheduledTestPlan[]>()
    mocks.listByAccount.mockReturnValueOnce(old.promise)
    const wrapper = await open()

    await wrapper.setProps({ show: false, accountId: null })
    old.reject(new Error('obsolete'))
    await flushPromises()

    expect(mocks.showError).not.toHaveBeenCalled()
  })

  it('does not show results that were requested before the panel was reopened', async () => {
    const old = deferred<ScheduledTestResult[]>()
    const current = deferred<ScheduledTestResult[]>()
    mocks.listByAccount.mockResolvedValue([plan(1, 'model')])
    mocks.listResults.mockReturnValueOnce(old.promise).mockReturnValueOnce(current.promise)
    const wrapper = await open()
    await flushPromises()

    await wrapper.get('.cursor-pointer.px-4').trigger('click')
    await wrapper.setProps({ show: false })
    await wrapper.setProps({ show: true })
    await flushPromises()
    await wrapper.get('.cursor-pointer.px-4').trigger('click')
    old.resolve([result(1, 111)])
    await flushPromises()

    expect(wrapper.text()).not.toContain('111ms')
    expect(wrapper.text()).toContain('common.loading')

    current.resolve([result(2, 222)])
    await flushPromises()

    expect(wrapper.text()).toContain('222ms')
  })

  it('ignores a plan click that lands while the panel is closing', async () => {
    mocks.listByAccount.mockResolvedValue([plan(1, 'model')])
    const wrapper = await open()
    await flushPromises()
    const planRow = wrapper.get('.cursor-pointer.px-4').element

    await wrapper.setProps({ show: false, accountId: null })
    planRow.dispatchEvent(new MouseEvent('click'))
    await flushPromises()

    expect(mocks.listResults).not.toHaveBeenCalled()
  })
})
