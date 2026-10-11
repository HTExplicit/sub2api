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
        <fieldset :disabled="saving" class="space-y-3 rounded-lg border border-line p-4" data-test="borrow-node-config">
          <legend class="px-1 text-sm font-medium">{{ text('候选节点与失败冷却', 'Candidate nodes and failure cooling') }}</legend>
          <label class="flex items-start gap-3 text-sm">
            <input v-model="draft.rotate_nodes" type="checkbox" class="checkbox mt-1" data-test="borrow-rotate-nodes" @change="initializeNodes" />
            <span>{{ text('为已选目标账号启用节点选择', 'Enable node selection for the selected target accounts') }}<span class="mt-1 block text-xs text-muted">{{ text('来源采集、目标验证与业务发送使用同一候选节点；失败节点进入冷却。仅支持 HTTP。', 'Acquisition, target validation and business dispatch use the same candidate node. Failed nodes enter cooling. HTTP only.') }}</span></span>
          </label>
          <template v-if="draft.rotate_nodes">
            <label class="flex items-start gap-3 text-sm"><input v-model="draft.wait_for_qualified_route" type="checkbox" class="checkbox mt-1" data-test="borrow-wait-qualified" /><span>{{ text('等待严格验证通过后继续原请求', 'Wait for strict qualification before continuing the original request') }}<span class="mt-1 block text-xs text-muted">{{ text('失败后按冷却时间恢复，连接有效期间不设恢复总时限；取消或断开即停止。不会改走普通请求。', 'Recover after cooling without an overall recovery deadline while connected. Cancellation or disconnection stops recovery. Ordinary fallback is disabled.') }}</span></span></label>
            <p class="text-xs text-muted">{{ text('按勾选顺序搜索；仅影响上方选中的目标账号，不改写账号或共享代理。', 'Search in selection order. Only the target accounts above are affected; account and shared proxy records are preserved.') }}</p>
            <p v-if="proxyError" role="alert" class="text-sm text-red-700 dark:text-red-300">{{ proxyError }}</p>
            <div class="grid max-h-64 gap-2 overflow-auto sm:grid-cols-2">
              <label class="flex items-center gap-2 text-sm"><input v-model="draft.node_proxy_ids" type="checkbox" class="checkbox" :value="0" data-test="borrow-node-0" />{{ text('原服务器直连', 'Server direct') }}</label>
              <label v-for="proxy in proxies" :key="proxy.id" class="flex items-center gap-2 text-sm"><input v-model="draft.node_proxy_ids" type="checkbox" class="checkbox" :value="proxy.id" :data-test="`borrow-node-${proxy.id}`" />{{ proxy.name }} #{{ proxy.id }}</label>
              <template v-if="proxiesLoaded"><label v-for="id in draft.node_proxy_ids?.filter(id => id !== 0 && !proxies.some(proxy => proxy.id === id)) || []" :key="id" class="flex items-center gap-2 text-sm text-amber-800 dark:text-amber-200"><input v-model="draft.node_proxy_ids" type="checkbox" class="checkbox" :value="id" />#{{ id }} · {{ text('不在可用清单中', 'Not in the available list') }}</label></template>
            </div>
            <p class="text-xs text-muted">{{ text('当前顺序', 'Current order') }}: {{ draft.node_proxy_ids?.map(id => id === 0 ? text('原服务器直连', 'Server direct') : `#${id}`).join(' → ') || '—' }}</p>
            <div class="grid gap-3 sm:grid-cols-2">
              <label class="text-sm">{{ text('每次准备最多尝试节点数', 'Maximum nodes per preparation') }}<input v-model.number="draft.max_node_attempts" type="number" min="1" max="10" class="input mt-1 w-full" data-test="borrow-node-attempts" /></label>
              <label class="text-sm">{{ text('明确拒绝后的冷却秒数', 'Cooldown after an explicit rejection (seconds)') }}<input v-model.number="draft.node_cooldown_seconds" type="number" min="60" max="86400" class="input mt-1 w-full" data-test="borrow-node-cooldown" /></label>
            </div>
            <p class="text-xs text-muted">{{ text('Sol 和 Astra 分别验证。候选耗尽会明确报错，已发送的生成不会换节点重放。', 'Sol and Astra are validated independently. Exhaustion is reported explicitly; a dispatched generation is never replayed on another node.') }}</p>
          </template>
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
import type { AccountListItem, Proxy } from '@/types'
import { list as listProxies } from '@/api/admin/proxies'
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
const proxies = ref<Proxy[]>([])
const proxyError = ref('')
const proxiesLoaded = ref(false)
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
  if (draft.value.enabled && draft.value.wait_for_qualified_route && !draft.value.rotate_nodes) return text('等待恢复需要启用节点选择。', 'Recovery waiting requires node selection.')
  if (draft.value.source_account_ids.some(id => draft.value!.target_account_ids.includes(id))) return t('admin.codexGatewayBorrow.disjointRequired')
  if (draft.value.enabled && (!draft.value.source_account_ids.length || !draft.value.target_account_ids.length || !draft.value.models.length)) return t('admin.codexGatewayBorrow.selectionRequired')
  if (draft.value.rotate_nodes && (!draft.value.node_proxy_ids?.length || !Number.isInteger(draft.value.max_node_attempts) || draft.value.max_node_attempts! < 1 || draft.value.max_node_attempts! > 10 || !Number.isInteger(draft.value.node_cooldown_seconds) || draft.value.node_cooldown_seconds! < 60 || draft.value.node_cooldown_seconds! > 86400)) return text('请选择候选节点，尝试次数须为 1–10，冷却时间须为 60–86400 秒。', 'Select candidate nodes, 1–10 attempts and a cooldown of 60–86400 seconds.')
  return ''
})

function copyConfig(value: CodexGatewayBorrowConfig): CodexGatewayBorrowConfig {
  const copy: CodexGatewayBorrowConfig = { enabled: value.enabled, source_account_ids: [...value.source_account_ids], target_account_ids: [...value.target_account_ids], models: [...value.models] }
  if (value.wait_for_qualified_route) copy.wait_for_qualified_route = true
  if (value.rotate_nodes || value.node_proxy_ids?.length || value.max_node_attempts || value.node_cooldown_seconds) Object.assign(copy, { rotate_nodes: !!value.rotate_nodes, node_proxy_ids: [...(value.node_proxy_ids || [])], max_node_attempts: value.max_node_attempts || 3, node_cooldown_seconds: value.node_cooldown_seconds || 3600 })
  return copy
}
async function loadNodes() {
  if (proxies.value.length || lifecycle.signal.aborted) return
  proxyError.value = ''
  try {
    const found: Proxy[] = []
    for (let page = 1; !lifecycle.signal.aborted; page++) {
      const result = await listProxies(page, 100, { status: 'active' }, { signal: lifecycle.signal })
      found.push(...result.items)
      if (!result.items.length || page * 100 >= result.total) break
    }
    if (!lifecycle.signal.aborted) { proxies.value = found; proxiesLoaded.value = true }
  } catch (error) { if (!lifecycle.signal.aborted) proxyError.value = extractApiErrorMessage(error, text('候选节点读取失败', 'Could not load candidate nodes')) }
}
function initializeNodes() {
  if (!draft.value?.rotate_nodes) return
  draft.value.node_proxy_ids ||= []
  draft.value.max_node_attempts ||= 3
  draft.value.node_cooldown_seconds ||= 3600
  void loadNodes()
}
function sameConfig(left: CodexGatewayBorrowConfig, right: CodexGatewayBorrowConfig) { return JSON.stringify(copyConfig(left)) === JSON.stringify(copyConfig(right)) }
function isConfig(value: unknown): value is CodexGatewayBorrowConfig {
  if (!value || typeof value !== 'object') return false
  const config = value as Partial<CodexGatewayBorrowConfig>
  return typeof config.enabled === 'boolean'
    && [config.source_account_ids, config.target_account_ids].every(ids => Array.isArray(ids) && ids.every(id => Number.isSafeInteger(id) && id > 0))
    && Array.isArray(config.models) && config.models.every(model => typeof model === 'string' && (BORROW_MODELS as readonly string[]).includes(model))
    && (config.rotate_nodes === undefined || typeof config.rotate_nodes === 'boolean')
    && (config.wait_for_qualified_route === undefined || typeof config.wait_for_qualified_route === 'boolean')
    && (config.node_proxy_ids === undefined || (Array.isArray(config.node_proxy_ids) && config.node_proxy_ids.every(id => Number.isSafeInteger(id) && id >= 0)))
    && [config.max_node_attempts, config.node_cooldown_seconds].every(value => value === undefined || (Number.isInteger(value) && value >= 0))
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
  initializeNodes()
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
