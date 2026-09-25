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
            <button type="button" class="btn btn-secondary btn-sm" data-test="duplicate-prompt" :disabled="content.composition_mode !== 'inline'" @click="emit('duplicate')">{{ text('copyIndependent') }}</button>
            <button type="button" class="btn btn-secondary btn-sm" data-test="remove-prompt" @click="emit('remove', selected.id)">{{ text('remove') }}</button>
          </div>
        </div>
        <label class="block text-sm">{{ text('name') }}<input v-model="selected.name" data-test="prompt-name" class="input mt-1 w-full" maxlength="200" /></label>
        <label v-if="content.composition_mode === 'inline'" class="block text-sm">{{ text('body') }}<textarea v-model="content.body" data-test="system-prompt-body" class="input mt-1 min-h-[330px] w-full resize-y font-mono text-[13px] leading-6" spellcheck="false" /></label>
        <fieldset v-else-if="structured" class="space-y-3" data-test="structured-prompt-editor"><legend class="text-sm">{{ text('body') }}</legend>
          <label v-for="(block, index) in structured.blocks" :key="index" class="block text-sm">{{ text('textBlock') }} {{ index + 1 }}<textarea v-if="block" :value="block.text || ''" :data-test="`prompt-block-${index}`" class="input mt-1 min-h-28 w-full font-mono text-[13px]" spellcheck="false" @input="editBlock(index, ($event.target as HTMLTextAreaElement).value)" /></label>
          <label v-if="typeof structured.expansion_prompt === 'string'" class="block text-sm">{{ text('expansionText') }}<textarea :value="structured.expansion_prompt" data-test="prompt-expansion" class="input mt-1 min-h-28 w-full font-mono text-[13px]" spellcheck="false" @input="editExpansion(($event.target as HTMLTextAreaElement).value)" /></label>
          <p class="text-xs text-muted">{{ text('structuredHint') }}</p>
        </fieldset>
        <div class="grid gap-3 sm:grid-cols-2">
          <label class="text-sm">{{ text('role') }}<select v-model="selected.role" data-test="prompt-role" class="input mt-1 w-full"><option v-for="role in roles" :key="role" :value="role" :disabled="!allows(role, selected.position)">{{ text(role) }}</option></select></label>
          <label class="text-sm">{{ text('position') }}<select v-model="selected.position" data-test="prompt-position" class="input mt-1 w-full"><option v-for="position in positions" :key="position" :value="position" :disabled="!allows(selected.role, position)">{{ text(position) }}</option></select></label>
        </div>
        <fieldset class="space-y-2"><legend class="text-sm font-medium">{{ text('platforms') }}</legend><p class="text-xs text-muted">{{ text('platformHint') }}</p><div class="flex flex-wrap gap-x-4 gap-y-2"><label v-for="platform in platforms" :key="platform" class="flex items-center gap-1.5 text-sm"><input v-model="selected.platforms" type="checkbox" :value="platform" :data-test="`platform-${platform}`" />{{ platformName(platform) }}</label></div></fieldset>
        <p v-if="scopeSummary" class="text-xs text-muted" data-test="prompt-scope-summary">{{ text('scopeRestrictions') }}: {{ scopeSummary }}</p>
        <details :key="selected.id" class="space-y-3 text-sm" data-test="prompt-detailed-scope">
          <summary class="cursor-pointer text-muted">{{ text('detailedScope') }}</summary>
          <div class="grid gap-3 sm:grid-cols-[minmax(180px,1fr)_2fr]">
            <label>{{ text('modelMatch') }}<select v-model="selected.model_match" class="input mt-1 w-full"><option value="upstream">{{ text('upstream') }}</option><option value="requested">{{ text('requested') }}</option></select></label>
            <label>{{ text('models') }}<textarea :value="selected.models.join('\n')" data-test="prompt-models" class="input mt-1 min-h-20 w-full font-mono text-xs" @input="selected.models = parseModels(($event.target as HTMLTextAreaElement).value)" /></label>
          </div>
          <p class="text-xs text-muted">{{ text('accountTypesHint') }}</p>
          <div class="flex flex-wrap gap-3"><label v-for="type in accountTypes" :key="type"><input :checked="selected.account_types?.includes(type) || false" type="checkbox" @change="toggleAccountType(type, ($event.target as HTMLInputElement).checked)" /> {{ type }}</label></div>
        </details>
        <p v-if="invalidReason" class="text-sm text-amber-700" role="status" data-test="prompt-invalid">{{ text(invalidReason) }}</p>
        <p class="text-xs text-muted">{{ text('defaultHint') }}</p>
        <div class="border-t border-line pt-3"><button type="button" class="btn btn-secondary btn-sm" data-test="prompt-open-history" @click="emit('history')">{{ text('history') }}</button></div>
      </article>
      <p v-else class="border border-dashed border-line p-10 text-center text-sm text-muted">{{ text('selectPrompt') }}</p>
    </div>
  </section>
</template>

<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import { legalPromptPosition, type PromptConfig, type PromptPosition, type PromptRole } from '@/api/admin/systemPromptRules'

const config = defineModel<PromptConfig>({ required: true })
const props = defineProps<{ selectedId: string; dirtyIds: string[]; invalidReason: string }>()
const emit = defineEmits<{ select: [id: string]; add: []; remove: [id: string]; duplicate: []; history: [] }>()
const { t } = useI18n()
const text = (key: string) => t(`admin.systemPrompts.rules.${key}`)
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
const structured = computed(() => {
  if (content.value?.composition_mode !== 'anthropic_system_blocks') return null
  try {
    const document = JSON.parse(content.value.body)
    const blocks = (Array.isArray(document) ? document : document.blocks || []) as Array<{ text?: string; [key: string]: unknown } | null>
    return { document, blocks, expansion_prompt: Array.isArray(document) ? undefined : document.expansion_prompt as string | undefined }
  } catch { return null }
})
function editBlock(index: number, value: string) {
  const blocks = structured.value
  if (!blocks?.blocks[index] || !content.value) return
  blocks.blocks[index]!.text = value
  content.value.body = JSON.stringify(blocks.document)
}
function editExpansion(value: string) {
  const blocks = structured.value
  if (!blocks || !content.value) return
  blocks.document.expansion_prompt = value
  content.value.body = JSON.stringify(blocks.document)
}
const scopeSummary = computed(() => {
  const rule = selected.value
  if (!rule) return ''
  return [
    rule.model_match !== 'upstream' ? `${text('modelMatch')}: ${text(rule.model_match)}` : '',
    rule.models.length ? `${text('models')}: ${rule.models.join(', ')}` : '',
    rule.account_types?.length ? `${text('accountTypes')}: ${rule.account_types.join(', ')}` : '',
    rule.request_profiles?.length ? `${text('retainedClientScope')}: ${rule.request_profiles.join(', ')}` : '',
    rule.exclude_model_contains?.length ? `${text('retainedModelExclusions')}: ${rule.exclude_model_contains.join(', ')}` : '',
  ].filter(Boolean).join(' · ')
})
</script>
