import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, mount, type VueWrapper } from '@vue/test-utils'
import { ref } from 'vue'
import CindyAccountControls from '../../../../../plugins/cindy-provider/ui/src/CindyAccountControls.vue'
import CindyCleanupDialog from '../../../../../plugins/cindy-provider/ui/src/CindyCleanupDialog.vue'
import { cindyView } from './accountView.fixtures'
import { viewIdentity } from '../accountView'

const calls = vi.hoisted(() => ({ resource: vi.fn(), availability: vi.fn(), event: vi.fn(), job: vi.fn() }))
const context = ref<Record<string, any>>({}), draft = ref('')
vi.mock('@sub2api/plugin-ui', async () => ({ ...await vi.importActual<typeof import('@sub2api/plugin-ui')>('@sub2api/plugin-ui'),
  resource: calls.resource, resourceAvailability: calls.availability, emitHostEvent: calls.event, openHostJob: calls.job,
  usePluginContext: () => context, usePersistentDraft: () => draft }))
vi.mock('vue-i18n', async () => ({ ...await vi.importActual<typeof import('vue-i18n')>('vue-i18n'), useI18n: () => ({ t: (key: string, args?: { count?: number }) => `${key}${args?.count === undefined ? '' : `:${args.count}`}` }) }))
const mounted: VueWrapper[] = []
beforeEach(() => {
  const view = cindyView()
  context.value = { actor_id: 41, available: true, account_view_state: { identity: viewIdentity(view, 'insufficient'),
    base_query: view.account_view!.base_query, preset: view.account_view!.presets.find(item => item.id === 'insufficient'),
    query: { search: 'narrow search' }, selected_ids: [1], preset_counts: {}, available: true } }
  draft.value = ''
  calls.event.mockReset().mockResolvedValue(undefined); calls.job.mockReset().mockResolvedValue(undefined)
  calls.availability.mockReset().mockResolvedValue(['preview', 'submit'].map(name => ({ name: `cindy.cleanup.insufficient.${name}`, available: true })))
  calls.resource.mockReset().mockImplementation((name: string) => Promise.resolve(name.endsWith('.preview') ? { count: 2, fingerprint: 'f'.repeat(64) } : { id: 72 }))
})
afterEach(() => mounted.splice(0).forEach(wrapper => wrapper.unmount()))
function dialog(kind: 'insufficient' | 'banned' = 'insufficient') {
  const wrapper = mount(CindyCleanupDialog, { props: { show: true, kind, available: true }, attachTo: document.body,
    global: { stubs: { BaseDialog: { props: ['show'], template: '<div v-if="show"><slot /><slot name="footer" /></div>' } } } })
  mounted.push(wrapper); return wrapper
}

describe('plugin-owned Cindy account controls', () => {
  it('requires whole-domain availability rather than silently narrowing cleanup to the visible selection', async () => {
    calls.availability.mockResolvedValue([{ name: 'cindy.cleanup.insufficient.preview', available: false }, { name: 'cindy.cleanup.insufficient.submit', available: false }])
    const wrapper = mount(CindyAccountControls); mounted.push(wrapper)
    await flushPromises()
    expect(wrapper.get('[data-test="delete-cindy-insufficient"]').attributes('disabled')).toBeDefined()
    expect(wrapper.text()).toContain('accountView.fullScopeRequired')
    expect(calls.resource).not.toHaveBeenCalled()
  })
  it.each(['insufficient', 'banned'] as const)('submits %s only with its own accepted server fingerprint and captured operation key', async kind => {
    const wrapper = dialog(kind)
    await flushPromises()
    expect(calls.resource).toHaveBeenCalledTimes(1)
    const previewInput = calls.resource.mock.calls[0][1]
    await wrapper.get('[data-test="cindy-cleanup-submit"]').trigger('click'); await flushPromises()
    const call = calls.resource.mock.calls.find(([name]) => name === `cindy.cleanup.${kind}.submit`)!
    expect(call[1]).toEqual({ operation_key: previewInput.operation_key, body: { expected_count: 2, fingerprint: 'f'.repeat(64) } })
    expect(call[1].body).not.toHaveProperty('filters')
    expect(call[1].body).not.toHaveProperty('account_ids')
    expect(calls.job).toHaveBeenCalledWith(72)
    expect(wrapper.get('[data-test="cindy-cleanup-result"]').text()).toContain('#72')
  })
  it('retains the preview draft on 409 and requires an explicit new preview instead of auto retry', async () => {
    const wrapper = dialog(); await flushPromises()
    calls.resource.mockRejectedValueOnce(Object.assign(new Error('candidate set changed'), { status: 409 }))
    await wrapper.get('[data-test="cindy-cleanup-submit"]').trigger('click'); await flushPromises()
    expect(wrapper.get('[data-test="cindy-cleanup-preview"]').text()).toContain(':2')
    expect(draft.value).toContain('f'.repeat(64))
    expect(wrapper.get('[data-test="cindy-cleanup-submit"]').attributes('disabled')).toBeDefined()
    expect(calls.resource).toHaveBeenCalledTimes(2)
    await wrapper.setProps({ available: false }); await wrapper.setProps({ available: true }); await flushPromises()
    expect(calls.resource).toHaveBeenCalledTimes(2)
    expect(wrapper.get('[data-test="cindy-cleanup-submit"]').attributes('disabled')).toBeDefined()
    await wrapper.get('[data-test="cindy-cleanup-repreview"]').trigger('click'); await flushPromises()
    expect(calls.resource).toHaveBeenCalledTimes(3)
    expect(calls.resource.mock.calls[2][1].operation_key).not.toBe(calls.resource.mock.calls[0][1].operation_key)
  })
  it('does not submit an empty candidate set or auto-submit on mount', async () => {
    calls.resource.mockResolvedValue({ count: 0, fingerprint: '0'.repeat(64) })
    const wrapper = dialog(); await flushPromises()
    expect(calls.resource.mock.calls.every(([name]) => name.endsWith('.preview'))).toBe(true)
    expect(wrapper.get('[data-test="cindy-cleanup-submit"]').attributes('disabled')).toBeDefined()
  })
  it('keeps a submitted result when its dialog is closed and reopened while unavailable', async () => {
    const wrapper = dialog(); await flushPromises()
    await wrapper.get('[data-test="cindy-cleanup-submit"]').trigger('click'); await flushPromises()
    await wrapper.setProps({ show: false, available: false })
    await wrapper.setProps({ show: true })
    await flushPromises()
    expect(wrapper.get('[data-test="cindy-cleanup-result"]').text()).toContain('#72')
    expect(calls.resource).toHaveBeenCalledTimes(2)
    await wrapper.get('[data-test="cindy-cleanup-result"] button').trigger('click')
    expect(calls.job).toHaveBeenLastCalledWith(72)
  })
  it('does not accept a late preview after availability was lost', async () => {
    let finish!: (value: unknown) => void
    calls.resource.mockReturnValueOnce(new Promise(resolve => { finish = resolve }))
    const wrapper = dialog(); await flushPromises()
    await wrapper.setProps({ available: false })
    finish({ count: 3, fingerprint: 'late-preview' }); await flushPromises()
    await wrapper.setProps({ available: true })
    expect(wrapper.find('[data-test="cindy-cleanup-preview"]').exists()).toBe(false)
    expect(wrapper.get('[data-test="cindy-cleanup-submit"]').attributes('disabled')).toBeDefined()
    expect(calls.resource).toHaveBeenCalledTimes(1)
  })
})
