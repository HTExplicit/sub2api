<template>
  <section class="space-y-3" data-test="account-system-prompt-binding">
    <h3 v-if="embedded" class="text-sm font-medium text-gray-700 dark:text-gray-300">{{ t('admin.systemPrompts.accountPrompts') }}</h3>
    <p class="text-xs text-muted">{{ t('admin.systemPrompts.binding.description') }}</p>
    <p v-if="scope" class="text-xs text-muted" data-test="system-prompt-binding-scope">{{ scope }}</p>
    <p v-if="error" role="alert" class="text-sm text-red-600">{{ error }}</p>
    <div data-ui="dense-dlg-if" class="flex flex-wrap items-end gap-3">
      <label class="space-y-1">
        <span class="block text-sm">{{ t('admin.systemPrompts.binding.mode') }}</span>
        <select v-model="mode" class="input" data-test="system-prompt-binding-mode" :disabled="busy">
          <option value="inherit">{{ t('admin.systemPrompts.binding.inherit') }}</option>
          <option value="off">{{ t('admin.systemPrompts.binding.off') }}</option>
          <option value="custom" :disabled="!prompts.length">{{ t('admin.systemPrompts.binding.custom') }}</option>
        </select>
      </label>
      <label v-if="mode === 'custom'" class="space-y-1">
        <span class="block text-sm">{{ t('admin.systemPrompts.binding.prompt') }}</span>
        <select v-model="promptId" class="input" data-test="system-prompt-binding-prompt" :disabled="busy">
          <option v-for="prompt in prompts" :key="prompt.id" :value="prompt.id">{{ prompt.name }}</option>
        </select>
      </label>
      <button v-if="!embedded" type="button" class="btn btn-primary btn-sm" data-test="system-prompt-binding-apply" :disabled="busy || !valid" @click="apply">
        {{ t('admin.systemPrompts.binding.apply') }}
      </button>
    </div>
    <p v-if="state && promptTakers > 0" class="text-xs text-muted" data-test="system-prompt-binding-effect">{{ effect }}</p>
  </section>
</template>

<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { useAppStore } from '@/stores/app'
import { extractApiErrorMessage } from '@/utils/apiError'
import { systemPromptsAPI, type SystemPromptBinding, type SystemPromptBindingMode, type SystemPromptState } from '@/api/admin/systemPrompts'
import { readSystemPromptBinding } from '@/utils/systemPromptBinding'

// takers: how many of accountIds the caller knows to take a prompt. The account edit dialog and the row menu offer the
// binding only for such an account and leave it out. A bulk selection can also hold accounts that take none and
// accounts the list has not loaded; the request skips every account that takes none.
const props = defineProps<{ accountIds: number[]; takers?: number; current?: unknown; embedded?: boolean }>()
const emit = defineEmits<{ changed: [binding: SystemPromptBinding] }>()
const { t } = useI18n()
const appStore = useAppStore()

// What the binding puts in effect is stated only when an account is known to receive it.
const promptTakers = computed(() => props.takers ?? props.accountIds.length)
// Which accounts the binding reaches: their number for more than one, and that TypeSafe accounts are skipped
// whenever one of them may be skipped, whatever the size of the selection.
const scope = computed(() => {
  const count = props.accountIds.length
  if (promptTakers.value >= count) return count > 1 ? t('admin.systemPrompts.binding.bulkHint', { count }) : ''
  return count > 1 ? t('admin.systemPrompts.binding.bulkSkipHint', { count }) : t('admin.systemPrompts.binding.skipHint')
})
const initial = readSystemPromptBinding(props.current)
const state = ref<SystemPromptState | null>(null)
const mode = ref<SystemPromptBindingMode>(initial.mode)
const promptId = ref(initial.prompt_id || '')
const busy = ref(false)
const error = ref('')

const prompts = computed(() => state.value?.prompts || [])
const valid = computed(() => mode.value !== 'custom' || prompts.value.some(prompt => prompt.id === promptId.value))
const dirty = computed(() => mode.value !== initial.mode || (mode.value === 'custom' && promptId.value !== (initial.prompt_id || '')))
const effect = computed(() => {
  const current = state.value
  if (!current) return ''
  if (!current.enabled) return t('admin.systemPrompts.binding.globalOff')
  if (mode.value === 'off') return t('admin.systemPrompts.binding.effectiveOff')
  const id = mode.value === 'custom' ? promptId.value : current.default_prompt_id
  const prompt = current.prompts.find(item => item.id === id)
  if (mode.value === 'custom') return prompt ? t('admin.systemPrompts.binding.effectiveCustom', { name: prompt.name }) : t('admin.systemPrompts.binding.missing')
  return prompt ? t('admin.systemPrompts.binding.effectiveDefault', { name: prompt.name }) : t('admin.systemPrompts.binding.effectiveNone')
})

function selection(): SystemPromptBinding {
  return mode.value === 'custom' ? { mode: 'custom', prompt_id: promptId.value } : { mode: mode.value }
}

async function apply(): Promise<SystemPromptBinding | undefined> {
  if (busy.value || !valid.value) return undefined
  const ids = [...new Set(props.accountIds)].filter(id => Number.isSafeInteger(id) && id > 0)
  if (!ids.length) return undefined
  busy.value = true
  error.value = ''
  try {
    const binding = selection()
    const result = await systemPromptsAPI.setBindings(ids, binding)
    appStore.showSuccess(t('admin.systemPrompts.binding.applied', { count: result.updated }))
    emit('changed', binding)
    return binding
  } catch (value) {
    error.value = extractApiErrorMessage(value) || t('admin.systemPrompts.binding.failed')
    appStore.showError(error.value)
    return undefined
  } finally {
    busy.value = false
  }
}

// The account edit form saves the binding after its own update succeeds.
async function saveIfChanged(): Promise<SystemPromptBinding | undefined> {
  return dirty.value ? apply() : undefined
}

defineExpose({ saveIfChanged })

onMounted(async () => {
  try {
    state.value = await systemPromptsAPI.get()
    if (!promptId.value && state.value.prompts.length) promptId.value = state.value.prompts[0]!.id
  } catch (value) {
    error.value = extractApiErrorMessage(value) || t('admin.systemPrompts.loadFailed')
  }
})
</script>
