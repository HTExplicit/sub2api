<template>
  <section class="space-y-4" data-test="prompt-rules-manager">
    <p class="text-sm text-muted">{{ text('description') }}</p>
    <div class="grid min-w-0 gap-4 lg:grid-cols-[240px_minmax(0,1fr)]">
      <aside class="min-w-0 border border-line">
        <div class="flex items-center justify-between border-b border-line p-3">
          <h2 class="text-sm font-semibold">{{ text('list') }}</h2>
          <button type="button" class="btn btn-secondary btn-sm" data-test="add-prompt" @click="emit('add')">{{ text('add') }}</button>
        </div>
        <div class="max-h-[65vh] overflow-auto p-2">
          <button v-for="rule in config.policy.rules" :key="rule.id" type="button" class="mb-1 flex w-full items-start gap-2 border-l-2 p-3 text-left" :class="selectedId === rule.id ? 'border-primary-500 bg-primary-50 dark:bg-primary-900/20' : 'border-transparent hover:bg-gray-50 dark:hover:bg-dark-800'" :data-test="`select-prompt-${rule.id}`" :aria-current="selectedId === rule.id ? 'true' : undefined" @click="emit('select', rule.id)">
            <span class="min-w-0 flex-1"><span class="block truncate text-sm font-medium">{{ rule.name || text('unnamed') }}</span><span class="mt-1 block text-xs text-muted">{{ rule.enabled ? text('enabled') : text('rule_disabled') }}<span v-if="config.policy.default_rule_ids.includes(rule.id)"> · {{ text('default') }}</span></span></span>
            <span v-if="dirtyIds.includes(rule.id)" class="text-amber-600" :aria-label="text('unsaved')">●</span>
          </button>
          <p v-if="!config.policy.rules.length" class="px-2 py-5 text-sm text-muted">{{ text('empty') }}</p>
        </div>
      </aside>
      <article v-if="selected && content" class="min-w-0 space-y-4 border border-line p-4" :data-test="`prompt-rule-${selected.id}`">
        <div class="flex flex-wrap items-center gap-3">
          <label class="flex items-center gap-2 text-sm"><input v-model="selected.enabled" type="checkbox" data-test="rule-enabled" />{{ text('enabled') }}</label>
          <label class="flex items-center gap-2 text-sm"><input v-model="config.policy.default_rule_ids" type="checkbox" :value="selected.id" data-test="rule-default" />{{ text('default') }}</label>
          <div class="ml-auto flex gap-2">
            <button type="button" class="btn btn-secondary btn-sm" :disabled="selectedIndex === 0" :aria-label="text('orderUp')" @click="move(-1)">↑</button>
            <button type="button" class="btn btn-secondary btn-sm" :disabled="selectedIndex === config.policy.rules.length - 1" :aria-label="text('orderDown')" @click="move(1)">↓</button>
            <button type="button" class="btn btn-secondary btn-sm" data-test="duplicate-prompt" :disabled="content.available === false" @click="emit('duplicate')">{{ text('copyIndependent') }}</button>
            <button type="button" class="btn btn-secondary btn-sm" data-test="remove-prompt" @click="emit('remove', selected.id)">{{ text('remove') }}</button>
          </div>
        </div>
        <label class="block text-sm">{{ text('name') }}<input v-model="selected.name" data-test="prompt-name" class="input mt-1 w-full" maxlength="200" /></label>
        <label v-if="content.available !== false" class="block text-sm">{{ text('body') }}<textarea v-model="content.body" data-test="system-prompt-body" :readonly="content.managed" class="input mt-1 min-h-[330px] w-full resize-y font-mono text-[13px] leading-6" spellcheck="false" /></label>
        <p v-else class="border border-amber-200 bg-amber-50 p-4 text-sm text-amber-800 dark:border-amber-800 dark:bg-amber-900/20" data-test="managed-source-unavailable">{{ text('sourceUnavailable') }}</p>
        <p v-if="content.managed" class="text-xs text-muted" data-test="managed-prompt-hint">{{ text('managedHint') }}</p>
        <div class="grid gap-3 sm:grid-cols-2">
          <label class="text-sm">{{ text('role') }}<select v-model="selected.role" data-test="prompt-role" class="input mt-1 w-full"><option v-for="role in roles" :key="role" :value="role" :disabled="!allows(role, selected.position)">{{ text(role) }}</option></select></label>
          <label class="text-sm">{{ text('position') }}<select v-model="selected.position" data-test="prompt-position" class="input mt-1 w-full"><option v-for="position in positions" :key="position" :value="position" :disabled="!allows(selected.role, position)">{{ text(position) }}</option></select></label>
        </div>
        <fieldset class="space-y-2"><legend class="text-sm font-medium">{{ text('platforms') }}</legend><p class="text-xs text-muted">{{ text('platformHint') }}</p><div class="flex flex-wrap gap-x-4 gap-y-2"><label v-for="platform in platforms" :key="platform" class="flex items-center gap-1.5 text-sm"><input v-model="selected.platforms" type="checkbox" :value="platform" :data-test="`platform-${platform}`" />{{ platformName(platform) }}</label></div></fieldset>
        <div class="grid gap-3 sm:grid-cols-[minmax(180px,1fr)_2fr]">
          <label class="text-sm">{{ text('modelMatch') }}<select v-model="selected.model_match" class="input mt-1 w-full"><option value="upstream">{{ text('upstream') }}</option><option value="requested">{{ text('requested') }}</option></select></label>
          <label class="text-sm">{{ text('models') }}<textarea :value="selected.models.join('\n')" data-test="prompt-models" class="input mt-1 min-h-20 w-full font-mono text-xs" @input="selected.models = parseModels(($event.target as HTMLTextAreaElement).value)" /></label>
        </div>
        <details class="text-xs"><summary class="cursor-pointer text-muted">{{ text('accountTypes') }}</summary><p class="my-2 text-muted">{{ text('accountTypesHint') }}</p><div class="flex flex-wrap gap-3"><label v-for="type in accountTypes" :key="type"><input :checked="selected.account_types?.includes(type) || false" type="checkbox" @change="toggleAccountType(type, ($event.target as HTMLInputElement).checked)" /> {{ type }}</label></div><p v-if="selected.request_profiles?.length" class="mt-2 text-muted">{{ text('retainedClientScope') }}: {{ selected.request_profiles.join(', ') }}</p><p v-if="selected.exclude_model_contains?.length" class="mt-2 text-muted">{{ text('retainedModelExclusions') }}: {{ selected.exclude_model_contains.join(', ') }}</p></details>
        <p v-if="invalidReason" class="text-sm text-amber-700" role="status" data-test="prompt-invalid">{{ text(invalidReason) }}</p>
        <p class="text-xs text-muted">{{ text('defaultHint') }}</p>
        <div class="flex flex-wrap items-center justify-between gap-2 border-t border-line pt-3 text-xs text-muted"><span>{{ text('reference') }}: {{ selected.id }} · {{ selected.template_id || '—' }}/{{ selected.version_id || '—' }}</span><button type="button" class="btn btn-secondary btn-sm" data-test="system-prompt-open-advanced" @click="emit('advanced')">{{ text('advanced') }}</button></div>
      </article>
      <p v-else class="border border-dashed border-line p-10 text-center text-sm text-muted">{{ text('selectPrompt') }}</p>
    </div>
    <details class="border border-line p-4" data-test="prompt-rules-preview">
      <summary class="cursor-pointer font-semibold">{{ text('preview') }}</summary>
      <div class="mt-4 space-y-3">
        <p class="text-xs text-muted">{{ text('previewDraft') }}</p>
        <div class="grid gap-3 sm:grid-cols-2">
          <label class="text-sm">{{ text('account') }}<input v-model.number="accountId" type="number" min="1" class="input mt-1 w-full" /></label>
          <label class="text-sm">{{ text('previewModel') }}<input v-model="previewModel" class="input mt-1 w-full font-mono" data-test="preview-model" /></label>
          <label class="text-sm">{{ text('ingress') }}<select v-model="protocol" class="input mt-1 w-full"><option value="responses">Responses</option><option value="chat">Chat Completions</option><option value="messages">Claude Messages</option><option value="gemini">Gemini</option></select></label>
          <label class="text-sm">{{ text('transport') }}<select v-model="transport" class="input mt-1 w-full"><option value="http">HTTP</option><option value="ws">WebSocket</option></select></label>
        </div>
        <div class="flex flex-wrap gap-4 text-sm"><label><input v-model="compact" type="checkbox" /> {{ text('compact') }}</label><label><input v-model="simulate" type="checkbox" /> {{ text('simulate') }}</label></div>
        <label class="block text-sm">{{ text('sample') }}<textarea v-model="sample" class="input mt-1 min-h-40 w-full font-mono text-xs" /></label>
        <p v-if="previewError" role="alert" class="text-sm text-red-600">{{ previewError }}</p>
        <button type="button" class="btn btn-secondary btn-sm" data-test="run-prompt-preview" :disabled="previewBusy || busy || invalid || accountId < 1" @click="preview">{{ text('runPreview') }}</button>
        <template v-if="result">
          <div class="flex flex-wrap gap-3 text-xs text-muted"><span>{{ result.protocol }} / {{ result.transport }}</span><span>{{ result.requested_model }} → {{ result.upstream_model }}</span><span v-if="result.wire_verified">{{ text('verified') }}</span><span v-if="result.simulated">{{ text('simulated') }}</span></div>
          <p class="text-xs text-amber-700">{{ text('sequenceHint') }}</p>
          <h3 class="text-sm font-semibold">{{ text('site') }}</h3>
          <ol class="space-y-2"><li v-for="(placement, index) in result.application.rules_plan?.placements || []" :key="`${placement.rule_id}-${index}`" class="border border-line p-3 text-sm"><strong>{{ placement.rule_id }}</strong> · {{ reasonText(placement.position) }} · <code>{{ placement.carrier }}{{ placement.role ? ` / role=${placement.role}` : '' }}{{ placement.index === undefined ? '' : ` [${placement.index}]` }}</code><pre class="mt-2 max-h-44 overflow-auto whitespace-pre-wrap text-xs">{{ placement.body }}</pre></li></ol>
          <p v-if="!result.application.applied" class="text-sm text-muted">{{ text('noRules') }}</p>
          <p v-for="skip in result.application.rules_plan?.skipped || []" :key="skip.rule_id" class="text-xs text-muted">{{ skip.rule_id }} — {{ text('skipped') }}: {{ reasonText(skip.reason) }}</p>
          <details open><summary>{{ text('topInstructions') }}</summary><pre class="max-h-72 overflow-auto whitespace-pre-wrap border border-line p-3 text-xs">{{ pretty(topInstructions) }}</pre></details>
          <details open><summary>{{ text('messageSequence') }}</summary><pre class="max-h-72 overflow-auto whitespace-pre-wrap border border-line p-3 text-xs">{{ pretty(messageSequence) }}</pre></details>
          <details><summary>{{ text('base') }}</summary><pre class="max-h-56 overflow-auto whitespace-pre-wrap text-xs">{{ result.gateway_base_instructions || text('noBase') }}</pre></details>
          <details><summary>{{ text('client') }}</summary><pre class="max-h-56 overflow-auto whitespace-pre-wrap text-xs">{{ pretty(result.client_control) }}</pre></details>
          <details><summary>{{ text('before') }}</summary><pre class="max-h-72 overflow-auto whitespace-pre-wrap text-xs">{{ pretty(result.before_rules) }}</pre></details>
          <details><summary>{{ text('final') }}</summary><pre class="max-h-[520px] overflow-auto whitespace-pre-wrap text-xs">{{ pretty(result.body) }}</pre></details>
        </template>
      </div>
    </details>
    <details class="border border-line p-4" data-test="prompt-support-matrix">
      <summary class="cursor-pointer text-sm font-medium">{{ text('supportMatrix') }}</summary>
      <p class="mt-2 text-xs text-muted">{{ text('supportMatrixHint') }}</p>
      <div class="mt-3 overflow-x-auto"><table class="w-full text-left text-xs"><thead><tr class="border-b border-line"><th class="p-2">{{ text('finalProtocol') }}</th><th v-for="role in roles" :key="role" class="p-2">{{ text(role) }}</th></tr></thead><tbody><tr v-for="capability in config.capabilities" :key="capability.protocol" class="border-b border-line"><th class="p-2">{{ capability.protocol }}<span class="block font-normal text-muted">{{ capability.platforms.map(platformName).join(', ') }}</span></th><td v-for="role in roles" :key="role" class="p-2">{{ capability.positions_by_role[role]?.map(reasonText).join(' · ') || text('matrixRejected') }}</td></tr></tbody></table></div>
      <p class="mt-3 text-xs text-muted">{{ text('nativeHint') }}</p>
    </details>
  </section>
</template>

<script setup lang="ts">
import { computed, onBeforeUnmount, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { extractApiErrorMessage } from '@/utils/apiError'
import { legalPromptPosition, rulesAPI, type PromptConfig, type PromptPosition, type PromptPreview, type PromptRole } from '@/api/admin/systemPromptRules'

const config = defineModel<PromptConfig>({ required: true })
const props = defineProps<{ selectedId: string; dirtyIds: string[]; contentEdits: Record<string, { body: string }>; invalid: boolean; invalidReason: string; busy: boolean }>()
const emit = defineEmits<{ select: [id: string]; add: []; remove: [id: string]; duplicate: []; advanced: [] }>()
const { t, te } = useI18n()
const text = (key: string) => t(`admin.systemPrompts.rules.${key}`)
const reasonText = (key: string) => te(`admin.systemPrompts.rules.${key}`) ? text(key) : key
const selected = computed(() => config.value.policy.rules.find(rule => rule.id === props.selectedId))
const content = computed(() => config.value.contents[props.selectedId])
const selectedIndex = computed(() => config.value.policy.rules.findIndex(rule => rule.id === props.selectedId))
const roles: PromptRole[] = ['auto', 'system', 'developer']
const positions: PromptPosition[] = ['control_prepend', 'control_append', 'conversation_head', 'conversation_tail', 'before_last_user', 'after_last_user']
const accountTypes = ['apikey', 'oauth', 'setup-token', 'bedrock', 'service_account', 'upstream']
const platforms = computed(() => [...new Set(config.value.capabilities.flatMap(capability => capability.platforms))])
const platformNames: Record<string, string> = { anthropic: 'Claude', openai: 'OpenAI', gemini: 'Gemini', antigravity: 'Antigravity', grok: 'Grok', cindy: 'Cindy', kimi: 'Kimi', zhipu: 'Zhipu', deepseek: 'DeepSeek', minimax: 'MiniMax', opencode_go: 'OpenCode Go' }
const platformName = (platform: string) => platformNames[platform] || platform
const allows = (role: PromptRole, position: PromptPosition) => {
  if (role === 'system' && selected.value?.platforms.some(platform => ['openai', 'cindy'].includes(platform)) && selected.value.account_types?.some(type => ['oauth', 'setup-token'].includes(type))) return false
  return !selected.value?.platforms.length || legalPromptPosition(role, position, selected.value.platforms, config.value.capabilities, selected.value.account_types)
}
const parseModels = (value: string) => [...new Set(value.split(/\r?\n/).map(item => item.trim()).filter(Boolean))]
function move(direction: number) {
  const index = selectedIndex.value
  const [rule] = config.value.policy.rules.splice(index, 1)
  if (rule) config.value.policy.rules.splice(index + direction, 0, rule)
  config.value.policy.rules.forEach((rule, order) => { rule.order = (order + 1) * 100 })
}
function toggleAccountType(type: string, checked: boolean) {
  if (!selected.value) return
  const types = new Set(selected.value.account_types || [])
  if (checked) types.add(type)
  else types.delete(type)
  selected.value.account_types = [...types]
}
const accountId = ref(0), protocol = ref('responses'), transport = ref('http'), compact = ref(false), simulate = ref(false)
const sample = ref('{"model":"gpt-6-astra","instructions":"Client instructions","input":[{"role":"user","content":"Hello"}]}')
const previewBusy = ref(false), previewError = ref(''), result = ref<PromptPreview | null>(null)
let disposed = false
const pretty = (value: unknown) => JSON.stringify(value, null, 2)
const previewModel = computed({
  get: () => { try { return JSON.parse(sample.value)?.model || '' } catch { return '' } },
  set: (model: string) => { try { const body = JSON.parse(sample.value); if (!body || typeof body !== 'object' || Array.isArray(body)) throw new Error(); sample.value = pretty({ ...body, model }) } catch { previewError.value = text('invalidJSON') } },
})
watch(protocol, value => {
  const model = previewModel.value || 'model-id'
  if (value === 'messages') sample.value = pretty({ model, max_tokens: 128, system: 'Client instructions', messages: [{ role: 'user', content: 'Hello' }] })
  else if (value === 'gemini') sample.value = pretty({ model, systemInstruction: { parts: [{ text: 'Client instructions' }] }, contents: [{ role: 'user', parts: [{ text: 'Hello' }] }] })
  else if (value === 'chat') sample.value = pretty({ model, messages: [{ role: 'system', content: 'Client instructions' }, { role: 'user', content: 'Hello' }] })
  else sample.value = pretty({ model, instructions: 'Client instructions', input: [{ role: 'user', content: 'Hello' }] })
  result.value = null
})
const finalBody = computed(() => result.value?.body && typeof result.value.body === 'object' ? result.value.body as Record<string, unknown> : {})
watch([() => config.value.policy, () => config.value.contents, sample, transport, compact, simulate, accountId], () => { result.value = null }, { deep: true })
const topInstructions = computed(() => Object.fromEntries(['instructions', 'system', 'systemInstruction'].filter(key => key in finalBody.value).map(key => [key, finalBody.value[key]])))
const messageSequence = computed(() => Object.fromEntries(['input', 'messages', 'contents'].filter(key => key in finalBody.value).map(key => [key, finalBody.value[key]])))
async function preview() {
  let body: unknown
  try { body = JSON.parse(sample.value) } catch { previewError.value = text('invalidJSON'); return }
  previewBusy.value = true; previewError.value = ''; result.value = null
  try {
    const next = await rulesAPI.preview(accountId.value, { protocol: protocol.value, transport: transport.value, compact: compact.value, body, policy: JSON.parse(JSON.stringify(config.value.policy)), contents: JSON.parse(JSON.stringify(props.contentEdits)), simulate_enabled: simulate.value || config.value.enabled })
    if (!disposed) result.value = next
  } catch (error) { if (!disposed) previewError.value = extractApiErrorMessage(error) || text('error') }
  finally { if (!disposed) previewBusy.value = false }
}
onBeforeUnmount(() => { disposed = true })
</script>
