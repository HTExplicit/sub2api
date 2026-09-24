<template>
  <section class="space-y-3 border-b border-line p-4" data-test="account-prompt-binding">
    <div class="flex items-center justify-between gap-3"><h3 class="font-semibold">{{ text('bindings') }}</h3><button class="btn btn-secondary btn-sm" :disabled="busy" @click="load">{{ text('refreshAccounts') }}</button></div>
    <p class="text-xs text-muted">{{ text('bindingDescription') }}</p>
    <p v-if="accountIds.length > 1" class="text-xs text-muted">{{ text('bulkHint') }}</p>
    <p v-if="error" role="alert" class="text-sm text-red-600">{{ error }}</p>
    <div v-for="account in accounts" :key="account.account_id" class="flex flex-wrap gap-2 text-xs text-muted">
      <span>#{{ account.account_id }} {{ account.name }}</span><span>{{ text(account.binding.mode) }}</span><span>{{ text('effective') }}: {{ account.effective_rule_ids.join(', ') || text('none') }}</span><span v-if="!account.supported">{{ text('unsupported') }}</span>
    </div>
    <template v-if="state && accounts.length">
      <select v-model="binding.mode" class="input w-full" :disabled="busy || hasUnsupportedAccount" data-test="binding-mode"><option value="inherit">{{ text('inherit') }}</option><option value="off">{{ text('off') }}</option><option value="custom">{{ text('custom') }}</option></select>
      <fieldset v-if="binding.mode === 'custom'" class="space-y-2" :disabled="busy"><legend class="mb-2 text-sm">{{ text('selected') }}</legend><label v-for="rule in state.policy.rules" :key="rule.id" class="flex items-center gap-2 text-sm"><input v-model="binding.rule_ids" type="checkbox" :value="rule.id" />{{ rule.name }} <span class="text-xs text-muted">{{ rule.id }}{{ rule.enabled ? '' : ` · ${text('rule_disabled')}` }}</span></label></fieldset>
      <button class="btn btn-primary btn-sm" :disabled="busy || hasUnsupportedAccount" data-test="save-binding" @click="save">{{ text('saveBinding') }}</button>
    </template>
    <ul v-if="results.length" class="space-y-1 text-xs" role="status"><li v-for="result in results" :key="result.account_id" :class="result.applied ? 'text-green-700' : 'text-red-600'">#{{ result.account_id }}: {{ result.applied ? text('applied') : text(result.code || 'error') }}</li></ul>
  </section>
</template>
<script setup lang="ts">
import { computed, onBeforeUnmount, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { extractApiErrorCode, extractApiErrorMessage } from '@/utils/apiError'
import { rulesAPI, type PromptAccountBinding, type PromptBinding, type PromptBindingResult, type PromptRuleState } from '@/api/admin/systemPromptRules'
const props = defineProps<{ accountIds: number[] }>()
const emit = defineEmits<{ changed: [] }>()
const { t } = useI18n()
const text = (key: string) => t(`admin.systemPrompts.rules.${key}`)
const state = ref<PromptRuleState | null>(null), accounts = ref<PromptAccountBinding[]>([]), results = ref<PromptBindingResult[]>([])
const binding = ref<PromptBinding>({ mode: 'inherit', rule_ids: [] }), busy = ref(false), error = ref('')
const hasUnsupportedAccount = computed(() => accounts.value.some(account => !account.supported))
let generation = 0, disposed = false
const validIDs = () => [...new Set(props.accountIds)].filter(id => Number.isSafeInteger(id) && id > 0)
function fail(value: unknown) { error.value = extractApiErrorCode(value) === 'system_prompt_revision_conflict' ? text('conflict') : extractApiErrorMessage(value) || text('error') }
async function load() {
  const ids = validIDs(), current = ++generation
  if (!ids.length || ids.length > 100) { error.value = text('error'); return }
  busy.value = true; error.value = ''
  try {
    const [next, rows] = await Promise.all([rulesAPI.read(), rulesAPI.accounts(ids)])
    if (disposed || current !== generation) return
    state.value = next; accounts.value = rows
    if (rows.length === 1) binding.value = JSON.parse(JSON.stringify(rows[0]!.binding))
  } catch (value) { if (!disposed && current === generation) fail(value) } finally { if (!disposed && current === generation) busy.value = false }
}
async function save() {
  if (!state.value || busy.value) return
  const current = generation
  busy.value = true; error.value = ''; results.value = []
  const selection: PromptBinding = { mode: binding.value.mode, rule_ids: binding.value.mode === 'custom' ? [...binding.value.rule_ids] : [] }
  try {
    const next = await rulesAPI.bind(accounts.value.map(account => ({ account_id: account.account_id, expected_updated_at: account.updated_at, binding: selection })), state.value.revision)
    if (disposed || current !== generation) return
    results.value = next
    const rows = await rulesAPI.accounts(validIDs())
    if (disposed || current !== generation) return
    accounts.value = rows
    if (next.some(item => item.applied)) emit('changed')
  } catch (value) { if (!disposed && current === generation) fail(value) } finally { if (!disposed && current === generation) busy.value = false }
}
watch(() => props.accountIds.join(','), () => { results.value = []; accounts.value = []; state.value = null; binding.value = { mode: 'inherit', rule_ids: [] }; void load() }, { immediate: true })
onBeforeUnmount(() => { disposed = true; generation++ })
</script>
