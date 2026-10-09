<template>
  <CodexLayout>
    <div class="mx-auto max-w-[1664px] space-y-6 px-1" data-ui="codex-gateway-borrow">
      <header class="space-y-1 border-b border-line pb-4">
        <h2 class="text-xl font-semibold">{{ text('线路借用', 'Route borrowing') }}</h2>
        <p class="max-w-4xl text-sm text-muted">{{ text('借用来源账号取得的路由信息，目标账号仍使用自己的凭据。客户端身份模拟在另一页独立管理。', 'Reuse routing information acquired by a source account; targets keep their own credentials. Client identity simulation is managed separately.') }}</p>
      </header>
      <pre v-if="loadError" role="alert" class="borrow-error">{{ loadError }}</pre>
      <p v-if="loading" class="py-8 text-center text-sm text-muted">{{ t('common.loading') }}</p>

      <div v-if="saved" class="flex flex-wrap items-center justify-between gap-3 text-sm text-muted" data-test="borrow-saved-configuration">
        <p>{{ t('admin.codexGatewayBorrow.savedSelection', { sources: saved.source_account_ids.length, targets: saved.target_account_ids.length, models: saved.models.length }) }}</p>
        <button type="button" class="btn btn-secondary btn-sm" :disabled="statusRefreshing" data-test="borrow-refresh" @click="refreshStatus">{{ t('admin.codexGatewayBorrow.refreshStatus') }}</button>
      </div>
      <details v-if="draft" :open="!saved?.enabled || dirty" class="rounded-lg border border-line p-4 sm:p-5" data-test="borrow-configuration-panel">
        <summary class="cursor-pointer font-semibold text-ink">{{ text('配置借用 · 三步完成', 'Configure borrowing · three steps') }}</summary>
        <section class="mt-4 space-y-4" aria-labelledby="borrow-config-title">
        <div class="flex flex-wrap items-center justify-between gap-3">
          <div class="flex flex-wrap items-center gap-2">
            <h2 id="borrow-config-title" class="font-semibold">{{ t('admin.codexGatewayBorrow.configuration') }}</h2>
            <span v-if="dirty" class="rounded bg-amber-50 px-2 py-1 text-xs text-amber-800 dark:bg-amber-950 dark:text-amber-200" data-test="borrow-unsaved">{{ t('admin.codexGatewayBorrow.unsavedChanges') }}</span>
          </div>
          <div class="flex flex-wrap gap-2">
            <button type="button" class="btn btn-secondary btn-sm" :disabled="!dirty || saving" data-test="borrow-reset" @click="resetDraft">{{ t('admin.codexGatewayBorrow.resetDraft') }}</button>

          </div>
        </div>
        <p class="text-xs text-muted">{{ t('admin.codexGatewayBorrow.draftHint') }}</p>
        <label class="flex items-center gap-3">
          <Toggle v-model="draft.enabled" :disabled="saving" :aria-label="t('admin.codexGatewayBorrow.enabled')" data-test="borrow-enabled" />
          <span class="text-sm font-medium">{{ t('admin.codexGatewayBorrow.enabled') }}</span>
        </label>
        <p class="text-sm text-muted">{{ t('admin.codexGatewayBorrow.saveHint') }}</p>

        <div class="grid gap-4 lg:grid-cols-2">
          <fieldset v-for="role in accountRoles" :key="role.key" :disabled="saving" class="min-w-0 space-y-2">
            <legend class="text-sm font-medium">{{ role.key === 'source_account_ids' ? text('1. 选择来源账号', '1. Choose source accounts') : text('2. 选择使用借用的账号', '2. Choose target accounts') }} · {{ draft[role.key].length }}</legend>
            <p class="text-xs text-muted">{{ t(role.hint) }}</p>
            <input v-model="accountSearch[role.key]" type="search" class="input w-full" :aria-label="t('admin.codexGatewayBorrow.searchAccounts')" :placeholder="t('admin.codexGatewayBorrow.searchAccounts')" />
            <div class="max-h-96 overflow-auto rounded-lg border border-line">
              <div v-for="account in filteredAccounts(role.key)" :key="account.id" class="space-y-2 border-b border-line px-3 py-3 last:border-b-0" :data-test="`borrow-account-${role.key}-${account.id}`">
                <label class="flex cursor-pointer items-start gap-3">
                  <input v-model="draft[role.key]" type="checkbox" class="checkbox mt-1 shrink-0" :value="account.id" :disabled="draft[role.other].includes(account.id) || (!!accountRestriction(account, role.key) && !draft[role.key].includes(account.id))" :data-test="`borrow-${role.key}-${account.id}`" />
                  <span class="min-w-0 flex-1 space-y-1">
                    <span class="flex flex-wrap items-center gap-2 text-sm font-medium"><span class="break-words">{{ account.name }} <span class="font-normal text-muted">#{{ account.id }}</span></span><span v-if="draft[role.key].includes(account.id)" class="text-xs text-primary-700 dark:text-primary-300">{{ t('admin.codexGatewayBorrow.selected') }}</span><span v-else-if="draft[role.other].includes(account.id)" class="text-xs font-normal text-muted">{{ t('admin.codexGatewayBorrow.selectedAsOtherRole') }}</span></span>
                    <span class="block text-xs text-muted">{{ accountStateLabel(account) }}</span><span v-if="accountRestriction(account, role.key)" class="block text-xs text-amber-800 dark:text-amber-200">{{ accountRestriction(account, role.key) }}</span>
                    <span class="block break-words text-xs text-muted">{{ t('admin.codexGatewayBorrow.proxy') }}: {{ proxyLabel(account) }}</span>
                  </span>
                </label>
                <details class="ml-7 text-xs" :data-test="`borrow-account-details-${role.key}-${account.id}`">
                  <summary class="cursor-pointer text-muted">{{ t('admin.codexGatewayBorrow.accountDetails') }}</summary>
                  <div class="mt-2 space-y-2">
                    <p v-if="account.parent_account_id" class="break-words text-muted">{{ t('admin.codexGatewayBorrow.shadowOf', { id: account.parent_account_id, name: accountName(account.parent_account_id) }) }}</p>
                    <p class="break-words text-muted">WS: {{ wsLabel(account) }}</p>
                    <p class="break-words text-muted">{{ modelMappingLabel(account) }}</p>
                    <pre v-if="account.error_message" class="borrow-error">{{ account.error_message }}</pre>
                  </div>
                </details>
              </div>
              <p v-if="filteredAccounts(role.key).length === 0" class="p-4 text-sm text-muted">{{ t('admin.codexGatewayBorrow.noAccounts') }}</p>
            </div>
            <div v-if="unknownSelectedAccounts(role.key).length" class="space-y-2 text-xs text-amber-700 dark:text-amber-300">
              <p>{{ t('admin.codexGatewayBorrow.missingAccounts', { ids: unknownSelectedAccounts(role.key).join(', ') }) }}</p>
              <label v-for="id in unknownSelectedAccounts(role.key)" :key="id" class="flex items-center gap-2"><input v-model="draft[role.key]" type="checkbox" class="checkbox" :value="id" />{{ t('admin.codexGatewayBorrow.unknownAccount') }} #{{ id }}</label>
            </div>
          </fieldset>
        </div>
        <fieldset :disabled="saving" class="flex flex-wrap items-center gap-4">
          <legend class="mb-2 text-sm font-medium">{{ t('admin.codexGatewayBorrow.models') }}</legend>
          <label v-for="model in BORROW_MODELS" :key="model" class="flex items-center gap-2 text-sm">
            <input v-model="draft.models" type="checkbox" class="checkbox" :value="model" :data-test="`borrow-config-model-${model}`" />
            <span>{{ model === 'gpt-6-astra' ? 'Astra' : '6.1 Sol' }} <span class="text-xs text-muted">{{ model }}</span></span>
          </label>
        </fieldset>
        <p class="text-xs text-muted">{{ t('admin.codexGatewayBorrow.localInventoryHint') }}</p>
        <p v-if="configError" role="alert" class="text-sm text-red-700 dark:text-red-300">{{ configError }}</p>
            <button type="button" class="btn btn-primary btn-sm" :disabled="loading || !dirty || !!configError || saving || statusRefreshing" data-test="borrow-save" @click="saveConfig">{{ saving ? t('common.saving') : text('3. 保存并检查', '3. Save and check') }}</button>
        <pre v-if="saveError" role="alert" class="borrow-error">{{ saveError }}</pre>
        <div v-if="savedNotice" class="space-y-2 rounded-lg border border-emerald-200 bg-emerald-50 p-3 text-sm dark:border-emerald-900 dark:bg-emerald-950" role="status" data-test="borrow-save-result">
          <p class="font-medium text-emerald-800 dark:text-emerald-200">{{ t('admin.codexGatewayBorrow.saved') }}</p>
          <p v-if="statusRefreshing" class="text-muted">{{ t('admin.codexGatewayBorrow.currentPreparationState') }}: {{ t('common.loading') }}</p>
          <p v-else-if="statusError" class="text-muted">{{ t('admin.codexGatewayBorrow.statusReadFailed') }}</p>
          <p v-else-if="status" class="text-muted">{{ t('admin.codexGatewayBorrow.currentPreparationState') }}: {{ status.preparing ? t('admin.codexGatewayBorrow.preparing') : t('admin.codexGatewayBorrow.preparationIdle') }}</p>
          <RouterLink data-test="borrow-status-link" to="/admin/codex#status" class="inline-block font-medium text-primary-700 underline dark:text-primary-300">{{ t('admin.codexGatewayBorrow.viewBorrowStatus') }}</RouterLink>
        </div>
        </section>
      </details>
      <CodexGatewayBorrowStatusView ref="statusPanel" v-if="saved && !loading" id="status" :key="statusEpoch" embedded />
    </div>
  </CodexLayout>
</template>

<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { RouterLink } from 'vue-router'
import CodexLayout from '@/components/admin/codex/CodexLayout.vue'
import CodexGatewayBorrowStatusView from './CodexGatewayBorrowStatusView.vue'
import Toggle from '@/components/common/Toggle.vue'
import { BORROW_MODELS, codexGatewayBorrowAPI, type CodexGatewayBorrowConfig, type CodexGatewayBorrowStatus } from '@/api/admin/codexGatewayBorrow'
import { useCodexBorrowInventory } from '@/composables/useCodexBorrowUI'
import { useAppStore } from '@/stores/app'
import type { AccountListItem } from '@/types'
import { extractApiErrorMessage } from '@/utils/apiError'

const { t, locale } = useI18n()
const text = (zh: string, en: string) => locale.value.startsWith('en') ? en : zh
const statusEpoch = ref(0)
const statusPanel = ref<InstanceType<typeof CodexGatewayBorrowStatusView> | null>(null)
const appStore = useAppStore()
const lifecycle = new AbortController()
const { accounts, loadAccounts, accountById, accountName, accountStateLabel, proxyLabel, wsLabel, modelMappingLabel } = useCodexBorrowInventory(lifecycle.signal)
const saved = ref<CodexGatewayBorrowConfig | null>(null)
const draft = ref<CodexGatewayBorrowConfig | null>(null)
const status = ref<CodexGatewayBorrowStatus | null>(null)
const loading = ref(true)
const statusRefreshing = ref(false)
const saving = ref(false)
const savedNotice = ref(false)
const loadError = ref('')
const statusError = ref('')
const saveError = ref('')
const DRAFT_KEY = 'codex-gateway-borrow-draft'

type AccountRole = 'source_account_ids' | 'target_account_ids'
const accountRoles: Array<{ key: AccountRole; other: AccountRole; label: string; hint: string }> = [
  { key: 'source_account_ids', other: 'target_account_ids', label: 'admin.codexGatewayBorrow.sources', hint: 'admin.codexGatewayBorrow.sourcesHint' },
  { key: 'target_account_ids', other: 'source_account_ids', label: 'admin.codexGatewayBorrow.targets', hint: 'admin.codexGatewayBorrow.targetsHint' }
]
const accountSearch = ref<Record<AccountRole, string>>({ source_account_ids: '', target_account_ids: '' })
const dirty = computed(() => !!draft.value && !!saved.value && !sameConfig(draft.value, saved.value))
const configError = computed(() => {
  if (!draft.value) return ''
  if (draft.value.source_account_ids.some(id => draft.value!.target_account_ids.includes(id))) return t('admin.codexGatewayBorrow.disjointRequired')
  if (draft.value.enabled && (!draft.value.source_account_ids.length || !draft.value.target_account_ids.length || !draft.value.models.length)) return t('admin.codexGatewayBorrow.selectionRequired')
  return ''
})

function copyConfig(value: CodexGatewayBorrowConfig): CodexGatewayBorrowConfig {
  return { enabled: value.enabled, source_account_ids: [...value.source_account_ids], target_account_ids: [...value.target_account_ids], models: [...value.models] }
}
function sameConfig(left: CodexGatewayBorrowConfig, right: CodexGatewayBorrowConfig) { return JSON.stringify(copyConfig(left)) === JSON.stringify(copyConfig(right)) }
function isConfig(value: unknown): value is CodexGatewayBorrowConfig {
  if (!value || typeof value !== 'object') return false
  const config = value as Partial<CodexGatewayBorrowConfig>
  return typeof config.enabled === 'boolean'
    && [config.source_account_ids, config.target_account_ids].every(ids => Array.isArray(ids) && ids.every(id => Number.isSafeInteger(id) && id > 0))
    && Array.isArray(config.models) && config.models.every(model => typeof model === 'string' && (BORROW_MODELS as readonly string[]).includes(model))
}
function clearStoredDraft() { try { sessionStorage.removeItem(DRAFT_KEY) } catch { /* The form remains usable when storage is unavailable. */ } }
function restoredDraft(base: CodexGatewayBorrowConfig): CodexGatewayBorrowConfig {
  try {
    const stored = sessionStorage.getItem(DRAFT_KEY)
    if (!stored) return copyConfig(base)
    const value = JSON.parse(stored) as { version?: unknown; base?: unknown; draft?: unknown }
    if (value?.version === 1 && isConfig(value.base) && isConfig(value.draft) && sameConfig(value.base, base)) return copyConfig(value.draft)
  } catch { /* Malformed or inaccessible drafts must not replace saved configuration. */ }
  clearStoredDraft()
  return copyConfig(base)
}
function persistDraft() {
  if (!draft.value || !saved.value) return
  if (!dirty.value) { clearStoredDraft(); return }
  try { sessionStorage.setItem(DRAFT_KEY, JSON.stringify({ version: 1, base: copyConfig(saved.value), draft: copyConfig(draft.value) })) }
  catch { /* Keep editing available even when session storage is full or disabled. */ }
}
function receiveConfig(value: CodexGatewayBorrowConfig, restore = false) {
  if (!isConfig(value)) throw new Error(t('admin.codexGatewayBorrow.invalidConfig'))
  const base = copyConfig(value)
  const nextDraft = restore ? restoredDraft(base) : copyConfig(base)
  saved.value = base
  draft.value = nextDraft
  if (!restore) clearStoredDraft()
}
function resetDraft() {
  if (!saved.value || saving.value) return
  draft.value = copyConfig(saved.value)
  clearStoredDraft()
  saveError.value = ''
}
function accountRestriction(account: AccountListItem, role: AccountRole) {
  if (account.status !== 'active') return text('账号未启用，不能准备借用。', 'Inactive account; borrowing cannot be prepared.')
  const mapping = account.credentials?.model_mapping as Record<string, unknown> | undefined
  if (!mapping || typeof mapping !== 'object' || !Object.keys(mapping).length) return ''
  const models = role === 'source_account_ids' ? ['gpt-6-astra'] : (draft.value?.models || [])
  for (const model of models) {
    if (typeof mapping[model] === 'string' && mapping[model] !== model) return text('模型映射到其他模型：', 'Maps to a different model: ') + model + ' → ' + mapping[model]
    if (!Object.keys(mapping).some(key => key.includes('*')) && !Object.prototype.hasOwnProperty.call(mapping, model)) return text('账号未允许模型：', 'Model is not allowed: ') + model
  }
  return ''
}
function filteredAccounts(role: AccountRole) {
  const search = accountSearch.value[role].trim().toLowerCase()
  return accounts.value.filter(account => !search || `${account.name} ${account.id}`.toLowerCase().includes(search))
}
function unknownSelectedAccounts(role: AccountRole) { return draft.value?.[role].filter(id => !accountById(id)) || [] }

async function refreshStatus() {
  if (statusRefreshing.value || lifecycle.signal.aborted) return
  statusRefreshing.value = true
  statusError.value = ''
  try { const value = await codexGatewayBorrowAPI.getStatus(lifecycle.signal); if (!lifecycle.signal.aborted) status.value = value }
  catch (value) { if (!lifecycle.signal.aborted) statusError.value = extractApiErrorMessage(value, t('admin.codexGatewayBorrow.statusLoadFailed')) }
  finally { statusRefreshing.value = false; await statusPanel.value?.refreshStatus?.() }
}
async function saveConfig() {
  if (!draft.value || !dirty.value || configError.value || loading.value || saving.value || statusRefreshing.value || lifecycle.signal.aborted) return
  saving.value = true
  saveError.value = ''
  savedNotice.value = false
  try {
    const value = await codexGatewayBorrowAPI.saveConfig(copyConfig(draft.value), lifecycle.signal)
    if (lifecycle.signal.aborted) return
    receiveConfig(value)
    savedNotice.value = true
    statusEpoch.value++
    appStore.showSuccess(t('admin.codexGatewayBorrow.saved'))
    await refreshStatus()
  } catch (value) { if (!lifecycle.signal.aborted) saveError.value = extractApiErrorMessage(value, t('admin.codexGatewayBorrow.saveFailed')) }
  finally { saving.value = false }
}

watch(draft, persistDraft, { deep: true, flush: 'sync' })
onMounted(async () => {
  const outcomes = await Promise.allSettled([
    codexGatewayBorrowAPI.getConfig(lifecycle.signal).then(value => { if (!lifecycle.signal.aborted) receiveConfig(value, true) }),
    refreshStatus(), loadAccounts()
  ])
  if (!lifecycle.signal.aborted) {
    loadError.value = outcomes.flatMap((outcome, index) => outcome.status === 'rejected'
      ? [extractApiErrorMessage(outcome.reason, t(index === 2 ? 'admin.codexGatewayBorrow.inventoryLoadFailed' : 'admin.codexGatewayBorrow.loadFailed'))]
      : []).join('\n')
    loading.value = false
  }
})
onBeforeUnmount(() => { persistDraft(); lifecycle.abort() })
</script>

<style scoped>
.borrow-error { max-height: 16rem; overflow: auto; white-space: pre-wrap; overflow-wrap: anywhere; border-radius: 0.5rem; padding: 0.65rem; font-size: 0.75rem; line-height: 1.5; color: rgb(153 27 27); background: rgb(254 242 242); }
:global(.dark) .borrow-error { color: rgb(254 202 202); background: rgb(69 10 10); }
</style>
