import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, mount, type VueWrapper } from '@vue/test-utils'
import { createI18n } from 'vue-i18n'
import en from '@/i18n/locales/en/admin/systemPrompts'
import zh from '@/i18n/locales/zh/admin/systemPrompts'

const api = vi.hoisted(() => ({
  list: vi.fn(), get: vi.fn(), create: vi.fn(), saveDraft: vi.fn(), updateMetadata: vi.fn(),
  updateRuntime: vi.fn(), publish: vi.fn(), remove: vi.fn(), duplicate: vi.fn(),
  getSkillRegistry: vi.fn(), startSkillSync: vi.fn(), publishSkillVersion: vi.fn(),
}))
const notifications = vi.hoisted(() => ({ showError: vi.fn(), showSuccess: vi.fn(), showWarning: vi.fn() }))
vi.mock('@/api/admin/systemPrompts', () => ({ default: api }))
vi.mock('@/stores/app', () => ({ useAppStore: () => notifications }))

import App from '../SystemPromptsView.vue'

function messages(value: object, prefix = ''): Record<string, string> {
  return Object.fromEntries(Object.entries(value).flatMap(([key, child]) => {
    const path = prefix ? `${prefix}.${key}` : key
    return typeof child === 'string' ? [[path, child]] : Object.entries(messages(child, path))
  }))
}
const wrappers: VueWrapper[] = []

describe('prompt-skills site language', () => {
  beforeEach(() => {
    Object.values(api).forEach(mock => mock.mockReset())
    Object.values(notifications).forEach(mock => mock.mockReset())
    vi.stubGlobal('fetch', vi.fn(() => { throw new Error('Unexpected network in locale regression') }))
    const template = { id: 5, slug: 'user-template', name: 'Keep 用户名称', description: '', is_seed: false, managed_source: '', created_at: '', updated_at: '' }
    const runtime = { enabled: true, expose_server_prompt: false, compact_enabled: false, revision: 7, template_id: 5, version_id: 50, degraded: false }
    const version = { id: 50, template_id: 5, version: 2, body: 'Original 原文', note: 'Original note', composition_mode: 'inline', is_active: true, created_at: '' }
    api.list.mockResolvedValue({ templates: [template], runtime })
    api.get.mockResolvedValue({ template, runtime, versions: [version] })
  })
  afterEach(() => {
    wrappers.splice(0).forEach(wrapper => wrapper.unmount())
    expect(fetch).not.toHaveBeenCalled()
    vi.unstubAllGlobals()
  })

  it('owns complete distinct English messages instead of aliasing Chinese', () => {
    const english = messages(en)
    const chinese = messages(zh)
    expect(Object.keys(english).sort()).toEqual(Object.keys(chinese).sort())
    for (const [key, value] of Object.entries(english)) {
      expect(value.trim(), key).not.toBe('')
      expect(value, key).not.toMatch(/\p{Script=Han}/u)
    }
    expect(en.systemPrompts.title).toBe('System Prompts')
    expect(zh.systemPrompts.title).toBe('系统提示词')
    expect(en.systemPrompts.confirm.skillPublishMessage).toContain('paired prompt together')
    expect(en.systemPrompts.confirm.deleteMessage).toContain('soft-deleted')
  })

  it('switches rendered language without resetting drafts or publishing data', async () => {
    const i18n = createI18n({ legacy: false, locale: 'en', fallbackLocale: 'en', messages: {
      en: { admin: en, common: { confirm: 'Confirm', cancel: 'Cancel', close: 'Close' } },
      zh: { admin: zh, common: { confirm: '确认', cancel: '取消', close: '关闭' } },
    } })
    const wrapper = mount(App, { global: { plugins: [i18n], stubs: {
      AppLayout: { template: '<div><slot /></div>' },
      Icon: true, Toggle: true, SystemPromptAdvancedDrawer: true,
      BaseDialog: true, ConfirmDialog: true,
    } } })
    wrappers.push(wrapper)
    await flushPromises()
    expect(wrapper.get('h1').text()).toBe('System Prompts')
    expect(wrapper.text()).toContain('Keep 用户名称')
    await wrapper.get('[data-test="system-prompt-body"]').setValue('Unsaved 原文\nKeep bytes')
    const editor = wrapper.get('[data-test="system-prompt-body"]').element
    i18n.global.locale.value = 'zh'
    await flushPromises()
    expect(wrapper.get('h1').text()).toBe('系统提示词')
    expect(wrapper.get('[data-test="system-prompt-save-version"]').text()).toBe('保存为新版本')
    expect(wrapper.get('[data-test="system-prompt-body"]').element).toBe(editor)
    expect(editor).toHaveProperty('value', 'Unsaved 原文\nKeep bytes')
    i18n.global.locale.value = 'en'
    await flushPromises()
    expect(wrapper.get('h1').text()).toBe('System Prompts')
    expect(wrapper.get('[data-test="system-prompt-save-version"]').text()).toBe('Save as a new version')
    expect(editor).toHaveProperty('value', 'Unsaved 原文\nKeep bytes')
    expect(api.list).toHaveBeenCalledTimes(1)
    expect(api.get).toHaveBeenCalledTimes(1)
    for (const [name, mock] of Object.entries(api)) {
      if (name !== 'list' && name !== 'get') expect(mock, name).not.toHaveBeenCalled()
    }
    expect(notifications.showError).not.toHaveBeenCalled()
  })
})
