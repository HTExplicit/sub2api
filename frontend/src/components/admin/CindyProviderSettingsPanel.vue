<template>
  <section class="space-y-3" aria-labelledby="cindy-provider-settings-title" data-testid="cindy-provider-settings">
    <h3 id="cindy-provider-settings-title" class="text-base font-semibold text-ink">{{ localLabel('title') }}</h3>
    <p class="text-sm text-muted">{{ localLabel('description') }}</p>
    <p v-if="loadError" role="alert" class="text-sm text-red-600">{{ localLabel('loadFailed') }}</p>
    <template v-if="form">
      <label v-for="key in fields" :key="key" class="flex items-center gap-2 text-sm text-ink">
        <input v-model="form[key]" type="checkbox" :disabled="!available" :data-testid="`cindy-provider-${key}`" />
        {{ localLabel(key) }}
      </label>
      <button type="button" class="btn btn-secondary" :disabled="saving || !available" data-testid="cindy-provider-save" @click="save">{{ localLabel('save') }}</button>
      <p v-if="status" role="status" class="text-sm text-muted">{{ localLabel(status) }}</p>
    </template>
    <input v-model="search" type="search" class="input" :aria-label="localLabel('search')" :placeholder="localLabel('search')" />
    <p v-if="catalogError" role="alert" class="text-sm text-red-600">{{ localLabel('catalogFailed') }}</p>
    <p v-else class="text-sm text-muted">{{ localLabel('matches', { count: filtered.length }) }}</p>
    <div class="grid gap-2 sm:grid-cols-2">
      <article v-for="entry in filtered.slice(0, 40)" :key="entry.public_id || entry.id || entry.model_id" class="border border-line bg-raised p-3">
        <h4 class="break-all text-sm font-medium text-ink">{{ entry.public_id || entry.id || entry.model_id }}</h4>
        <p class="mt-1 break-all text-xs text-muted">{{ entry.live_upstream_id || entry.upstream_model }} · {{ (entry.endpoints || []).join(', ') }}</p>
      </article>
    </div>
  </section>
  <TotpStepUpDialog :controller="stepUp" />
</template>

<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { getCindyProviderCatalog, getCindyProviderSettings, updateCindyProviderSettings, type CindyProviderCatalogEntry, type CindyProviderSettings } from '@/api/admin/cindyProvider'
import { useCindyAdminScope } from '@/features/cindy/nativeState'
import { useStepUp, isStepUpCancelled, isStepUpBlocked, stepUpBlockReason, StepUpCancelledError } from '@/composables/useStepUp'
import TotpStepUpDialog from '@/components/auth/TotpStepUpDialog.vue'

const { t: localLabel } = useI18n({ useScope: 'local', messages: {
  zh: { title: 'Cindy 目录与策略', description: '目录提供精确模型名称、协议能力与计费参考。更改设置不清除已有账号健康记录。',
    balance_detection: '余额不足与临时健康状态识别', catalog_enabled: '完整模型目录与价格参考', search_enabled: 'Cindy 搜索',
    save: '保存设置', saved: '设置已保存', unsaved: '设置已保存；后续修改仍未保存', saveFailed: '保存失败', loadFailed: '设置加载失败',
    search: '搜索模型', matches: '{count} 项匹配，显示前 40 项。', catalogFailed: '目录不可用' },
  en: { title: 'Cindy catalog and policies', description: 'The catalog supplies exact model identities, protocol capabilities and pricing references. Settings changes preserve existing account health records.',
    balance_detection: 'Detect exhausted balance and transient health states', catalog_enabled: 'Full model catalog and pricing references', search_enabled: 'Cindy search',
    save: 'Save settings', saved: 'Settings saved', unsaved: 'Settings saved; later edits are still unsaved', saveFailed: 'Save failed', loadFailed: 'Settings could not be loaded',
    search: 'Search models', matches: '{count} matches; showing the first 40.', catalogFailed: 'Catalog unavailable' }
} })
const { available, actorID } = useCindyAdminScope()
const stepUp = useStepUp()
const fields = ['balance_detection', 'catalog_enabled', 'search_enabled'] as const
const form = ref<CindyProviderSettings | null>(null)
const entries = ref<CindyProviderCatalogEntry[]>([])
const search = ref(''), status = ref('')
const saving = ref(false), loadError = ref(false), catalogError = ref(false)
const filtered = computed(() => entries.value.filter(entry => JSON.stringify(entry).toLowerCase().includes(search.value.trim().toLowerCase())))
const controller = new AbortController()
let disposed = false, catalogRevision = 0

async function refreshCatalog() {
  if (disposed || !available.value) return
  const revision = ++catalogRevision, actor = actorID.value
  try {
    const next = await getCindyProviderCatalog(controller.signal)
    if (disposed || revision !== catalogRevision || actor !== actorID.value) return
    entries.value = next
    catalogError.value = false
  } catch {
    if (!disposed && revision === catalogRevision && actor === actorID.value) catalogError.value = true
  }
}

async function save() {
  if (!form.value || !available.value || saving.value || disposed) return
  const submitted = { ...form.value }, actor = actorID.value
  saving.value = true
  status.value = ''
  try {
    const result = await stepUp.run(() => {
      if (disposed || actor !== actorID.value || !available.value) throw new StepUpCancelledError()
      return updateCindyProviderSettings(submitted)
    })
    if (disposed || actor !== actorID.value || !form.value) return
    let dirty = false
    for (const key of fields) {
      if (form.value[key] === submitted[key]) form.value[key] = result[key]
      else dirty = true
    }
    status.value = dirty ? 'unsaved' : 'saved'
    await refreshCatalog()
  } catch (error) {
    if (!disposed && actor === actorID.value && !isStepUpCancelled(error)) {
      status.value = isStepUpBlocked(error)
        ? stepUpBlockReason(error) === 'STEP_UP_ADMIN_API_KEY_FORBIDDEN' ? 'stepUp.adminApiKeyForbidden' : 'stepUp.notEnabled'
        : 'saveFailed'
    }
  } finally { if (!disposed) saving.value = false }
}

onMounted(async () => {
  const actor = actorID.value
  try {
    const result = await getCindyProviderSettings(controller.signal)
    if (disposed || actor !== actorID.value) return
    form.value = result
    await refreshCatalog()
  } catch { if (!disposed && actor === actorID.value) loadError.value = true }
})
onBeforeUnmount(() => { disposed = true; catalogRevision++; controller.abort(); stepUp.onCancel() })
</script>
