import { effectScope, reactive } from 'vue'
import { createPinia, setActivePinia } from 'pinia'
import { describe, it, expect, vi } from 'vitest'
const holder = vi.hoisted(() => ({ auth: null as unknown }))
vi.mock('@/stores/auth', () => ({ useAuthStore: () => holder.auth }))
import { useAccountTestPrompt } from '../useAccountTestPrompt'
import { pluginPreferenceKey, writeBrowserPreference } from '@/components/plugins/preferences'
describe('account test prompt memory', () => {
  it('shares text drafts between single/batch modals and isolates administrator identity', () => {
    setActivePinia(createPinia()); localStorage.clear()
    const auth = reactive({ user: { id: 9123 } }); holder.auth = auth
    const scope = effectScope()
    scope.run(() => {
      const single = useAccountTestPrompt(), batch = useAccountTestPrompt()
      single.prompt.value = '  测试原文\n'
      expect(batch.prompt.value).toBe(single.prompt.value)
      expect(localStorage.getItem(`account-test-text:${location.origin}:9123`)).toBe('  测试原文\n')
      writeBrowserPreference(pluginPreferenceKey(location.origin, 9123, 'codexrip.account-tools', 'test-prompt'), 'independent plugin draft')
      expect(single.prompt.value).toBe('independent plugin draft')
      single.prompt.value = '  测试原文\n'
      auth.user = { id: 9124 }
      expect(single.prompt.value).toBe('')
      batch.prompt.value = 'another admin'
      auth.user = { id: 9123 }
      expect(single.prompt.value).toBe('  测试原文\n')
      single.prompt.value = '😀'.repeat(8192)
      expect(single.valid.value).toBe(true)
      single.prompt.value += 'a'; expect(single.valid.value).toBe(false)
      single.reset(); expect(batch.prompt.value).toBe('')
    })
    scope.stop()
  })
})
