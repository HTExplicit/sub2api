import { defineStore } from 'pinia'
import { onScopeDispose, ref, watch } from 'vue'
import { contributions, publicContributions, type PluginContribution } from '@/api/admin/plugins'
import { useAuthStore } from './auth'

export const usePluginExtensions = defineStore('pluginExtensions', () => {
  const auth = useAuthStore()
  const items = ref<PluginContribution[]>([])
  const loaded = ref(false)
  let flight: Promise<void> | null = null
  let dirty = false
  let attempted = false

  async function refresh() {
    if (!auth.isAuthenticated) { items.value = []; loaded.value = false; return }
    if (flight) return flight
    dirty = false
    attempted = true
    flight = (async () => {
      try {
        const next = await (auth.isAdmin ? contributions() : publicContributions())
        if (auth.isAuthenticated && !dirty) { items.value = next; loaded.value = true }
      } catch { items.value = items.value.map(item => ({ ...item, available: false, reason: 'plugin_registry_unavailable' })) }
      finally { flight = null; if (dirty) void refresh() }
    })()
    return flight
  }

  const notify = () => { dirty = true; void refresh() }
  window.addEventListener('sub2api:plugins-changed', notify)
  const timer = window.setInterval(() => { if (attempted && auth.isAuthenticated && document.visibilityState === 'visible') void refresh() }, 15000)
  watch(() => [auth.user?.id, auth.isAdmin], () => { items.value = []; loaded.value = false; dirty = true; if (attempted) void refresh() })
  onScopeDispose(() => { window.removeEventListener('sub2api:plugins-changed', notify); window.clearInterval(timer) })
  return { items, loaded, refresh }
})
