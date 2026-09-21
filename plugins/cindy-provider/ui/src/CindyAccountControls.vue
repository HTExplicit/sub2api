<template>
  <div v-if="kind" class="mb-3 flex flex-wrap items-center justify-end gap-2" data-test="cindy-account-controls">
    <span v-if="!canCleanup" role="status" class="text-xs text-muted">{{ t('accountView.fullScopeRequired') }}</span>
    <button type="button" class="btn btn-danger" :disabled="!canCleanup" :data-test="`delete-cindy-${kind}`" @click="openCleanup">
      <Icon name="trash" size="sm" />{{ t(`accountView.${kind}Title`) }}
    </button>
  </div>
  <CindyCleanupDialog v-if="openedKind" :show="showDialog" :kind="openedKind" :available="canCleanupOpened"
    @close="showDialog = false" />
</template>
<script setup lang="ts">
import { computed, onBeforeUnmount, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { Icon, resourceAvailability, usePluginContext, type AccountViewStateV1 } from '@sub2api/plugin-ui'
import CindyCleanupDialog from './CindyCleanupDialog.vue'
import type { CindyCleanupKind } from './api'

const { t } = useI18n()
const context = usePluginContext()
const state = computed(() => context.value.account_view_state as AccountViewStateV1 | undefined)
const kind = computed<CindyCleanupKind | undefined>(() => {
  const preset = state.value?.preset.id
  return preset === 'insufficient' || preset === 'banned' ? preset : undefined
})
const availableResources = ref(new Set<string>())
const openedKind = ref<CindyCleanupKind>()
const showDialog = ref(false)
let generation = 0
const available = computed(() => context.value.available !== false && state.value?.available !== false)
const allows = (value?: CindyCleanupKind) => available.value && !!value &&
  availableResources.value.has(`cindy.cleanup.${value}.preview`) && availableResources.value.has(`cindy.cleanup.${value}.submit`)
const canCleanup = computed(() => allows(kind.value))
const canCleanupOpened = computed(() => allows(openedKind.value))
watch(() => [context.value.actor_id, state.value?.identity.package_sha256, available.value, kind.value], async () => {
  const current = ++generation
  availableResources.value = new Set()
  if (!available.value) return
  try {
    const resources = await resourceAvailability()
    if (current === generation) availableResources.value = new Set(resources.filter(item => item.available).map(item => item.name))
  } catch { /* Deny new cleanup; keep the mounted dialog and its result. */ }
}, { immediate: true })
function openCleanup() {
  if (!canCleanup.value) return
  openedKind.value = kind.value
  showDialog.value = true
}
onBeforeUnmount(() => { generation++ })
</script>
