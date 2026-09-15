<template>
  <BaseDialog :show="show" :title="t('admin.accounts.batchTest.title')" width="wide" @close="emit('close')">
    <form id="batch-test-accounts" @submit.prevent="submit">
      <p class="mb-4 text-sm text-gray-600 dark:text-gray-300">{{ t('admin.accounts.batchTest.description', { count: rows.length }) }}</p>
      <p v-if="applicationResult" class="mb-3 text-sm text-blue-600" role="status">{{ applicationResult }}</p>
      <div class="space-y-3">
        <div v-for="row in visibleRows" :key="row.account_id" :data-account-id="row.account_id" class="rounded-none border border-gray-200 p-3 dark:border-dark-600">
          <div class="mb-2 flex items-center justify-between gap-3">
            <span class="min-w-0 truncate font-medium">{{ row.name || `#${row.account_id}` }} <span class="text-xs text-gray-500">{{ row.platform }}</span></span>
            <button type="button" class="btn btn-secondary" :disabled="busy" @click="remove(row.account_id)">{{ t('admin.accounts.batchTest.remove') }}</button>
          </div>
          <label :for="`batch-model-${row.account_id}`" class="sr-only">{{ t('admin.accounts.selectTestModel') }} {{ row.name }}</label>
          <AccountTestModelSelect :id="`batch-model-${row.account_id}`" v-model="row.model" :models="row.models" value-key="id" label-key="display_name"
            :disabled="busy || row.loading || !!row.error_code" :placeholder="row.loading ? t('common.loading') : t('admin.accounts.selectTestModel')" />
          <div class="mt-2 flex flex-wrap items-center gap-2">
            <template v-if="!row.loading && (row.error_code || !row.models.length)">
              <span class="text-sm text-red-600" role="alert">{{ t(row.error_code ? 'admin.accounts.batchTest.loadFailed' : 'admin.accounts.batchTest.emptyModels') }}</span>
              <button type="button" class="btn btn-secondary" :disabled="busy" @click="load([row.account_id], generation)">{{ t('admin.accounts.batchTest.retry') }}</button>
            </template>
            <button v-else type="button" class="btn btn-secondary" :disabled="busy || pending || !row.model" @click="applyModel(row.model)">{{ t('admin.accounts.batchTest.apply') }}</button>
          </div>
        </div>
      </div>
      <div v-if="pages > 1" class="mt-4 flex items-center justify-between">
        <button type="button" class="btn btn-secondary" :disabled="page <= 1" @click="page--">{{ t('admin.accounts.batchTest.previous') }}</button>
        <span>{{ page }} / {{ pages }}</span>
        <button type="button" class="btn btn-secondary" :disabled="page >= pages" @click="page++">{{ t('admin.accounts.batchTest.next') }}</button>
      </div>
      <p v-if="!ready" class="mt-3 text-sm text-gray-500">{{ t('admin.accounts.batchTest.resolveBeforeStart') }}</p>
    </form>
    <template #footer>
      <button type="button" class="btn btn-secondary" :disabled="busy" @click="emit('close')">{{ t('common.cancel') }}</button>
      <button type="submit" form="batch-test-accounts" class="btn btn-primary" :disabled="busy || !ready">{{ t(busy ? 'common.submitting' : 'admin.accounts.batchTest.start') }}</button>
    </template>
  </BaseDialog>
</template>

<script setup lang="ts">
import { computed, onBeforeUnmount, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import BaseDialog from '@/components/common/BaseDialog.vue'
import AccountTestModelSelect from './AccountTestModelSelect.vue'
import accountJobsAPI, { type AccountJob, type BatchTestModelRow } from '@/api/admin/accountJobs'
import { prepareAccountTestModels, accountTestModelsForMode, defaultAccountTestModel } from '@/utils/accountTestModels'
import { useAppStore } from '@/stores/app'

const props = defineProps<{ show: boolean; accountIds: number[] }>()
const emit = defineEmits<{ close: []; submitted: [job: AccountJob] }>()
const { t } = useI18n()
type Row = BatchTestModelRow & { model: string; loading: boolean }
const rows = ref<Row[]>([])
const busy = ref(false)
const page = ref(1)
const applicationResult = ref('')
let generation = 0
let controller = new AbortController()
const pages = computed(() => Math.max(1, Math.ceil(rows.value.length / 100)))
const visibleRows = computed(() => rows.value.slice((page.value - 1) * 100, page.value * 100))
const pending = computed(() => rows.value.some(row => row.loading))
const ready = computed(() => rows.value.length > 0 && rows.value.every(row => !row.loading && !row.error_code && row.models.some(model => model.id === row.model)))

async function load(ids: number[], version: number) {
  const idSet = new Set(ids)
  for (const row of rows.value) if (idSet.has(row.account_id)) row.loading = true
  try {
    const result = await accountJobsAPI.batchTestModels(ids, controller.signal)
    if (version !== generation) return
    const byID = new Map(result.map(row => [row.account_id, row]))
    for (const row of rows.value) {
      if (!idSet.has(row.account_id)) continue
      const loaded = byID.get(row.account_id)
      if (!loaded) { row.error_code = 'catalog_missing'; row.loading = false; continue }
      const models = accountTestModelsForMode(loaded, prepareAccountTestModels(loaded, loaded.models || []))
      const model = models.some(m => m.id === row.model) ? row.model : defaultAccountTestModel(loaded, models)
      Object.assign(row, loaded, { models, model, loading: false, error_code: loaded.error_code })
    }
  } catch {
    if (version !== generation) return
    for (const row of rows.value) if (idSet.has(row.account_id)) { row.loading = false; row.error_code = 'catalog_failed' }
  }
}

watch(() => props.show, async show => {
  const version = ++generation
  controller.abort()
  controller = new AbortController()
  if (!show) return
  page.value = 1
  applicationResult.value = ''
  const ids = [...new Set(props.accountIds)]
  rows.value = ids.map(account_id => ({ account_id, name: '', platform: '', type: '', is_cindy: false, models: [], model: '', loading: true }))
  // One batch request at a time; the server bounds upstream discovery.
  for (let offset = 0; offset < ids.length && version === generation; offset += 100) await load(ids.slice(offset, offset + 100), version)
}, { immediate: true })
onBeforeUnmount(() => { generation++; controller.abort() })

function remove(id: number) {
  rows.value = rows.value.filter(row => row.account_id !== id)
  page.value = Math.min(page.value, pages.value)
}
function applyModel(model: string) {
  let applied = 0
  for (const row of rows.value) {
    if (!row.error_code && row.models.some(candidate => candidate.id === model)) { row.model = model; applied++ }
  }
  applicationResult.value = t('admin.accounts.batchTest.applied', { applied, skipped: rows.value.length - applied })
}
async function submit() {
  if (busy.value || !ready.value) return
  const items = rows.value.map(row => ({ account_id: row.account_id, model_id: row.model }))
  busy.value = true
  try {
    emit('submitted', await accountJobsAPI.batchTest(items))
    emit('close')
  } catch { useAppStore().showError(t('admin.accounts.batchTest.submitFailed')) }
  finally { busy.value = false }
}
</script>
