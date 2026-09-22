import { afterEach, describe, expect, it, vi } from 'vitest'
import type { PluginContribution } from '@/api/admin/plugins'
import { applyPluginThemeContributions } from '../theme'

vi.mock('@/api/admin/plugins', () => ({ publicContributions: vi.fn(async () => []) }))

const contribution = (): PluginContribution => ({ id: 'theme', plugin_id: 7, permission: 'public', slot: 'theme', label: { en: 'Theme' }, available: true, stylesheet_url: `/api/v1/settings/plugins/7/theme/${'a'.repeat(64)}/assets/theme.css` })
afterEach(() => { document.querySelectorAll('[data-plugin-theme]').forEach(link => link.remove()) })

describe('plugin stylesheets', () => {
  it('loads a declared theme, keeps the loaded resource, and removes it on failure or disable', async () => {
    const item = contribution()
    const pending = applyPluginThemeContributions([item])
    const link = document.querySelector<HTMLLinkElement>('link[data-plugin-theme]')!
    expect(link.getAttribute('href')).toBe(item.stylesheet_url)
    link.dispatchEvent(new Event('load'))
    await pending
    await applyPluginThemeContributions([item])
    expect(document.querySelector('link[data-plugin-theme]')).toBe(link)
    await applyPluginThemeContributions([{ ...item, available: false }])
    expect(document.querySelector('link[data-plugin-theme]')).toBeNull()
    await applyPluginThemeContributions([])
    expect(document.querySelector('link[data-plugin-theme]')).toBeNull()
  })

  it('rejects external URLs and private scripts even when metadata asks to load them', async () => {
    const item = contribution()
    for (const url of ['https://example.com/theme.css', '/api/v1/plugin-ui/token/index.html', item.stylesheet_url!.replace('theme.css', 'app.js'), item.stylesheet_url!.replace('assets/theme.css', '../theme.css')]) {
      await applyPluginThemeContributions([{ ...item, stylesheet_url: url }])
      expect(document.querySelector('link[data-plugin-theme]')).toBeNull()
    }
  })
})
