import { afterEach, describe, expect, it, vi } from 'vitest'
import type { PluginContribution } from '@/api/admin/plugins'
import { applyPluginThemeContributions, refreshPluginThemes } from '../theme'
import { publicContributions } from '@/api/admin/plugins'

vi.mock('@/api/admin/plugins', () => ({ publicContributions: vi.fn(async () => []) }))

const contribution = (): PluginContribution => ({ id: 'theme', plugin_id: 7, permission: 'public', slot: 'theme', label: { en: 'Theme' }, available: true, stylesheet_url: `/api/v1/settings/plugins/7/theme/${'a'.repeat(64)}/assets/theme.css` })
afterEach(async () => { await applyPluginThemeContributions([]); vi.clearAllMocks(); vi.unstubAllGlobals() })

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

  it('keeps the loaded theme on replacement failure and switches only after load', async () => {
    const item = contribution()
    const initial = applyPluginThemeContributions([item])
    const previous = document.querySelector<HTMLLinkElement>('link[data-plugin-theme]')!
    previous.dispatchEvent(new Event('load'))
    await initial
    const next = { ...item, stylesheet_url: item.stylesheet_url!.replace('a'.repeat(64), 'b'.repeat(64)) }
    const failed = applyPluginThemeContributions([next])
    const pending = document.querySelectorAll<HTMLLinkElement>('link[data-plugin-theme]')[1]
    expect(previous.isConnected).toBe(true)
    expect(pending.media).toBe('not all')
    pending.dispatchEvent(new Event('error'))
    await failed
    expect(previous.isConnected).toBe(true)
    const updated = applyPluginThemeContributions([next])
    const replacement = document.querySelectorAll<HTMLLinkElement>('link[data-plugin-theme]')[1]
    replacement.dispatchEvent(new Event('load'))
    await updated
    expect(previous.isConnected).toBe(false)
    expect(replacement.media).toBe('')
  })

  it('keeps appearance on a registry network failure but removes an explicitly disabled theme', async () => {
    const item = contribution()
    const initial = applyPluginThemeContributions([item])
    const link = document.querySelector<HTMLLinkElement>('link[data-plugin-theme]')!
    link.dispatchEvent(new Event('load'))
    await initial
    vi.mocked(publicContributions).mockRejectedValueOnce(new Error('offline'))
    await refreshPluginThemes()
    expect(link.isConnected).toBe(true)
    vi.mocked(publicContributions).mockResolvedValueOnce([{ ...item, available: false }])
    await refreshPluginThemes()
    expect(link.isConnected).toBe(false)
  })

  it('does not resurrect a pending stylesheet after it is disabled', async () => {
    const pending = applyPluginThemeContributions([contribution()])
    const link = document.querySelector<HTMLLinkElement>('link[data-plugin-theme]')!
    await applyPluginThemeContributions([])
    link.dispatchEvent(new Event('load'))
    await pending
    expect(document.querySelector('link[data-plugin-theme]')).toBeNull()
  })

  it('retains the old font face until replacement fonts have finished loading', async () => {
    const item = contribution()
    const initial = applyPluginThemeContributions([item])
    const previous = document.querySelector<HTMLLinkElement>('link[data-plugin-theme]')!
    previous.dispatchEvent(new Event('load'))
    await initial
    let loaded!: () => void
    vi.stubGlobal('FontFace', class {
      load() { return new Promise<void>(resolve => { loaded = resolve }) }
    })
    const updated = applyPluginThemeContributions([{ ...item, stylesheet_url: item.stylesheet_url!.replace('a'.repeat(64), 'c'.repeat(64)) }])
    const replacement = document.querySelectorAll<HTMLLinkElement>('link[data-plugin-theme]')[1]
    const style = document.createElement('div').style
    style.setProperty('font-family', 'Fixture')
    style.setProperty('src', 'url("fonts/Fixture.woff2")')
    Object.defineProperty(replacement, 'sheet', { value: { cssRules: [{ type: CSSRule.FONT_FACE_RULE, style }] } })
    replacement.dispatchEvent(new Event('load'))
    await Promise.resolve()
    expect(previous.isConnected).toBe(true)
    expect(replacement.media).toBe('not all')
    loaded()
    await updated
    expect(previous.isConnected).toBe(false)
    expect(replacement.media).toBe('')
  })
})
