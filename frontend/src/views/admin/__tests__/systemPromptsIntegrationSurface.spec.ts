import { readFileSync } from 'node:fs'
import { dirname, resolve } from 'node:path'
import { fileURLToPath } from 'node:url'
import { describe, expect, it } from 'vitest'
import en from '@/i18n/locales/en'
import zh from '@/i18n/locales/zh'

const here = dirname(fileURLToPath(import.meta.url))
const read = (path: string) => readFileSync(resolve(here, path), 'utf8')

describe('System Prompts integration surface', () => {
  it('registers an independently guarded admin route and sidebar entry', () => {
    const router = read('../../../router/index.ts')
    const start = router.indexOf("path: '/admin/system-prompts'")
    expect(start).toBeGreaterThan(0)
    const route = router.slice(start, router.indexOf("path: '/admin/risk-control'", start))
    expect(route).toContain('requiresAuth: true')
    expect(route).toContain('requiresAdmin: true')
    expect(route).toContain("title: '系统提示词'")
    expect(route).not.toContain('titleKey:')

    const sidebar = read('../../../components/layout/AppSidebar.vue')
    expect(sidebar).toContain("path: '/admin/system-prompts'")
    expect(sidebar).toContain("t('nav.systemPrompts')")
  })

  it('keeps the page Chinese under the English locale and removes legacy surfaces', () => {
    expect(en.admin.systemPrompts).toEqual(zh.admin.systemPrompts)
    const view = read('../SystemPromptsView.vue')
    for (const marker of ['saveVersion', 'setCurrent', 'rollback', 'syncManagedSource', 'SystemPromptAdvancedDrawer']) {
      expect(view).toContain(marker)
    }
    expect(view).not.toContain('previewMerge')
    expect(view).not.toContain('previewUpstream')
    expect(view).not.toContain('DOMPurify')
    expect(view).not.toContain('isLegacyComposition')
    expect(view).not.toContain('copyInstallCommand')

    const drawer = read('../../../components/admin/systemPrompt/SystemPromptAdvancedDrawer.vue')
    expect(drawer).toContain('compact_enabled')
    expect(drawer).toContain('expose_server_prompt')
    expect(drawer).toContain('data-test="system-prompt-advanced-drawer"')
    expect(drawer).toContain('data-test="system-prompt-skill-source"')
    expect(drawer).not.toContain('system-prompt-copy-acquire')
    expect(drawer).not.toContain('system-prompt-copy-execute')
  })
})
