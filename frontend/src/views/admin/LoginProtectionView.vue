<template>
  <AppLayout>
    <div class="mx-auto max-w-5xl space-y-6" data-ui="login-protection">
      <header class="space-y-1 border-b border-line pb-4">
        <h1 class="text-xl font-semibold">{{ t('admin.loginProtection.title') }}</h1>
        <p class="text-sm text-muted">{{ t('admin.loginProtection.description') }}</p>
      </header>

      <section class="card space-y-4 p-5">
        <div class="flex items-center justify-between gap-4">
          <h2 class="font-semibold">{{ t('admin.loginProtection.rules') }}</h2>
          <button type="button" class="btn btn-secondary btn-sm" :disabled="loading || saving" @click="loadSettings">{{ t('admin.loginProtection.reload') }}</button>
        </div>
        <p v-if="settingsError" role="alert" class="text-sm text-red-600 dark:text-red-400">{{ settingsError }}</p>
        <p v-if="loading" class="text-sm text-muted">{{ t('common.loading') }}</p>
        <form v-else-if="draft" novalidate class="space-y-5" @submit.prevent="save">
          <div v-if="submitted && fields.some(field => errors[field.key])" ref="errorSummary" tabindex="-1" role="alert" aria-labelledby="login-protection-errors" class="space-y-1 rounded border border-red-500 p-3 text-sm text-red-600 dark:text-red-400">
            <p id="login-protection-errors" class="font-medium">{{ t('admin.loginProtection.validationFailed') }}</p>
            <ul class="list-inside list-disc"><template v-for="field in fields" :key="field.key"><li v-if="errors[field.key]"><a :href="`#login-protection-${field.key}`" class="underline">{{ t(`admin.loginProtection.${field.key}`) }}: {{ errors[field.key] }}</a></li></template></ul>
          </div>
          <div class="flex items-center justify-between gap-4" data-ui="setting-row">
            <label for="login-protection-enabled" class="font-medium">{{ t('admin.loginProtection.enabled') }}</label>
            <Toggle id="login-protection-enabled" v-model="draft.enabled" :aria-label="t('admin.loginProtection.enabled')" :disabled="saving" />
          </div>
          <div class="grid gap-4 sm:grid-cols-3">
            <div v-for="field in fields" :key="field.key" class="space-y-1">
              <label :for="`login-protection-${field.key}`" class="block text-sm font-medium">{{ t(`admin.loginProtection.${field.key}`) }}</label>
              <input
                :id="`login-protection-${field.key}`" v-model.number="draft[field.key]" type="number" min="1" :max="field.max" step="1"
                class="input w-full" :disabled="saving" :aria-invalid="!!errors[field.key]" :aria-describedby="`login-protection-${field.key}-hint`"
              />
              <p :id="`login-protection-${field.key}-hint`" class="text-xs" :class="errors[field.key] ? 'text-red-600 dark:text-red-400' : 'text-muted'" :role="errors[field.key] ? 'alert' : undefined">
                {{ errors[field.key] || t('admin.loginProtection.range', { max: field.max }) }}
              </p>
            </div>
          </div>
          <p class="text-sm" data-testid="login-protection-summary">{{ t('admin.loginProtection.summary', { minutes: draft.window_minutes, failures: draft.max_failures, trigger: Number(draft.max_failures) + 1, hours: draft.ban_hours }) }}</p>
          <p class="text-sm text-muted">{{ t('admin.loginProtection.scopeNote') }}</p>
          <p class="text-sm text-muted">{{ t('admin.loginProtection.disableNote') }}</p>
          <button type="submit" class="btn btn-primary" :disabled="saving" data-testid="login-protection-save">{{ saving ? t('common.saving') : t('common.save') }}</button>
        </form>
      </section>

      <section class="card space-y-4 p-5">
        <div class="flex items-center justify-between gap-4">
          <h2 class="font-semibold">{{ t('admin.loginProtection.bans') }}</h2>
          <button type="button" class="btn btn-secondary btn-sm" :disabled="bansLoading || !!unbanning" @click="loadBans">{{ t('admin.loginProtection.reload') }}</button>
        </div>
        <p class="text-sm text-muted">{{ t('admin.loginProtection.unbanNote') }}</p>
        <p v-if="bansError" role="alert" class="text-sm text-red-600 dark:text-red-400">{{ bansError }}</p>
        <p v-if="bansLoading" class="text-sm text-muted">{{ t('common.loading') }}</p>
        <p v-else-if="!bans.length && !bansError" class="py-4 text-sm text-muted">{{ t('admin.loginProtection.empty') }}</p>
        <div v-else-if="bans.length" class="overflow-x-auto">
          <table class="w-full text-left text-sm">
            <thead class="border-b border-line text-muted"><tr>
              <th scope="col" class="px-2 py-3">IP</th><th scope="col" class="px-2 py-3">{{ t('admin.loginProtection.blockedAt') }}</th>
              <th scope="col" class="px-2 py-3">{{ t('admin.loginProtection.expiresAt') }}</th><th scope="col" class="px-2 py-3">{{ t('common.actions') }}</th>
            </tr></thead>
            <tbody><tr v-for="ban in bans" :key="ban.ip" class="border-b border-line">
              <td class="px-2 py-3 font-mono">{{ ban.ip }}</td><td class="whitespace-nowrap px-2 py-3">{{ displayDate(ban.blocked_at) }}</td>
              <td class="whitespace-nowrap px-2 py-3">{{ displayDate(ban.expires_at) }}</td>
              <td class="px-2 py-3"><button type="button" class="btn btn-secondary btn-sm" :disabled="!!unbanning || bansLoading" :aria-label="t('admin.loginProtection.unbanIP', { ip: ban.ip })" @click="unbanIP(ban.ip)">{{ t('admin.loginProtection.unban') }}</button></td>
            </tr></tbody>
          </table>
        </div>
        <Pagination :total="total" :page="page" :page-size="pageSize" :page-size-options="[20, 50, 100]" @update:page="changePage" @update:page-size="changePageSize" />
      </section>
      <TotpStepUpDialog :controller="stepUp" />
    </div>
  </AppLayout>
</template>

<script setup lang="ts">
import { computed, nextTick, onMounted, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import AppLayout from '@/components/layout/AppLayout.vue'
import Toggle from '@/components/common/Toggle.vue'
import Pagination from '@/components/common/Pagination.vue'
import TotpStepUpDialog from '@/components/auth/TotpStepUpDialog.vue'
import { isStepUpCancelled, useStepUp } from '@/composables/useStepUp'
import { useAppStore } from '@/stores/app'
import { extractApiErrorMessage } from '@/utils/apiError'
import * as api from '@/api/admin/loginProtection'
import type { LoginIPBan, LoginProtectionSettings } from '@/api/admin/loginProtection'

const { t, locale } = useI18n()
const appStore = useAppStore()
const stepUp = useStepUp()
type NumericKey = 'window_minutes' | 'max_failures' | 'ban_hours'
const fields: { key: NumericKey; max: number }[] = [{ key: 'window_minutes', max: 1440 }, { key: 'max_failures', max: 1000 }, { key: 'ban_hours', max: 720 }]
const draft = ref<LoginProtectionSettings | null>(null)
const loading = ref(false)
const saving = ref(false)
const settingsError = ref('')
const submitted = ref(false)
const errorSummary = ref<HTMLElement | null>(null)
const errors = computed(() => Object.fromEntries(fields.map(({ key, max }) => [key, draft.value && (!Number.isInteger(draft.value[key]) || draft.value[key] < 1 || draft.value[key] > max) ? t('admin.loginProtection.invalidRange', { max }) : ''])) as Record<NumericKey, string>)
const bans = ref<LoginIPBan[]>([])
const total = ref(0)
const page = ref(1)
const pageSize = ref(20)
const bansLoading = ref(false)
const bansError = ref('')
const unbanning = ref('')
let bansSequence = 0

async function loadSettings() {
  loading.value = true
  settingsError.value = ''
  try { draft.value = await api.getSettings() } catch (error) { settingsError.value = extractApiErrorMessage(error, t('admin.loginProtection.loadFailed')) } finally { loading.value = false }
}
async function save() {
  submitted.value = true
  const firstInvalid = fields.find(({ key }) => errors.value[key])
  if (firstInvalid) { await nextTick(); errorSummary.value?.focus(); return }
  if (!draft.value || saving.value) return
  const payload = { ...draft.value }
  saving.value = true
  settingsError.value = ''
  try {
    draft.value = await stepUp.run(() => api.saveSettings(payload))
    appStore.showSuccess(t('admin.loginProtection.saved'))
  } catch (error) { if (!isStepUpCancelled(error)) settingsError.value = extractApiErrorMessage(error, t('admin.loginProtection.saveFailed')) } finally { saving.value = false }
}
async function loadBans() {
  const sequence = ++bansSequence
  bansLoading.value = true
  bansError.value = ''
  try {
    const result = await api.listBans(page.value, pageSize.value)
    if (sequence !== bansSequence) return
    if (page.value > 1 && !result.items.length && result.total <= (page.value - 1) * pageSize.value) { page.value = Math.max(1, Math.ceil(result.total / pageSize.value)); await loadBans(); return }
    bans.value = result.items
    total.value = result.total
  } catch (error) { if (sequence === bansSequence) bansError.value = extractApiErrorMessage(error, t('admin.loginProtection.loadFailed')) } finally { if (sequence === bansSequence) bansLoading.value = false }
}
async function unbanIP(ip: string) {
  if (unbanning.value) return
  unbanning.value = ip
  bansError.value = ''
  try {
    await stepUp.run(() => api.unban(ip))
    appStore.showSuccess(t('admin.loginProtection.unbanned', { ip }))
    await loadBans()
  } catch (error) { if (!isStepUpCancelled(error)) bansError.value = extractApiErrorMessage(error, t('admin.loginProtection.unbanFailed')) } finally { unbanning.value = '' }
}
function changePage(value: number) { page.value = value; void loadBans() }
function changePageSize(value: number) { pageSize.value = Math.min(100, value); page.value = 1; void loadBans() }
function displayDate(value: string) { return new Date(value).toLocaleString(locale.value) }
onMounted(() => { void loadSettings(); void loadBans() })
</script>
