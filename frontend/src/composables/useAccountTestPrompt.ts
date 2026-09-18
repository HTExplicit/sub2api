import { computed, ref, watch, type Ref } from 'vue'
import { getActivePinia } from 'pinia'
import { useAuthStore } from '@/stores/auth'

export const ACCOUNT_TEST_PROMPT_LIMIT = 8192
const drafts = new Map<string, Ref<string>>()

export function useAccountTestPrompt() {
  const auth = getActivePinia() ? useAuthStore() : undefined
  const key = computed(() => auth?.user?.id ? `account-test-text:${location.origin}:${auth.user.id}` : '')
  const current = ref('')
  let selected: Ref<string> = ref('')
  watch(key, value => {
    if (!value) { selected = ref(''); current.value = ''; return }
    if (!drafts.has(value)) {
      let stored = ''
      try { stored = localStorage.getItem(value) || '' } catch { /* Storage can be disabled. */ }
      drafts.set(value, ref(stored))
    }
    selected = drafts.get(value)!
    current.value = value
  }, { immediate: true, flush: 'sync' })
  const prompt = computed({
    get: () => { void current.value; return selected.value },
    set: value => {
      selected.value = value
      if (!key.value) return
      try { localStorage.setItem(key.value, value) } catch { /* Storage can be disabled. */ }
    }
  })
  const length = computed(() => Array.from(prompt.value).length)
  const valid = computed(() => length.value <= ACCOUNT_TEST_PROMPT_LIMIT)
  return { prompt, length, valid, reset: () => { prompt.value = '' } }
}
