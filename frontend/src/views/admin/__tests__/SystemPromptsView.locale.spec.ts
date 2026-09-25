import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, mount, type VueWrapper } from '@vue/test-utils'
import { createI18n } from 'vue-i18n'
import en from '@/i18n/locales/en/admin/systemPrompts'
import zh from '@/i18n/locales/zh/admin/systemPrompts'
import { promptConfig } from './systemPromptFixtures'

const api = vi.hoisted(() => ({ config: vi.fn(), saveConfig: vi.fn(), preview: vi.fn() }))
const notifications = vi.hoisted(() => ({ showSuccess: vi.fn() }))
vi.mock('@/api/admin/systemPromptRules', async () => ({ ...await vi.importActual<typeof import('@/api/admin/systemPromptRules')>('@/api/admin/systemPromptRules'), rulesAPI: api }))
vi.mock('@/stores/app', () => ({ useAppStore: () => notifications }))
vi.mock('@/stores/auth', () => ({ useAuthStore: () => ({ user: { id: 1 } }) }))
import App from '../SystemPromptsView.vue'

function messages(value: object, prefix = ''): Record<string, string> {
  return Object.fromEntries(Object.entries(value).flatMap(([key, child]) => {
    const path = prefix ? `${prefix}.${key}` : key
    return typeof child === 'string' ? [[path, child]] : Object.entries(messages(child, path))
  }))
}
const wrappers: VueWrapper[] = []
beforeEach(() => {
  sessionStorage.clear()
  Object.values(api).forEach(mock => mock.mockReset())
  const config = promptConfig(); config.policy.rules[0]!.name = 'Keep 用户名称'
  api.config.mockResolvedValue(config)
})
afterEach(() => wrappers.splice(0).forEach(wrapper => wrapper.unmount()))
describe('unified prompt editor site language', () => {
  it('owns complete English and Chinese messages', () => {
    const english = messages(en), chinese = messages(zh)
    expect(Object.keys(english).sort()).toEqual(Object.keys(chinese).sort())
    for (const [key, value] of Object.entries(english)) { expect(value.trim(), key).not.toBe(''); expect(value, key).not.toMatch(/\p{Script=Han}/u) }
    expect(en.systemPrompts.title).toBe('System Prompts')
    expect(zh.systemPrompts.title).toBe('系统提示词')
  })
  it('switches language without resetting content or settings drafts', async () => {
    const i18n = createI18n({ legacy: false, locale: 'en', messages: { en: { admin: en }, zh: { admin: zh } } })
    const wrapper = mount(App, { global: { plugins: [i18n], stubs: { AppLayout: { template: '<div><slot /></div>' } } } }); wrappers.push(wrapper)
    await flushPromises()
    await wrapper.get('[data-test="system-prompt-body"]').setValue('Unsaved 原文\nKeep bytes')
    await wrapper.get('[data-test="prompt-position"]').setValue('after_last_user')
    const editor = wrapper.get('[data-test="system-prompt-body"]').element
    i18n.global.locale.value = 'zh'; await flushPromises()
    expect(wrapper.get('h1').text()).toBe('系统提示词')
    expect(wrapper.get('[data-test="save-rules"]').text()).toBe('保存并生效')
    expect(wrapper.text()).toContain('Keep 用户名称')
    expect(wrapper.get('[data-test="system-prompt-body"]').element).toBe(editor)
    expect(editor).toHaveProperty('value', 'Unsaved 原文\nKeep bytes')
    i18n.global.locale.value = 'en'; await flushPromises()
    expect(wrapper.get('[data-test="save-rules"]').text()).toBe('Save and apply')
    expect(wrapper.get('[data-test="prompt-position"]').element).toHaveProperty('value', 'after_last_user')
    expect(api.config).toHaveBeenCalledTimes(1)
    expect(api.saveConfig).not.toHaveBeenCalled()
    expect(api.preview).not.toHaveBeenCalled()
  })
})
