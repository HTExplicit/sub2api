<template>
  <AppLayout>
    <section class="space-y-6" data-ui="codex-fingerprint">
      <div>
        <h1 class="text-xl font-semibold text-ink">{{ text('Codex 指纹模拟', 'Codex fingerprint simulation') }}</h1>
        <p class="mt-2 text-sm text-muted">{{ text('统一管理指纹模拟并查看完整固定设备身份与当前策略。账号列表仅包含 OpenAI OAuth 和 Setup Token 账号；设备身份由后台自动生成并保留。', 'Manage fingerprint simulation and inspect complete fixed device identities and current policies. The list includes only OpenAI OAuth and setup-token accounts; device identities are generated and preserved by the backend.') }}</p>
      </div>
      <p v-if="error" role="alert" class="text-sm text-red-600 dark:text-red-400">{{ error }}</p>
      <p v-if="message" role="status" class="text-sm text-ink">{{ message }}</p>
      <form class="space-y-4 rounded-lg border border-line p-4" @submit.prevent="saveSettings">
        <fieldset :disabled="!settings || settingsBusy" class="space-y-4 disabled:opacity-60">
          <label class="flex items-center gap-3 text-sm font-medium text-ink">
            <input v-model="draft.enabled" data-test="fingerprint-enabled" type="checkbox" />
            {{ text('启用 Codex 指纹模拟', 'Enable Codex fingerprint simulation') }}
          </label>
          <p class="text-sm text-muted">{{ text('开启：使用每账号固定 TUI 身份，并按账号模式收敛标识。关闭：新请求保留客户端指纹；保存的配置和账号身份继续保留。', 'On: use a fixed TUI identity per account and converge identifiers according to its mode. Off: new requests retain the client fingerprint; saved configuration and account identities are preserved.') }}</p>
          <div class="grid gap-4 lg:grid-cols-2">
            <label class="block text-sm text-ink lg:col-span-2" for="codex-fingerprint-ua">
              {{ text('全局 UA', 'Global User-Agent') }}
              <input id="codex-fingerprint-ua" v-model.trim="draft.user_agent" data-test="fingerprint-ua" type="text" maxlength="512" class="input mt-1 w-full font-mono" />
              <span class="mt-1 block text-xs text-muted">{{ text('作为没有账号身份时的默认 UA；留空使用官方标准形态。UA 的版本由下方生效版本统一重建。', 'Default UA when no account identity is available; leave empty for the official format. Its version is rebuilt from the effective client version below.') }}</span>
            </label>
            <label class="block text-sm text-ink" for="codex-fingerprint-version">
              {{ text('手动版本', 'Pinned client version') }}
              <input id="codex-fingerprint-version" v-model.trim="draft.client_version" data-test="fingerprint-version" type="text" maxlength="64" class="input mt-1 w-full font-mono" :placeholder="text('留空使用同步值或内置版本', 'Leave empty for the synced or built-in version')" />
            </label>
            <label class="flex items-center gap-3 text-sm text-ink">
              <input v-model="draft.version_auto_sync_enabled" data-test="fingerprint-auto-sync" type="checkbox" />
              {{ text('每 6 小时自动同步官方稳定版', 'Sync the official stable version every 6 hours') }}
            </label>
          </div>
          <dl v-if="settings" class="grid gap-3 text-sm sm:grid-cols-2">
            <div><dt class="text-muted">{{ text('同步版本', 'Synced version') }}</dt><dd class="font-mono text-ink">{{ settings.synced_version || '—' }}</dd></div>
            <div><dt class="text-muted">{{ settings.enabled ? text('生效模拟版本', 'Effective simulated version') : text('待启用模拟版本', 'Saved simulation version') }}</dt><dd class="font-mono text-ink" data-test="fingerprint-effective-version">{{ settings.effective_version }}</dd></div>
            <div class="sm:col-span-2"><dt class="text-muted">{{ text('全局默认身份', 'Global default identity') }}</dt><dd class="break-words font-mono text-ink">{{ settings.effective_user_agent }}</dd></div>
          </dl>
          <button data-test="fingerprint-save" type="submit" :disabled="settingsBusy || !settingsDirty" class="btn btn-primary">{{ text('保存设置', 'Save settings') }}</button>
        </fieldset>
        <p v-if="!settings" class="text-sm text-muted">{{ t('common.loading') }}</p>
      </form>

      <section class="space-y-4" :aria-label="text('账号收敛设置', 'Account convergence settings')">
        <div class="flex flex-wrap items-end justify-between gap-3">
          <div><h2 class="text-lg font-semibold text-ink">{{ text('账号收敛设置', 'Account convergence settings') }}</h2><p class="mt-1 text-sm text-muted">{{ text('模式只控制标识收敛；上方总开关控制整个模拟功能。', 'Modes control identifier convergence; the master switch above controls the entire simulation.') }}</p></div>
          <form class="flex items-center gap-2" @submit.prevent="searchAccounts">
            <label class="sr-only" for="codex-fingerprint-search">{{ text('搜索账号', 'Search accounts') }}</label>
            <input id="codex-fingerprint-search" v-model="search" class="input w-48" :placeholder="text('搜索账号', 'Search accounts')" />
            <button type="submit" class="btn btn-secondary" :disabled="rowsLoading">{{ text('搜索', 'Search') }}</button>
          </form>
        </div>
        <div class="flex flex-wrap items-center gap-3">
          <label class="text-sm text-ink" for="codex-fingerprint-bulk-mode">{{ text('选中账号的模式', 'Mode for selected accounts') }} ({{ selectedIDs.length }})</label>
          <select id="codex-fingerprint-bulk-mode" v-model="bulkMode" data-test="fingerprint-bulk-mode" class="input w-auto" :disabled="bulkBusy">
            <option v-for="mode in modes" :key="mode.value" :value="mode.value">{{ mode.label }}</option>
          </select>
          <button data-test="fingerprint-bulk-save" type="button" class="btn btn-secondary" :disabled="!selectedIDs.length || bulkBusy" @click="saveBulk">{{ text('应用到选中账号', 'Apply to selected accounts') }}</button>
          <button type="button" class="btn btn-secondary" :disabled="rowsLoading" @click="loadRows">{{ text('刷新账号', 'Refresh accounts') }}</button>
        </div>
        <div class="overflow-x-auto rounded-lg border border-line" :aria-busy="rowsLoading">
          <table class="w-full text-left text-sm">
            <thead class="border-b border-line bg-surface text-muted"><tr>
              <th class="p-3"><input type="checkbox" :checked="allPageSelected" :indeterminate="somePageSelected && !allPageSelected" :aria-label="text('选择本页账号', 'Select accounts on this page')" @change="selectPage(($event.target as HTMLInputElement).checked)" /></th>
              <th class="p-3">{{ text('账号', 'Account') }}</th><th class="p-3">{{ text('标识收敛模式', 'Identifier convergence mode') }}</th><th class="p-3">{{ text('操作', 'Actions') }}</th>
            </tr></thead>
            <tbody class="divide-y divide-line text-ink">
              <template v-for="account in rows" :key="account.id">
                <tr>
                  <td class="p-3"><input type="checkbox" :checked="selectedIDs.includes(account.id)" :aria-label="text('选择账号 ', 'Select account ') + account.name" @change="selectAccount(account.id, ($event.target as HTMLInputElement).checked)" /></td>
                  <td class="p-3"><span class="block font-medium">{{ account.name }}</span><span class="text-xs text-muted">#{{ account.id }} · {{ account.type }}{{ account.parent_account_id ? text(' · 影子账号', ' · shadow account') : '' }}</span></td>
                  <td class="p-3"><select v-model="draftModes[account.id]" :data-test="`fingerprint-mode-${account.id}`" :aria-label="text('账号模式 ', 'Account mode ') + account.name" class="input w-auto" :disabled="rowBusy[account.id]"><option v-for="mode in modes" :key="mode.value" :value="mode.value">{{ mode.label }}</option></select></td>
                  <td class="p-3"><div class="flex flex-wrap gap-2"><button type="button" class="btn btn-secondary btn-sm" :data-test="`fingerprint-account-save-${account.id}`" :disabled="rowBusy[account.id] || settingsBusy || rowsLoading || draftModes[account.id] === savedMode(account)" @click="saveAccount(account)">{{ text('保存', 'Save') }}</button><button type="button" class="btn btn-secondary btn-sm" :data-test="'fingerprint-account-refresh-' + account.id" :aria-label="text('刷新账号身份 ', 'Refresh account identity ') + account.name" :disabled="rowBusy[account.id] || detailLoading[account.id] || settingsBusy || rowsLoading" @click="requestIdentity(account.id)">{{ text('刷新身份', 'Refresh identity') }}</button></div></td>
                </tr>
                <tr><td colspan="4" class="bg-surface p-4" :aria-busy="detailLoading[account.id] || settingsBusy">
                  <div v-if="detailErrors[account.id]" class="flex flex-wrap items-center gap-3"><p role="alert" class="text-sm text-red-600 dark:text-red-400">{{ detailErrors[account.id] }}</p><button type="button" class="btn btn-secondary btn-sm" :data-test="'fingerprint-account-retry-' + account.id" :aria-label="text('重试读取账号身份 ', 'Retry account identity ') + account.name" :disabled="detailLoading[account.id] || rowBusy[account.id] || settingsBusy || rowsLoading" @click="requestIdentity(account.id)">{{ text('重试读取身份', 'Retry identity') }}</button></div>
                  <CodexFingerprintIdentity v-if="accountViews[account.id]" :view="accountViews[account.id]!" :account-id="account.id" />
                  <p v-else-if="!detailErrors[account.id]" role="status" class="text-sm text-muted">{{ settingsBusy ? text('正在保存策略，保存结束后重新读取身份', 'Saving policy; identities will reload when the save finishes') : text('正在读取完整身份…', 'Loading complete identity…') }}</p>
                </td></tr>
              </template>
              <tr v-if="!rows.length"><td colspan="4" class="p-6 text-center text-muted">{{ rowsLoading ? t('common.loading') : text('没有符合条件的账号', 'No matching accounts') }}</td></tr>
            </tbody>
          </table>
        </div>
        <div class="flex items-center justify-between gap-3 text-sm text-muted"><span>{{ page }} / {{ Math.max(1, Math.ceil(total / pageSize)) }} · {{ total }}</span><div class="flex gap-2"><button class="btn btn-secondary btn-sm" :disabled="rowsLoading || page <= 1" @click="changePage(-1)">{{ text('上一页', 'Previous') }}</button><button class="btn btn-secondary btn-sm" :disabled="rowsLoading || page * pageSize >= total" @click="changePage(1)">{{ text('下一页', 'Next') }}</button></div></div>
      </section>
    </section>
  </AppLayout>
</template>

<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, reactive, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import AppLayout from '@/components/layout/AppLayout.vue'
import CodexFingerprintIdentity from '@/components/admin/CodexFingerprintIdentity.vue'
import accountsAPI from '@/api/admin/accounts'
import { codexFingerprintAPI, type CodexFingerprintSettings, type CodexFingerprintSettingsView, type CodexFingerprintMode, type CodexFingerprintAccountView } from '@/api/admin/codexFingerprint'
import { useAccountJobsStore, isTerminalAccountJob } from '@/stores/accountJobs'
import { extractApiErrorMessage } from '@/utils/apiError'
import type { AccountListItem } from '@/types'

const { t, locale } = useI18n()
const text = (zh: string, en: string) => locale.value.startsWith('en') ? en : zh
const jobs = useAccountJobsStore()
const settings = ref<CodexFingerprintSettingsView | null>(null)
const draft = reactive<CodexFingerprintSettings>({ enabled: true, user_agent: '', client_version: '', version_auto_sync_enabled: true })
const settingsBusy = ref(false), rowsLoading = ref(false), bulkBusy = ref(false)
const error = ref(''), message = ref('')
const rows = ref<AccountListItem[]>([]), selectedIDs = ref<number[]>([])
const draftModes = reactive<Record<number, CodexFingerprintMode>>({})
const rowBusy = reactive<Record<number, boolean>>({})
const accountViews = reactive<Record<number, CodexFingerprintAccountView>>({})
const storedModes = reactive<Record<number, CodexFingerprintMode>>({})
const detailLoading = reactive<Record<number, boolean>>({})
const detailErrors = reactive<Record<number, string>>({})
const page = ref(1), total = ref(0), search = ref(''), activeSearch = ref(''), bulkMode = ref<CodexFingerprintMode>('device')
const pageSize = 20
let generation = 0, listSerial = 0
const ownedJobID = ref(0)
let listController: AbortController | null = null
let detailController = new AbortController(), detailEpoch = 0, detailSerial = 0, activeDetails = 0
const detailVersions: Record<number, number> = {}
type DetailTask = { id: number; epoch: number; serial: number; list: number; generation: number; signal: AbortSignal }
const detailQueue: DetailTask[] = []
const modes = computed(() => [
  { value: 'off' as const, label: text('保留设备／会话标识', 'Preserve device/session identifiers') },
  { value: 'device' as const, label: text('仅设备', 'Device only') },
  { value: 'session' as const, label: text('设备＋会话', 'Device + session') },
  { value: 'full' as const, label: text('设备＋会话＋线程', 'Device + session + thread') }
])
const settingsDirty = computed(() => settings.value && (draft.enabled !== settings.value.enabled || draft.user_agent !== settings.value.user_agent || draft.client_version !== settings.value.client_version || draft.version_auto_sync_enabled !== settings.value.version_auto_sync_enabled))
const allPageSelected = computed(() => rows.value.length > 0 && rows.value.every(row => selectedIDs.value.includes(row.id)))
const somePageSelected = computed(() => rows.value.some(row => selectedIDs.value.includes(row.id)))

function savedMode(account: AccountListItem): CodexFingerprintMode {
  const value = storedModes[account.id] ?? account.extra?.codex_fingerprint_mode
  return modes.value.some(mode => mode.value === value) ? value as CodexFingerprintMode : 'device'
}
function selectAccount(id: number, selected: boolean) {
  selectedIDs.value = selected ? [...new Set([...selectedIDs.value, id])] : selectedIDs.value.filter(value => value !== id)
}
function selectPage(selected: boolean) { for (const row of rows.value) selectAccount(row.id, selected) }
function invalidateIdentity(id: number) {
  detailVersions[id] = ++detailSerial
  delete accountViews[id]; delete detailErrors[id]; delete detailLoading[id]
}
function invalidateDetails() {
  detailEpoch++; detailController.abort(); detailController = new AbortController()
  detailQueue.splice(0)
  for (const record of [accountViews, detailErrors, detailLoading]) for (const id of Object.keys(record)) delete record[Number(id)]
}
function taskIsCurrent(task: DetailTask): boolean {
  return task.generation === generation && task.epoch === detailEpoch && task.list === listSerial &&
    task.serial === detailVersions[task.id] && rows.value.some(row => row.id === task.id)
}
function applyAccountView(account: AccountListItem, view: CodexFingerprintAccountView) {
  const dirty = draftModes[account.id] !== savedMode(account)
  storedModes[account.id] = view.mode
  account.extra = { ...account.extra, codex_fingerprint_mode: view.mode }
  accountViews[account.id] = view
  if (!dirty) draftModes[account.id] = view.mode
}
function pumpDetails() {
  while (activeDetails < 3 && detailQueue.length) {
    const task = detailQueue.shift()!
    if (!taskIsCurrent(task)) continue
    activeDetails++
    void readIdentity(task).finally(() => { activeDetails--; pumpDetails() })
  }
}
async function readIdentity(task: DetailTask) {
  try {
    const view = await codexFingerprintAPI.getAccount(task.id, { signal: task.signal })
    if (!taskIsCurrent(task)) return
    const account = rows.value.find(row => row.id === task.id)!
    applyAccountView(account, view)
  } catch (cause) {
    if (taskIsCurrent(task) && !task.signal.aborted) detailErrors[task.id] = extractApiErrorMessage(cause, t('common.error'))
  } finally { if (taskIsCurrent(task)) detailLoading[task.id] = false }
}
function requestIdentity(id: number) {
  if (settingsBusy.value || rowsLoading.value || rowBusy[id] || detailLoading[id] || !rows.value.some(row => row.id === id)) return
  delete detailErrors[id]
  const serial = ++detailSerial
  detailVersions[id] = serial; detailLoading[id] = true
  detailQueue.push({ id, epoch: detailEpoch, serial, list: listSerial, generation, signal: detailController.signal })
  pumpDetails()
}
function loadIdentities() {
  for (const row of rows.value) requestIdentity(row.id)
}
async function loadSettings() {
  const current = generation
  try {
    const value = await codexFingerprintAPI.getSettings()
    if (current !== generation) return
    settings.value = value; Object.assign(draft, value)
  } catch (cause) { if (current === generation) error.value = extractApiErrorMessage(cause, t('common.error')) }
}
async function saveSettings() {
  if (!settings.value || settingsBusy.value) return
  const current = generation
  const sent: CodexFingerprintSettings = { enabled: draft.enabled, user_agent: draft.user_agent, client_version: draft.client_version, version_auto_sync_enabled: draft.version_auto_sync_enabled }
  settingsBusy.value = true; error.value = ''; message.value = ''
  invalidateDetails()
  try {
    const value = await codexFingerprintAPI.saveSettings(sent)
    if (current !== generation) return
    settings.value = value; Object.assign(draft, value)
    message.value = text('设置已保存，新请求立即生效', 'Settings saved; new requests use the new policy')
  } catch (cause) { if (current === generation) error.value = extractApiErrorMessage(cause, t('common.error')) }
  finally { if (current === generation) { settingsBusy.value = false; loadIdentities() } }
}
async function loadRows() {
  const current = ++listSerial
  listController?.abort(); listController = new AbortController(); rowsLoading.value = true; invalidateDetails()
  rows.value = []
  const controller = listController
  try {
    const result = await accountsAPI.list(page.value, pageSize, { platform: 'openai', types: 'oauth,setup-token', search: activeSearch.value, lite: '0' }, { signal: controller.signal })
    if (current !== listSerial) return
    rows.value = result.items; total.value = result.total
    for (const row of rows.value) {
      const dirty = draftModes[row.id] !== undefined && draftModes[row.id] !== savedMode(row)
      const value = row.extra?.codex_fingerprint_mode
      storedModes[row.id] = modes.value.some(mode => mode.value === value) ? value as CodexFingerprintMode : 'device'
      if (!dirty) draftModes[row.id] = storedModes[row.id]!
    }
  } catch (cause) { if (current === listSerial && !controller.signal.aborted) error.value = extractApiErrorMessage(cause, t('common.error')) }
  finally { if (current === listSerial) { rowsLoading.value = false; loadIdentities() } }
}
function searchAccounts() { page.value = 1; activeSearch.value = search.value.trim(); void loadRows() }
function changePage(delta: number) { page.value += delta; void loadRows() }
async function saveAccount(account: AccountListItem) {
  const current = generation, mode = draftModes[account.id]
  if (!mode || rowBusy[account.id]) return
  rowBusy[account.id] = true; error.value = ''; message.value = ''
  invalidateIdentity(account.id)
  const epoch = detailEpoch, list = listSerial
  try {
    const view = await codexFingerprintAPI.saveAccount(account.id, mode)
    if (current !== generation) return
    storedModes[account.id] = view.mode
    if (epoch === detailEpoch && list === listSerial) {
      applyAccountView(account, view)
      if (draftModes[account.id] === mode) draftModes[account.id] = view.mode
    }
    message.value = text('账号模式已保存', 'Account mode saved')
  } catch (cause) { if (current === generation) error.value = extractApiErrorMessage(cause, t('common.error')) }
  finally { if (current === generation) { rowBusy[account.id] = false; if (!accountViews[account.id]) requestIdentity(account.id) } }
}
async function saveBulk() {
  if (!selectedIDs.value.length || bulkBusy.value) return
  const ids = [...selectedIDs.value], mode = bulkMode.value, current = generation
  bulkBusy.value = true; error.value = ''; message.value = ''
  try {
    const job = await accountsAPI.bulkUpdate(ids, { extra: { codex_fingerprint_mode: mode } })
    if (current !== generation) return
    ownedJobID.value = job.id; jobs.track(job)
    selectedIDs.value = selectedIDs.value.filter(id => !ids.includes(id))
    message.value = text('批量任务已提交，可在操作记录中查看结果', 'Batch operation submitted; results are available in operation history')
  } catch (cause) { if (current === generation) error.value = extractApiErrorMessage(cause, t('common.error')) }
  finally { if (current === generation) bulkBusy.value = false }
}
watch(() => jobs.visibleJobs.find(job => job.id === ownedJobID.value)?.status, () => {
  const job = jobs.visibleJobs.find(job => job.id === ownedJobID.value)
  if (job && isTerminalAccountJob(job)) { ownedJobID.value = 0; void loadRows() }
})
onMounted(() => { void loadSettings(); void loadRows() })
onBeforeUnmount(() => { generation++; listSerial++; listController?.abort(); detailController.abort(); detailQueue.splice(0) })
</script>
