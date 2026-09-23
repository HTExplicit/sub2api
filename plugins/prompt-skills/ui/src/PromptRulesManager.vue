<template>
  <section class="space-y-4" data-test="prompt-rules-manager">
    <p class="text-sm text-muted">{{ text('description') }}</p>
    <div class="flex flex-wrap items-center gap-3">
      <button class="btn btn-secondary btn-sm" :disabled="busy" @click="load">{{ text('reload') }}</button>
      <button class="btn btn-secondary btn-sm" :disabled="!state || busy" @click="addRule">{{ text('add') }}</button>
      <button class="btn btn-primary btn-sm" :disabled="!dirty || invalid || busy" data-test="save-rules" @click="save">{{ text('save') }}</button>
      <span v-if="state" class="text-xs text-muted">revision {{ state.revision }}</span>
    </div>
    <p v-if="error" role="alert" class="text-sm text-red-600">{{ error }}</p>
    <p v-if="invalid" role="status" class="text-sm text-amber-700">{{ text('invalid') }}</p>
    <p class="text-xs text-muted">{{ text('nativeHint') }}</p>
    <p class="text-xs text-muted">{{ text('defaultHint') }}</p>
    <details class="rounded border border-line p-3" data-test="prompt-support-matrix">
      <summary class="cursor-pointer text-sm font-medium">{{ text('supportMatrix') }}</summary>
      <p class="mt-2 text-xs text-muted">{{ text('supportMatrixHint') }}</p>
      <div class="mt-3 overflow-x-auto">
        <table class="w-full text-left text-xs">
          <thead><tr class="border-b border-line"><th class="p-2">{{ text('finalProtocol') }}</th><th class="p-2">{{ text('transport') }}</th><th class="p-2">native_control</th><th class="p-2">system</th><th class="p-2">developer</th></tr></thead>
          <tbody>
            <tr class="border-b border-line"><th class="p-2">{{ text('matrixCodex') }}</th><td class="p-2">HTTP / WS</td><td class="p-2 font-mono">instructions</td><td class="p-2">{{ text('matrixRejected') }}</td><td class="p-2 font-mono">input[].role=developer</td></tr>
            <tr class="border-b border-line"><th class="p-2">{{ text('matrixResponses') }}</th><td class="p-2">HTTP / WS</td><td class="p-2 font-mono">instructions</td><td class="p-2 font-mono">input[].role=system</td><td class="p-2 font-mono">input[].role=developer</td></tr>
            <tr><th class="p-2">{{ text('matrixChat') }}</th><td class="p-2">HTTP</td><td class="p-2 font-mono">messages[].role=system</td><td class="p-2 font-mono">messages[].role=system</td><td class="p-2 font-mono">messages[].role=developer</td></tr>
          </tbody>
        </table>
      </div>
      <p class="mt-3 text-xs text-muted">{{ text('matrixPositions') }}</p>
      <p class="mt-2 text-xs text-muted">{{ text('matrixConversions') }}</p>
    </details>
    <p v-if="hasLegacyRule" class="text-xs text-muted">{{ text('migration') }}</p>
    <article v-for="(rule, index) in draft.rules" :key="rule.id" class="space-y-3 border border-line p-4" :data-test="`prompt-rule-${rule.id}`">
      <div class="flex flex-wrap items-center gap-3">
        <label class="flex items-center gap-2 text-sm"><input v-model="rule.enabled" type="checkbox" />{{ text('enabled') }}</label>
        <label class="flex items-center gap-2 text-sm"><input v-model="draft.default_rule_ids" type="checkbox" :value="rule.id" />{{ text('default') }}</label>
        <span class="flex-1 truncate text-xs text-muted">{{ rule.id }}</span>
        <button class="btn btn-secondary btn-sm" :disabled="index === 0" :aria-label="text('orderUp')" @click="move(index, -1)">↑</button>
        <button class="btn btn-secondary btn-sm" :disabled="index === draft.rules.length - 1" :aria-label="text('orderDown')" @click="move(index, 1)">↓</button>
        <button class="btn btn-secondary btn-sm" @click="remove(rule.id)">{{ text('remove') }}</button>
      </div>
      <label class="block text-sm">{{ text('name') }}<input v-model="rule.name" class="input mt-1 w-full" maxlength="200" /></label>
      <label class="flex items-center gap-2 text-sm"><input v-model="rule.follow_active" type="checkbox" />{{ text('follow') }}</label>
      <div v-if="!rule.follow_active" class="grid gap-3 sm:grid-cols-2">
        <label class="text-sm">{{ text('template') }}<select v-model.number="rule.template_id" class="input mt-1 w-full" @change="selectTemplate(rule)"><option :value="0">—</option><option v-for="template in templates" :key="template.id" :value="template.id">{{ template.name }}</option></select></label>
        <label class="text-sm">{{ text('version') }}<select v-model.number="rule.version_id" class="input mt-1 w-full"><option :value="0">—</option><option v-for="version in versions[rule.template_id] || []" :key="version.id" :value="version.id">v{{ version.version }} · {{ version.note }}</option></select></label>
      </div>
      <div class="grid gap-3 sm:grid-cols-2">
        <label class="text-sm">{{ text('delivery') }}<select v-model="rule.delivery" class="input mt-1 w-full"><option v-for="delivery in deliveries" :key="delivery" :value="delivery" :disabled="!legalPromptPosition(delivery, rule.position)">{{ text(delivery) }}</option></select></label>
        <label class="text-sm">{{ text('position') }}<select v-model="rule.position" class="input mt-1 w-full"><option v-for="position in positions" :key="position" :value="position" :disabled="!legalPromptPosition(rule.delivery, position)">{{ text(position) }}</option></select></label>
      </div>
      <label class="block text-sm">{{ text('modelMatch') }}<select v-model="rule.model_match" class="input ml-3"><option value="upstream">{{ text('upstream') }}</option><option value="requested">{{ text('requested') }}</option></select></label>
      <label class="block text-sm">{{ text('models') }}<textarea :value="rule.models.join('\n')" class="input mt-1 min-h-20 w-full font-mono" @input="rule.models = parseModels(($event.target as HTMLTextAreaElement).value)" /></label>
    </article>
    <p v-if="state && !draft.rules.length" class="text-sm text-muted">{{ text('empty') }}</p>
    <section class="space-y-3 border-t border-line pt-5" data-test="prompt-rules-preview">
      <h2 class="font-semibold">{{ text('preview') }}</h2>
      <p class="text-xs text-muted">{{ text('previewDraft') }}</p>
      <div class="grid gap-3 sm:grid-cols-2">
        <label class="text-sm">{{ text('account') }}<input v-model.number="accountId" type="number" min="1" class="input mt-1 w-full" /></label>
        <label class="text-sm">{{ text('previewModel') }}<input v-model="previewModel" class="input mt-1 w-full font-mono" data-test="preview-model" /></label>
        <label class="text-sm">{{ text('ingress') }}<select v-model="protocol" class="input mt-1 w-full"><option value="responses">Responses</option><option value="chat">Chat Completions</option><option value="messages">Messages</option></select></label>
        <label class="text-sm">{{ text('transport') }}<select v-model="transport" class="input mt-1 w-full"><option value="http">HTTP</option><option value="ws">WebSocket</option></select></label>
      </div>
      <div class="flex flex-wrap gap-4 text-sm"><label><input v-model="compact" type="checkbox" /> {{ text('compact') }}</label><label><input v-model="simulate" type="checkbox" /> {{ text('simulate') }}</label></div>
      <label class="block text-sm">{{ text('sample') }}<textarea v-model="sample" class="input mt-1 min-h-40 w-full font-mono text-xs" /></label>
      <button class="btn btn-secondary btn-sm" :disabled="busy || !state || invalid || accountId < 1" @click="preview">{{ text('runPreview') }}</button>
      <template v-if="result">
        <div class="flex flex-wrap gap-3 text-xs text-muted"><span>{{ result.protocol }} / {{ result.transport }}</span><span>{{ result.requested_model }} → {{ result.upstream_model }}</span><span>{{ text('verified') }}</span><span v-if="result.simulated">{{ text('simulated') }}</span></div>
        <p class="text-xs text-amber-700">{{ text('sequenceHint') }}</p>
        <h3 class="text-sm font-semibold">{{ text('site') }}</h3>
        <ol class="space-y-2"><li v-for="placement in result.application.rules_plan?.placements || []" :key="placement.rule_id" class="border border-line p-3 text-sm"><strong>{{ placement.rule_id }}</strong> · {{ text(placement.position) }} · <code>{{ placement.carrier }}{{ placement.role ? ` / role=${placement.role}` : '' }}</code><pre class="mt-2 max-h-44 overflow-auto whitespace-pre-wrap text-xs">{{ placement.body }}</pre></li></ol>
        <p v-if="!result.application.applied" class="text-sm text-muted">{{ text('noRules') }}</p>
        <p v-for="skip in result.application.rules_plan?.skipped || []" :key="skip.rule_id" class="text-xs text-muted">{{ skip.rule_id }} — {{ text('skipped') }}: {{ text(skip.reason) }}</p>
        <details><summary>{{ text('base') }}</summary><pre class="max-h-56 overflow-auto whitespace-pre-wrap text-xs">{{ result.gateway_base_instructions || text('noBase') }}</pre></details>
        <details><summary>{{ text('client') }}</summary><pre class="max-h-56 overflow-auto whitespace-pre-wrap text-xs">{{ pretty(result.client_control) }}</pre></details>
        <details><summary>{{ text('before') }}</summary><pre class="max-h-72 overflow-auto whitespace-pre-wrap text-xs">{{ pretty(result.before_rules) }}</pre></details>
        <details open><summary>{{ text('final') }}</summary><pre class="max-h-[520px] overflow-auto whitespace-pre-wrap border border-line p-3 text-xs">{{ pretty(result.body) }}</pre></details>
      </template>
    </section>
  </section>
</template>

<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { extractApiErrorCode, extractApiErrorMessage, useNotifications } from '@sub2api/plugin-ui'
import promptAPI, { type SystemPromptTemplate, type SystemPromptVersion } from './api'
import { legalPromptPosition, rulesAPI, type PromptDelivery, type PromptPosition, type PromptPreview, type PromptRule, type PromptRulePolicy, type PromptRuleState } from './rules-api'
const { t } = useI18n()
const text = (key: string) => t(`admin.systemPrompts.rules.${key}`)
const notifications = useNotifications()
const emit = defineEmits<{ saved: [] }>()
const state = ref<PromptRuleState | null>(null), draft = ref<PromptRulePolicy>({ version: 1, rules: [], default_rule_ids: [] })
const templates = ref<SystemPromptTemplate[]>([]), versions = ref<Record<number, SystemPromptVersion[]>>({})
const busy = ref(false), error = ref(''), accountId = ref(0), compact = ref(false), simulate = ref(false)
const protocol = ref('responses'), transport = ref('http'), sample = ref('{"model":"gpt-6-astra","instructions":"Client instructions","input":[{"role":"user","content":"Hello"}]}')
const previewModel = computed({
  get: () => { try { const value = JSON.parse(sample.value); return typeof value?.model === 'string' ? value.model : '' } catch { return '' } },
  set: (model: string) => {
    try {
      const value = JSON.parse(sample.value)
      if (!value || typeof value !== 'object' || Array.isArray(value)) throw new Error('invalid request')
      sample.value = JSON.stringify({ ...value, model }, null, 2)
    } catch { error.value = text('invalidJSON') }
  }
})
const result = ref<PromptPreview | null>(null)
const deliveries: PromptDelivery[] = ['native_control', 'system', 'developer']
const positions: PromptPosition[] = ['control_prepend', 'control_append', 'conversation_head', 'conversation_tail']
let disposed = false, generation = 0
const pretty = (value: unknown) => JSON.stringify(value, null, 2)
const dirty = computed(() => state.value !== null && pretty(draft.value) !== pretty(state.value.policy))
const hasLegacyRule = computed(() => draft.value.rules.some(rule => rule.id === 'legacy-default'))
const invalid = computed(() => draft.value.rules.some(rule => !rule.name.trim() || (!rule.follow_active && (!rule.template_id || !rule.version_id)) || !legalPromptPosition(rule.delivery, rule.position)))
const parseModels = (value: string) => [...new Set(value.split(/\r?\n/).map(item => item.trim()).filter(Boolean))]
function fail(value: unknown) { error.value = extractApiErrorCode(value) === 'system_prompt_revision_conflict' ? text('conflict') : extractApiErrorMessage(value) || text('error') }
async function load() {
  if (busy.value) return
  const current = ++generation
  busy.value = true; error.value = ''
  try {
    const [next, catalog] = await Promise.all([rulesAPI.read(), promptAPI.list()])
    if (disposed || current !== generation) return
    const referenced = [...new Set(next.policy.rules.filter(rule => !rule.follow_active).map(rule => rule.template_id))]
    const details = await Promise.all(referenced.map(id => promptAPI.listVersions(id).then(items => [id, items] as const)))
    if (disposed || current !== generation) return
    state.value = next; draft.value = JSON.parse(JSON.stringify(next.policy)); templates.value = catalog.templates; versions.value = Object.fromEntries(details)
  } catch (value) { if (!disposed && current === generation) fail(value) } finally { if (!disposed && current === generation) busy.value = false }
}
function addRule() { draft.value.rules.push({ id: `rule-${crypto.randomUUID().slice(0, 12)}`, name: '', enabled: true, template_id: 0, version_id: 0, order: (draft.value.rules.length + 1) * 100, delivery: 'native_control', position: 'control_append', model_match: 'upstream', models: [] }) }
function reorder() { draft.value.rules.forEach((rule, index) => { rule.order = (index + 1) * 100 }) }
function move(index: number, direction: number) { const [rule] = draft.value.rules.splice(index, 1); if (rule) draft.value.rules.splice(index + direction, 0, rule); reorder() }
function remove(id: string) { draft.value.rules = draft.value.rules.filter(rule => rule.id !== id); draft.value.default_rule_ids = draft.value.default_rule_ids.filter(ref => ref !== id); reorder() }
async function selectTemplate(rule: PromptRule) {
  rule.version_id = 0
  if (!rule.template_id || versions.value[rule.template_id]) return
  const id = rule.template_id
  try { const items = await promptAPI.listVersions(id); if (!disposed) versions.value = { ...versions.value, [id]: items } } catch (value) { if (!disposed) fail(value) }
}
async function save() {
  if (!state.value || busy.value || invalid.value) return
  busy.value = true; error.value = ''
  try { const next = await rulesAPI.save(JSON.parse(JSON.stringify(draft.value)), state.value.revision); if (!disposed) { state.value = next; draft.value = JSON.parse(JSON.stringify(next.policy)); notifications.showSuccess(text('saved')); emit('saved') } } catch (value) { if (!disposed) fail(value) } finally { if (!disposed) busy.value = false }
}
async function preview() {
  if (!state.value || accountId.value < 1 || busy.value) return
  let body: unknown
  try { body = JSON.parse(sample.value) } catch { error.value = text('invalidJSON'); return }
  busy.value = true; error.value = ''; result.value = null
  try { const next = await rulesAPI.preview(accountId.value, { protocol: protocol.value, transport: transport.value, compact: compact.value, body, policy: JSON.parse(JSON.stringify(draft.value)), ...(simulate.value ? { simulate_enabled: true } : {}) }); if (!disposed) result.value = next } catch (value) { if (!disposed) fail(value) } finally { if (!disposed) busy.value = false }
}
onMounted(load)
onBeforeUnmount(() => { disposed = true; generation++ })
</script>
