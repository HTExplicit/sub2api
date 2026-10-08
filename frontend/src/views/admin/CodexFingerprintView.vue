<template>
  <AppLayout>
    <section class="space-y-6" data-ui="codex-fingerprint">
      <div>
        <h1 class="text-xl font-semibold text-ink">{{ text('Codex 指纹', 'Codex fingerprint') }}</h1>
        <p class="mt-2 text-sm text-muted">{{ text('统一管理 OpenAI OAuth 和 Setup Token 账号的 Codex TUI 身份及设备、会话标识。', 'Manage the Codex TUI identity and device/session identifiers of OpenAI OAuth and setup-token accounts.') }}</p>
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
                  <td class="p-3"><div class="flex flex-wrap gap-2"><button type="button" class="btn btn-secondary btn-sm" :data-test="`fingerprint-account-save-${account.id}`" :disabled="rowBusy[account.id] || draftModes[account.id] === savedMode(account)" @click="saveAccount(account)">{{ text('保存', 'Save') }}</button><button type="button" class="btn btn-secondary btn-sm" :disabled="rowBusy[account.id]" @click="showIdentity(account.id)">{{ text('查看生效身份', 'View effective identity') }}</button></div></td>
                </tr>
                <tr v-if="accountViews[account.id]"><td colspan="4" class="bg-surface p-4"><dl class="grid gap-3 text-sm sm:grid-cols-2">
                  <div class="sm:col-span-2"><dt class="text-muted">User-Agent</dt><dd class="break-words font-mono">{{ accountViews[account.id]!.identity.user_agent }}</dd></div>
                  <div><dt class="text-muted">{{ text('身份来源', 'Identity source') }}</dt><dd>{{ identitySource(accountViews[account.id]!.identity.identity_source) }} · #{{ accountViews[account.id]!.identity.identity_account_id }}</dd></div>
                  <div><dt class="text-muted">Originator / Version</dt><dd class="font-mono">{{ accountViews[account.id]!.identity.originator }} / {{ accountViews[account.id]!.identity.version }}</dd></div>
                  <div><dt class="text-muted">{{ text('当前收敛状态', 'Effective convergence') }}</dt><dd>{{ effectiveMode(accountViews[account.id]!) }}</dd></div>
                </dl></td></tr>
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
const page = ref(1), total = ref(0), search = ref(''), activeSearch = ref(''), bulkMode = ref<CodexFingerprintMode>('device')
const pageSize = 20
let generation = 0, listSerial = 0
const ownedJobID = ref(0)
let listController: AbortController | null = null
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
  const value = accountViews[account.id]?.mode || account.extra?.codex_fingerprint_mode
  return modes.value.some(mode => mode.value === value) ? value as CodexFingerprintMode : 'device'
}
function selectAccount(id: number, selected: boolean) {
  selectedIDs.value = selected ? [...new Set([...selectedIDs.value, id])] : selectedIDs.value.filter(value => value !== id)
}
function selectPage(selected: boolean) { for (const row of rows.value) selectAccount(row.id, selected) }
function identitySource(source: string): string {
  const labels: Record<string, string> = { account: text('账号固定 TUI', 'Fixed account TUI'), override_ua: text('账号自定义 UA', 'Account UA override'), canonical: text('全局默认', 'Global default'), protocol_fallback: text('协议兜底；实际请求保留客户端身份', 'Protocol fallback; actual requests retain the client identity') }
  return labels[source] || source
}
function effectiveMode(view: CodexFingerprintAccountView): string {
  if (!view.simulation_enabled) return text('总开关已关闭', 'Master switch is off')
  if (view.identity.fingerprint_reason === 'seed_missing') return text('身份种子缺失，未收敛', 'Missing identity seed; no convergence')
  return modes.value.find(mode => mode.value === view.identity.fingerprint_mode_effective)?.label || view.identity.fingerprint_reason
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
  try {
    const value = await codexFingerprintAPI.saveSettings(sent)
    if (current !== generation) return
    settings.value = value; Object.assign(draft, value)
    for (const id of Object.keys(accountViews)) delete accountViews[Number(id)]
    message.value = text('设置已保存，新请求立即生效', 'Settings saved; new requests use the new policy')
  } catch (cause) { if (current === generation) error.value = extractApiErrorMessage(cause, t('common.error')) }
  finally { if (current === generation) settingsBusy.value = false }
}
async function loadRows() {
  const current = ++listSerial
  listController?.abort(); listController = new AbortController(); rowsLoading.value = true
  try {
    const result = await accountsAPI.list(page.value, pageSize, { platform: 'openai', types: 'oauth,setup-token', search: activeSearch.value, lite: '0' }, { signal: listController.signal })
    if (current !== listSerial) return
    rows.value = result.items; total.value = result.total
    for (const row of rows.value) { delete accountViews[row.id]; draftModes[row.id] = savedMode(row) }
  } catch (cause) { if (current === listSerial && !listController.signal.aborted) error.value = extractApiErrorMessage(cause, t('common.error')) }
  finally { if (current === listSerial) rowsLoading.value = false }
}
function searchAccounts() { page.value = 1; activeSearch.value = search.value.trim(); void loadRows() }
function changePage(delta: number) { page.value += delta; void loadRows() }
async function showIdentity(id: number) {
  if (accountViews[id]) { delete accountViews[id]; return }
  const current = generation
  const account = rows.value.find(row => row.id === id)
  const dirty = account && draftModes[id] !== savedMode(account)
  rowBusy[id] = true
  try {
    const view = await codexFingerprintAPI.getAccount(id)
    if (current === generation) {
      if (account) account.extra = { ...account.extra, codex_fingerprint_mode: view.mode }
      accountViews[id] = view
      if (!dirty) draftModes[id] = view.mode
    }
  }
  catch (cause) { if (current === generation) error.value = extractApiErrorMessage(cause, t('common.error')) }
  finally { if (current === generation) rowBusy[id] = false }
}
async function saveAccount(account: AccountListItem) {
  const current = generation, mode = draftModes[account.id]
  if (!mode || rowBusy[account.id]) return
  rowBusy[account.id] = true; error.value = ''; message.value = ''
  try {
    const view = await codexFingerprintAPI.saveAccount(account.id, mode)
    if (current !== generation) return
    accountViews[account.id] = view
    account.extra = { ...account.extra, codex_fingerprint_mode: view.mode }
    draftModes[account.id] = view.mode
    message.value = text('账号模式已保存', 'Account mode saved')
  } catch (cause) { if (current === generation) error.value = extractApiErrorMessage(cause, t('common.error')) }
  finally { if (current === generation) rowBusy[account.id] = false }
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
onBeforeUnmount(() => { generation++; listSerial++; listController?.abort() })
</script>
