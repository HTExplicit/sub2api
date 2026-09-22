import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { defineComponent, nextTick, reactive, ref } from 'vue'
import { flushPromises, mount, type VueWrapper } from '@vue/test-utils'
import type { Account, UpdateAccountRequest } from '@/types'
import { editAccount, editCatalog, editContext, editContribution, editInput } from '@/__tests__/fixtures/accountEdit'
import { contributionAccountBucket } from '@/components/plugins/contributionAdmission'
import { useAccountEditProfile, type AccountEditContribution } from '@/composables/useAccountEditProfile'
import { copyAccountEditValue } from '@/utils/accountEditCodec'

let registry: { loaded: boolean; items: AccountEditContribution[]; refresh: ReturnType<typeof vi.fn> }
vi.mock('@/stores/pluginExtensions', () => ({ usePluginExtensions: () => registry }))
const wrappers: VueWrapper[] = []
function harness(initial = editAccount()) {
  const account = ref<Account>(initial), actor = ref(9), active = ref(true), input = ref(editInput(initial))
  const load = vi.fn().mockImplementation(async () => editContext(account.value, registry.items[0]))
  let editor!: ReturnType<typeof useAccountEditProfile>
  const wrapper = mount(defineComponent({ setup() {
    editor = useAccountEditProfile({ active: () => active.value, account: () => account.value, actorID: () => actor.value,
      readInput: () => input.value, writeInput: value => { input.value = value }, loadContext: load })
    return () => null
  } }))
  wrappers.push(wrapper)
  const activate = () => editor.activate(account.value)
  return { account, actor, active, input, load, editor, activate }
}

describe('account.edit profile lifecycle', () => {
  beforeEach(() => { registry = reactive({ loaded: true, items: [editContribution()], refresh: vi.fn() }) })
  afterEach(() => { for (const wrapper of wrappers.splice(0)) wrapper.unmount() })

  it('does not load a provider profile for ordinary OpenAI even at Laxa with is_cindy', async () => {
    const h = harness(editAccount({ platform: 'openai', is_cindy: true }))
    h.activate(); await flushPromises()
    expect(h.editor.required.value).toBe(false)
    expect(h.load).not.toHaveBeenCalled()
    const payload = { notes: 'core' }
    h.editor.prepare(payload)
    expect(payload).toEqual({ notes: 'core' })
  })

  it('admits an enrolled real account in a partial bucket instead of requiring Create 100 percent', async () => {
    const id = [1, 2, 3, 4, 5, 6, 7, 8].find(value => contributionAccountBucket(value)! < 90)!
    registry.items[0].account_scope!.bindings[0]!.rollout_percent = contributionAccountBucket(id)! + 1
    const h = harness(editAccount({ id }))
    h.activate(); await flushPromises()
    expect(h.editor.available.value).toBe(true)
    h.input.value.responses_mode = 'auto'; h.editor.updateInput('responses_mode', 'auto')
    const payload: UpdateAccountRequest = { extra: { openai_ws_force_http: true } }
    h.editor.prepare(payload)
    expect(payload.provider_edit).toMatchObject({ contribution_id: 'cindy-edit', changes: { responses_mode: { op: 'set', value: 'auto' } } })
    expect(payload.extra).toEqual({ openai_ws_force_http: true })
  })

  it('keeps basic and proven no-op edits independent of a missing runtime', async () => {
    registry.items[0].available = false
    const h = harness(editAccount({ extra: { openai_responses_mode: 'auto', openai_compact_mode: null } }))
    h.activate(); await flushPromises()
    h.editor.updateInput('responses_mode', 'auto')
    expect(h.editor.pending.value).toBe(false)
    const payload: UpdateAccountRequest = { notes: 'basic', extra: { ...h.account.value.extra }, credentials: { ...h.account.value.credentials } }
    h.editor.prepare(payload)
    expect(payload).not.toHaveProperty('provider_edit')
    expect(payload.extra).not.toHaveProperty('openai_responses_mode')
    expect(payload.extra).not.toHaveProperty('openai_compact_mode')
    h.input.value.responses_mode = 'force_responses'; h.editor.updateInput('responses_mode', 'force_responses')
    expect(h.editor.canSubmit.value).toBe(false)
    expect(() => h.editor.prepare({ notes: 'mixed' })).toThrow('Provider edit is unavailable')
    expect(h.input.value.responses_mode).toBe('force_responses')
  })

  it('lets admitted wire edits proceed when the catalog is unavailable, without granting map edits', async () => {
    const h = harness()
    const context = editContext(h.account.value)
    context.catalog = { status: 'unavailable', reason: 'catalog_disabled' }
    h.load.mockResolvedValue(context)
    h.activate(); await flushPromises()
    h.input.value.compact_mode = 'auto'; h.editor.updateInput('compact_mode', 'auto')
    const payload: UpdateAccountRequest = {}
    h.editor.prepare(payload)
    expect(payload.provider_edit?.changes).toEqual({ compact_mode: { op: 'set', value: 'auto' } })
    h.input.value.model_mapping.rows.push({ from: 'custom', to: 'target' }); h.editor.updateInput('model_mapping')
    expect(h.editor.canSubmit.value).toBe(false)
    expect(h.editor.catalogAvailable.value).toBe(false)
  })

  it('partitions only captured stored pairs and retains the displayed catalog after RPC failure', async () => {
    const h = harness(editAccount({ credentials: { model_mapping: { managed: 'wire-managed', custom: 'target' } } }))
    h.activate(); await flushPromises()
    expect(h.editor.draft.value?.preserved).toEqual({ managed: 'wire-managed' })
    expect(h.input.value.model_mapping.rows).toEqual([{ from: 'custom', to: 'target' }])
    h.input.value.model_mapping.rows[0].to = 'draft-target'; h.editor.updateInput('model_mapping')
    h.load.mockRejectedValueOnce(new Error('unavailable'))
    await h.editor.refresh()
    expect(h.editor.catalogAvailable.value).toBe(false)
    expect(h.editor.projection.value.models.map(model => model.id)).toContain('managed')
    expect(h.input.value.model_mapping.rows[0].to).toBe('draft-target')
  })

  it('never partitions dirty live input when the initial catalog response arrives late', async () => {
    const h = harness(editAccount({ credentials: { model_mapping: { managed: 'wire-managed', custom: 'target' } } }))
    let resolve!: (value: ReturnType<typeof editContext>) => void
    h.load.mockReturnValue(new Promise(done => { resolve = done }))
    h.activate()
    h.input.value.model_mapping.rows.push({ from: 'typed-before-response', to: 'draft-target' }); h.editor.updateInput('model_mapping')
    const before = copyAccountEditValue(h.input.value)
    resolve(editContext(h.account.value)); await flushPromises()
    expect(h.input.value.model_mapping).toEqual(before.model_mapping)
    expect(h.editor.draft.value?.catalogNeedsReview).toBe(true)
    expect(h.editor.canSubmit.value).toBe(false)
    expect(h.editor.reconcile()).toBe(true)
    expect(h.input.value.model_mapping).toEqual(before.model_mapping)
    const payload: UpdateAccountRequest = {}
    h.editor.prepare(payload)
    expect(payload.credentials?.model_mapping).toMatchObject({ managed: 'wire-managed', custom: 'target', 'typed-before-response': 'draft-target' })
  })

  it('retains dirty input across disable and same-owner package changes until explicit reconciliation', async () => {
    const h = harness()
    h.activate(); await flushPromises()
    h.input.value.responses_mode = 'force_chat_completions'; h.editor.updateInput('responses_mode', 'force_chat_completions')
    registry.items = [editContribution({ available: false })]
    await flushPromises()
    expect(h.editor.canSubmit.value).toBe(false)
    expect(h.input.value.responses_mode).toBe('force_chat_completions')
    registry.items = [editContribution({ package_sha256: 'c'.repeat(64), edit_definition_digest: 'd'.repeat(64), runtime_generation: 2 })]
    await flushPromises()
    expect(h.editor.draft.value?.pendingContext).toBeDefined()
    expect(h.editor.canSubmit.value).toBe(false)
    expect(h.editor.reconcile()).toBe(true)
    expect(h.input.value.responses_mode).toBe('force_chat_completions')
    const payload: UpdateAccountRequest = {}
    h.editor.prepare(payload)
    expect(payload.provider_edit?.expected_package_sha256).toBe('c'.repeat(64))
  })

  it('does not rebase a dirty draft over changed stored state or transfer it to another owner', async () => {
    const h = harness()
    h.activate(); await flushPromises()
    h.input.value.compact_mode = 'force_on'; h.editor.updateInput('compact_mode', 'force_on')
    h.account.value = { ...h.account.value, extra: { openai_compact_mode: 'force_off' } }
    await h.editor.refresh()
    expect(h.editor.reconcile()).toBe(false)
    expect(h.input.value.compact_mode).toBe('force_on')
    registry.items = [editContribution({ plugin_id: 88, plugin_key: 'other.owner' })]
    await flushPromises()
    expect(h.editor.reconcile()).toBe(false)
    expect(h.editor.contribution.value?.plugin_key).toBe('codexrip.cindy-provider')
  })

  it('isolates account drafts and discards late responses and all old actor state', async () => {
    const h = harness()
    let finishOld!: (value: ReturnType<typeof editContext>) => void
    h.load.mockReturnValueOnce(new Promise(done => { finishOld = done }))
    const old = h.account.value
    h.activate()
    h.input.value.compact_mode = 'force_on'; h.editor.updateInput('compact_mode', 'force_on')
    h.account.value = editAccount({ id: 42 }); h.input.value = editInput(h.account.value); h.activate()
    await flushPromises()
    finishOld(editContext(old)); await flushPromises()
    expect(h.editor.draft.value?.accountID).toBe(42)
    expect(h.input.value.compact_mode).toBe('auto')
    h.account.value = old; h.input.value = editInput(old); h.activate(); await flushPromises()
    expect(h.input.value.compact_mode).toBe('force_on')
    h.actor.value = 10; await nextTick()
    expect(h.editor.draft.value).toBeUndefined()
    expect(h.editor.pending.value).toBe(false)
  })

  it('never turns a 200 empty or mismatched context into catalog deletion authority', async () => {
    const h = harness(editAccount({ credentials: { model_mapping: { custom: 'target' } } }))
    h.load.mockResolvedValue([])
    h.activate(); await flushPromises()
    h.editor.clear('model_mapping')
    expect(h.editor.available.value).toBe(false)
    expect(h.editor.canSubmit.value).toBe(false)
    expect(h.editor.draft.value?.stored.credentials.model_mapping).toEqual({ custom: 'target' })
    const wrong = editContext(editAccount({ id: 99 }))
    wrong.catalog = editCatalog('new')
    h.load.mockResolvedValue(wrong); await h.editor.refresh()
    expect(h.editor.catalogAvailable.value).toBe(false)
    expect(h.editor.projection.value.models).toEqual([])
  })

  it('loads an uninitialized registry when an already-mounted core editor switches to a provider account', async () => {
    registry.loaded = false
    const h = harness(editAccount({ platform: 'openai' }))
    expect(registry.refresh).not.toHaveBeenCalled()
    h.account.value = editAccount(); h.input.value = editInput(h.account.value); h.activate()
    await flushPromises()
    expect(registry.refresh).toHaveBeenCalledTimes(1)
  })

  it.each([undefined, 'f'.repeat(64)])('requires a matching native baseline digest %s without blocking untouched basic edits', async nativeDigest => {
    const h = harness(editAccount({ account_edit_state_sha256: nativeDigest, extra: { openai_responses_mode: 'force_responses' } }))
    h.activate(); await flushPromises()
    expect(h.editor.available.value).toBe(false)
    expect(h.editor.reason.value).toBe('account_edit_state_changed')
    const basic: UpdateAccountRequest = { notes: 'basic', extra: { ...h.account.value.extra } }
    h.editor.prepare(basic)
    expect(basic).not.toHaveProperty('provider_edit')
    h.editor.updateInput('responses_mode', 'force_responses')
    expect(h.editor.pending.value).toBe(true)
    expect(h.editor.canSubmit.value).toBe(false)
    expect(h.editor.reconcile()).toBe(false)
    expect(h.input.value.responses_mode).toBe('force_responses')
  })

  it('does not borrow a new context digest for an older first-open raw mode or mapping baseline', async () => {
    const old = editAccount({ extra: { openai_responses_mode: 'force_responses' }, credentials: { model_mapping: { custom: 'old-target' } } })
    const changed = editAccount({ extra: { openai_responses_mode: 'auto' }, credentials: { model_mapping: { custom: 'new-target' } } })
    const h = harness(old)
    h.load.mockResolvedValue(editContext(changed))
    h.activate(); await flushPromises()
    expect(h.editor.draft.value?.context).toBeUndefined()
    expect(h.editor.draft.value?.stored.credentials.model_mapping).toEqual({ custom: 'old-target' })
    expect(h.input.value.model_mapping.rows).toEqual([{ from: 'custom', to: 'old-target' }])
    h.editor.updateInput('responses_mode', 'force_responses')
    expect(h.editor.changes.value.responses_mode).toEqual({ op: 'set', value: 'force_responses' })
    expect(h.editor.canSubmit.value).toBe(false)
    expect(h.editor.reconcile()).toBe(false)
  })

  it('rejects a late old context after the native same-ID snapshot changes, without rebasing dirty input', async () => {
    const old = editAccount({ extra: { openai_compact_mode: 'force_off' } })
    const h = harness(old)
    let finish!: (value: ReturnType<typeof editContext>) => void
    h.load.mockReturnValueOnce(new Promise(resolve => { finish = resolve }))
    h.activate()
    h.input.value.compact_mode = 'force_on'; h.editor.updateInput('compact_mode', 'force_on')
    h.account.value = editAccount({ id: old.id, extra: { openai_compact_mode: 'auto' } })
    finish(editContext(old)); await flushPromises()
    expect(h.editor.available.value).toBe(false)
    expect(h.editor.draft.value?.context).toBeUndefined()
    expect(h.input.value.compact_mode).toBe('force_on')
    expect(h.editor.draft.value?.stored.extra.openai_compact_mode).toBe('force_off')
    expect(h.editor.reconcile()).toBe(false)
  })
})
