import { computed, shallowRef, watch } from 'vue'
import type { PluginContribution } from '@/api/admin/plugins'
import { usePluginExtensions } from '@/stores/pluginExtensions'
import { resolveContribution, type ContributionRef } from './accountView'

/** Keep mounted input/results, but never keep an old admission grant. */
export function useRetainedContribution(reference: () => ContributionRef) {
  const registry = usePluginExtensions()
  const last = shallowRef<PluginContribution>()
  const identity = computed(() => JSON.stringify([registry.actorID, reference().pluginKey, reference().pluginId, reference().id, reference().slot]))
  watch(identity, () => { last.value = undefined }, { flush: 'sync' })
  const current = computed(() => resolveContribution(registry.items, reference()))
  watch(current, item => {
    if (item && (!last.value || (last.value.plugin_id === item.plugin_id && last.value.package_sha256 === item.package_sha256))) last.value = item
  }, { immediate: true, flush: 'sync' })
  const contribution = computed(() => {
    const previous = last.value
    if (!previous) return undefined
    const next = current.value
    if (next?.plugin_id === previous.plugin_id && next.package_sha256 === previous.package_sha256) return next
    return { ...previous, available: false, reason: next ? 'plugin_package_changed' : 'plugin_unavailable' }
  })
  return { contribution, current }
}
