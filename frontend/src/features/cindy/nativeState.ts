import { computed, onScopeDispose, reactive, watch } from 'vue'
import { useAuthStore } from '@/stores/auth'

// Preserve the existing non-secret browser drafts across the native cutover.
// The historical key is a storage namespace, not a plugin runtime dependency.
const drafts = reactive(new Map<string, string>())

export function useCindyAdminScope() {
  const auth = useAuthStore()
  const owner = auth.user?.id
  return {
    actorID: computed(() => auth.user?.id),
    available: computed(() => auth.user?.id === owner && auth.user?.role === 'admin' && Number(auth.user.id) > 0)
  }
}

export function useCindyDraft(name: string) {
  const { actorID, available } = useCindyAdminScope()
  const key = computed(() => `sub2api:plugin-pref:${window.location.origin}:${actorID.value ?? ''}:codexrip.cindy-provider:${name}`)
  watch(key, value => {
    if (!available.value || drafts.has(value)) return
    try { drafts.set(value, localStorage.getItem(value) || '') } catch { drafts.set(value, '') }
  }, { immediate: true, flush: 'sync' })
  const receive = (event: StorageEvent) => {
    if (event.key === key.value) drafts.set(key.value, event.newValue || '')
  }
  window.addEventListener('storage', receive)
  onScopeDispose(() => window.removeEventListener('storage', receive))
  return computed({
    get: () => available.value ? drafts.get(key.value) || '' : '',
    set: (value: string) => {
      if (!available.value || value.length > 64 * 1024) return
      if (value) drafts.set(key.value, value)
      else drafts.delete(key.value)
      try {
        if (value) localStorage.setItem(key.value, value)
        else localStorage.removeItem(key.value)
      } catch { /* A disabled browser store still permits in-memory drafts. */ }
    }
  })
}
