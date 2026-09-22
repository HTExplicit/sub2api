import { computed } from 'vue'
import { usePersistentDraft } from '@sub2api/plugin-ui'

export function useAccountTestPrompt() {
  const prompt = usePersistentDraft('test-prompt')
  const length = computed(() => Array.from(prompt.value).length)
  return { prompt, length, valid: computed(() => length.value <= 8192), reset: () => { prompt.value = '' } }
}
