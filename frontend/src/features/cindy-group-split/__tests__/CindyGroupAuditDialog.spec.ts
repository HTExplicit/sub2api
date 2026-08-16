import { enableAutoUnmount, flushPromises, mount } from '@vue/test-utils'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import CindyGroupAuditDialog from '../CindyGroupAuditDialog.vue'

enableAutoUnmount(afterEach)

const mocks = vi.hoisted(() => ({
  auditCindyGroups: vi.fn(),
  getGroupApiKeys: vi.fn(),
  previewCindyGroupSplit: vi.fn(),
  splitCindyGroup: vi.fn(),
  showError: vi.fn(),
  showSuccess: vi.fn(),
}))

vi.mock('@/api/admin', () => ({
  adminAPI: {
    groups: {
      auditCindyGroups: mocks.auditCindyGroups,
      getGroupApiKeys: mocks.getGroupApiKeys,
      previewCindyGroupSplit: mocks.previewCindyGroupSplit,
      splitCindyGroup: mocks.splitCindyGroup,
    },
  },
}))

vi.mock('@/stores/app', () => ({
  useAppStore: () => ({ showError: mocks.showError, showSuccess: mocks.showSuccess }),
}))

vi.mock('vue-i18n', async () => {
  const actual = await vi.importActual<typeof import('vue-i18n')>('vue-i18n')
  return {
    ...actual,
    useI18n: () => ({
      t: (key: string, values?: Record<string, string | number>) => values
        ? `${key}:${Object.values(values).join('/')}`
        : key,
    }),
  }
})

const mixedGroup = {
  group_id: 7,
  group_name: 'Mixed OpenAI',
  status: 'active',
  classification: 'mixed',
  cindy_account_count: 2,
  ordinary_account_count: 3,
  api_key_count: 2,
}

const audit = {
  summary: { pure_cindy_groups: 1, mixed_groups: 1, no_cindy_groups: 0 },
  groups: [
    mixedGroup,
    {
      ...mixedGroup,
      group_id: 8,
      group_name: 'Cindy only',
      classification: 'pure_cindy',
      ordinary_account_count: 0,
    },
  ],
}

const preview = {
  source_group_id: 7,
  source_group_name: 'Mixed OpenAI',
  source_keeps: 'cindy',
  target_name: 'Ordinary target',
  target_classification: 'no_cindy',
  member_fingerprint: 'a'.repeat(64),
  cindy_account_count: 2,
  ordinary_account_count: 3,
  accounts_to_move: 3,
  source_api_key_count: 2,
  api_keys_to_rebind: 1,
  api_keys_remaining: 1,
}

const keyBase = {
  user_id: 4,
  group_id: 7,
  status: 'active',
  ip_whitelist: [],
  ip_blacklist: [],
  last_used_at: null,
  last_used_ip: null,
  quota: 0,
  quota_used: 0,
  expires_at: null,
  created_at: '',
  updated_at: '',
  current_concurrency: 0,
  rate_limit_5h: 0,
  rate_limit_1d: 0,
  rate_limit_7d: 0,
  usage_5h: 0,
  usage_1d: 0,
  usage_7d: 0,
  window_5h_start: null,
  window_1d_start: null,
  window_7d_start: null,
  reset_5h_at: null,
  reset_1d_at: null,
  reset_7d_at: null,
}

function render() {
  return mount(CindyGroupAuditDialog, {
    props: { show: true },
    global: {
      stubs: {
        BaseDialog: {
          props: ['show', 'title'],
          emits: ['close'],
          template: '<div v-if="show"><slot /><slot name="footer" /></div>',
        },
        Icon: true,
      },
    },
  })
}

describe('CindyGroupAuditDialog', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    mocks.auditCindyGroups.mockResolvedValue(audit)
    mocks.getGroupApiKeys
      .mockResolvedValueOnce({
        items: [{ ...keyBase, id: 19, name: 'First', key: 'sk-first-secret' }],
        total: 2,
        page: 1,
        page_size: 100,
        pages: 2,
      })
      .mockResolvedValueOnce({
        items: [{ ...keyBase, id: 20, name: 'Second', key: 'sk-second-secret' }],
        total: 2,
        page: 2,
        page_size: 100,
        pages: 2,
      })
    mocks.previewCindyGroupSplit.mockResolvedValue(preview)
    mocks.splitCindyGroup.mockResolvedValue({ ...preview, target_group_id: 12 })
  })

  it('offers the split wizard only for mixed groups', async () => {
    const wrapper = render()
    await flushPromises()

    expect(wrapper.get('[data-test="cindy-group-split-7"]').exists()).toBe(true)
    expect(wrapper.find('[data-test="cindy-group-split-8"]').exists()).toBe(false)
    expect(wrapper.get('[data-test="cindy-group-audit-summary"]').text()).toContain('1')
  })

  it('loads every Key page, defaults to no selection, invalidates changed previews, and commits the fingerprint', async () => {
    const wrapper = render()
    await flushPromises()

    await wrapper.get('[data-test="cindy-group-split-7"]').trigger('click')
    await flushPromises()

    expect(mocks.getGroupApiKeys).toHaveBeenNthCalledWith(1, 7, 1, 100)
    expect(mocks.getGroupApiKeys).toHaveBeenNthCalledWith(2, 7, 2, 100)
    const firstKey = wrapper.get<HTMLInputElement>('[data-test="cindy-group-api-key-19"]')
    const secondKey = wrapper.get<HTMLInputElement>('[data-test="cindy-group-api-key-20"]')
    expect(firstKey.element.checked).toBe(false)
    expect(secondKey.element.checked).toBe(false)

    await wrapper.get('[data-test="cindy-group-target-name"]').setValue('Ordinary target')
    await firstKey.setValue(true)
    await wrapper.get('[data-test="cindy-group-split-preview-button"]').trigger('click')
    await flushPromises()

    expect(mocks.previewCindyGroupSplit).toHaveBeenCalledWith(7, {
      source_keeps: 'cindy',
      target_name: 'Ordinary target',
      api_key_ids: [19],
    })
    expect(wrapper.get('[data-test="cindy-group-split-preview"]').text()).toContain('aaaaaaaaaa...aaaaaa')

    await wrapper.get('[data-test="cindy-group-target-name"]').setValue('Renamed target')
    expect(wrapper.find('[data-test="cindy-group-split-preview"]').exists()).toBe(false)
    expect(wrapper.get('[data-test="cindy-group-split-submit"]').attributes('disabled')).toBeDefined()

    mocks.previewCindyGroupSplit.mockResolvedValueOnce({ ...preview, target_name: 'Renamed target' })
    await wrapper.get('[data-test="cindy-group-split-preview-button"]').trigger('click')
    await flushPromises()
    await wrapper.get('[data-test="cindy-group-split-submit"]').trigger('click')
    await flushPromises()

    expect(mocks.splitCindyGroup).toHaveBeenCalledWith(7, {
      source_keeps: 'cindy',
      target_name: 'Renamed target',
      api_key_ids: [19],
      member_fingerprint: preview.member_fingerprint,
    })
    expect(mocks.auditCindyGroups).toHaveBeenCalledTimes(2)
    expect(wrapper.emitted('split')).toHaveLength(1)
  })

  it('turns a 409 into a required re-preview without retrying', async () => {
    mocks.splitCindyGroup.mockRejectedValue({ status: 409, code: 'CINDY_GROUP_SPLIT_DRIFT' })
    const wrapper = render()
    await flushPromises()

    await wrapper.get('[data-test="cindy-group-split-7"]').trigger('click')
    await flushPromises()
    await wrapper.get('[data-test="cindy-group-target-name"]').setValue('Ordinary target')
    await wrapper.get('[data-test="cindy-group-split-preview-button"]').trigger('click')
    await flushPromises()
    await wrapper.get('[data-test="cindy-group-split-submit"]').trigger('click')
    await flushPromises()

    expect(mocks.splitCindyGroup).toHaveBeenCalledTimes(1)
    expect(mocks.previewCindyGroupSplit).toHaveBeenCalledTimes(1)
    expect(wrapper.get('[data-test="cindy-group-split-drift"]').exists()).toBe(true)
    expect(wrapper.find('[data-test="cindy-group-split-preview"]').exists()).toBe(false)
  })
})
