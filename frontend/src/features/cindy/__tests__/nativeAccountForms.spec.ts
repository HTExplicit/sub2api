import { defineComponent, reactive, ref } from 'vue'
import { mount, flushPromises } from '@vue/test-utils'
import { describe, expect, it, vi } from 'vitest'
import { useCindyAccountCreate } from '../useCindyAccountCreate'
import { useCindyAccountEdit } from '../useCindyAccountEdit'
import type { Account, AccountEditContext, UpdateAccountRequest } from '@/types'
import type { AccountEditInput } from '@/utils/accountEditCodec'

describe('native Cindy account forms', () => {
  it('keeps ordinary OpenAI independent and preserves explicit Cindy defaults and device input across platform changes', async () => {
    const platform = ref('openai'), endpoint = ref('https://api.laxarouter.ai')
    const loadCatalog = vi.fn().mockResolvedValue(['public-model'])
    let state!: ReturnType<typeof useCindyAccountCreate>
    const wrapper = mount(defineComponent({ setup() {
      state = useCindyAccountCreate({ platform: () => platform.value, accountType: () => 'apikey', baseURL: () => endpoint.value,
        actorID: () => 7, loadCatalog })
      return () => null
    } }))
    expect(state.required.value).toBe(false)
    expect(loadCatalog).not.toHaveBeenCalled()
    state.activate('cindy')
    platform.value = 'cindy'
    await flushPromises()
    expect(state.definition.value?.defaults).toMatchObject({ concurrency: 3, priority: 50, responses_mode: 'force_responses' })
    state.values.value = { device_id: 'my-device-identity' }
    state.markExplicit('priority')
    platform.value = 'openai'
    await flushPromises()
    expect(state.required.value).toBe(false)
    platform.value = 'cindy'
    state.activate('cindy')
    await flushPromises()
    const request = state.request()
    expect(request.values).toEqual({ device_id: 'my-device-identity' })
    expect(request.inherit_defaults).not.toContain('priority')
    expect(Object.keys(request).sort()).toEqual(['inherit_defaults', 'values'])
    expect(loadCatalog).toHaveBeenCalledTimes(1)
    wrapper.unmount()
  })

  it('preserves absent modes, sends explicit auto, partitions managed mappings and rejects a changed raw-row digest', async () => {
    const account = ref({ id: 11, platform: 'cindy', type: 'apikey', account_edit_state_sha256: 'a'.repeat(64),
      credentials: { base_url: 'https://api.laxarouter.ai', model_mapping: { public: 'upstream', custom: 'custom-upstream' } },
      extra: {} } as Account)
    const input = reactive<AccountEditInput>({ responses_mode: 'auto', compact_mode: 'auto', responses_websocket_mode: 'off',
      model_mapping: { mode: 'mapping', allowed: [], rows: [] }, compact_model_mapping: [] })
    const context = () => ({ schema_version: 1, kind: 'provider', account_id: 11,
      profile: { available: true, native: true }, edit_state_sha256: account.value.account_edit_state_sha256,
      values: { responses_mode: { present: false, recognized: false, effective: 'auto' }, compact_mode: { present: false, recognized: false, effective: 'auto' },
        responses_websocket_mode: { present: false, recognized: false, effective: 'off' } },
      catalog: { status: 'ready', namespace: 'native:catalog-a', models: [{ id: 'public', type: 'model', managed: true, public_model: true, live_upstream_id: 'upstream' }], aliases: {} }
    } as AccountEditContext)
    let state!: ReturnType<typeof useCindyAccountEdit>
    const wrapper = mount(defineComponent({ setup() {
      state = useCindyAccountEdit({ active: () => true, account: () => account.value, actorID: () => 7,
        readInput: () => structuredClone(JSON.parse(JSON.stringify(input))), writeInput: value => Object.assign(input, value),
        loadContext: async () => context() })
      state.activate(account.value)
      return () => null
    } }))
    await flushPromises()
    expect(state.available.value).toBe(true)
    const basic = { extra: { openai_responses_mode: 'auto', openai_compact_mode: 'auto', unrelated: true } } as UpdateAccountRequest
    state.prepare(basic)
    expect(basic.extra).toEqual({ unrelated: true })
    expect(basic.provider_edit).toBeUndefined()
    state.updateInput('responses_mode', 'auto')
    const changed = { extra: {} } as UpdateAccountRequest
    state.prepare(changed)
    expect(changed.provider_edit).toMatchObject({ expected_state_sha256: 'a'.repeat(64), changes: { responses_mode: { op: 'set', value: 'auto' } } })
    expect(changed.provider_edit).not.toHaveProperty('expected_package_sha256')
    state.clear('model_mapping')
    const mappings = { credentials: {} } as UpdateAccountRequest
    state.prepare(mappings)
    expect(state.draft.value?.preserved).toEqual({ public: 'upstream' })
    expect(mappings.provider_edit).toMatchObject({ expected_catalog_namespace: 'native:catalog-a', changes: { model_mapping: { op: 'clear' } } })
    account.value = { ...account.value, account_edit_state_sha256: 'b'.repeat(64) }
    await state.refresh()
    expect(state.canSubmit.value).toBe(false)
    expect(state.draft.value?.input.responses_mode).toBe('auto')
    wrapper.unmount()
  })
})
