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

    const sidebar = read('../../../components/layout/AppSidebar.vue')
    expect(sidebar).toContain("path: '/admin/system-prompts'")
    expect(sidebar).toContain("t('nav.systemPrompts')")
  })

  it('keeps locale trees symmetric and exposes draft, publish, rollback, preview, and runtime controls', () => {
    expect(Object.keys(zh.admin.systemPrompts)).toEqual(Object.keys(en.admin.systemPrompts))
    const view = read('../SystemPromptsView.vue')
    for (const marker of ['saveDraft', 'publish', 'rollback', 'previewMerge', 'previewUpstream', 'compact_enabled', 'expose_server_prompt']) {
      expect(view).toContain(marker)
    }
    expect(view).toContain('DOMPurify.sanitize')
  })
})
