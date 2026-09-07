<template>
  <span
    data-testid="model-context-capacity-field"
    :data-model-id="row?.upstream_model_id || modelId"
    class="relative inline-flex shrink-0 select-none items-center gap-1 align-middle font-normal"
    @click.stop
    @dblclick.stop
    @mousedown.stop
    @mouseup.stop
    @pointerdown.stop
    @pointerup.stop
    @keydown.stop
    @keyup.stop
    @keypress.stop
  >
    <input
      v-if="editing"
      ref="inputElement"
      v-model="inputValue"
      data-testid="context-capacity-input"
      type="text"
      inputmode="text"
      autocomplete="off"
      spellcheck="false"
      maxlength="80"
      :aria-label="t(`${key}.edit`, { model: modelId })"
      :aria-invalid="!inputValid"
      :title="t(`${key}.${inputValid ? 'editHint' : 'invalid'}`)"
      :style="{ width: fieldWidth }"
      class="h-5 min-w-0 rounded border bg-white px-1 py-0 text-[11px] leading-4 text-gray-900 outline-none dark:bg-dark-800 dark:text-gray-100"
      :class="inputValid
        ? 'border-primary-400 focus:ring-1 focus:ring-primary-400'
        : 'border-red-500 focus:ring-1 focus:ring-red-500'"
      @keydown="handleKeydown"
      @blur="confirmEdit"
    />
    <button
      v-else-if="editable"
      type="button"
      data-testid="context-capacity-edit"
      :aria-label="t(`${key}.edit`, { model: modelId })"
      :title="detailsTitle"
      :style="{ width: fieldWidth }"
      class="h-5 rounded border border-transparent px-1 py-0 text-[11px] leading-4 text-primary-700 hover:border-primary-200 hover:bg-primary-50 focus-visible:outline-none focus-visible:ring-1 focus-visible:ring-primary-400 dark:text-primary-300 dark:hover:border-primary-700 dark:hover:bg-primary-900/30"
      @click="startEdit"
    ><span data-testid="context-capacity-value">[{{ formattedValue }}]</span></button>
    <span
      v-else
      data-testid="context-capacity-readonly"
      :title="detailsTitle"
      :style="{ width: fieldWidth }"
      class="inline-flex h-5 items-center justify-center px-1 text-[11px] leading-4 text-gray-500 dark:text-gray-400"
    ><span data-testid="context-capacity-value">[{{ formattedValue }}]</span></span>
    <span
      data-testid="context-capacity-source"
      :title="detailsTitle"
      class="whitespace-nowrap text-[10px] leading-none text-gray-500 dark:text-gray-400"
    >{{ sourceLabel }}</span>
    <span v-if="editing && !inputValid" role="alert" class="sr-only">{{ t(`${key}.invalid`) }}</span>
  </span>
</template>

<script setup lang="ts">
import { computed, nextTick, onBeforeUnmount, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import type { ModelContextCapacityRow } from '@/api/admin/accounts'
import { formatContextCapacity, parseContextCapacityInput } from '@/utils/modelContextCapacity'

const props = defineProps<{
  modelId: string
  row?: ModelContextCapacityRow
  draft?: string
}>()

const emit = defineEmits<{
  commit: [value: string]
  editing: [editing: boolean]
  validity: [valid: boolean]
}>()

const { t } = useI18n()
const key = 'admin.accounts.contextCapacity'
const editing = ref(false)
const inputValue = ref('')
const openingValue = ref('')
const inputElement = ref<HTMLInputElement>()

function isConcreteModelId(modelId: string): boolean {
  return !!modelId && modelId.trim() === modelId &&
    new TextEncoder().encode(modelId).length <= 512 && !modelId.includes('*') &&
    [...modelId].every(character => character.charCodeAt(0) >= 32 && character.charCodeAt(0) !== 127)
}

const boundRow = computed(() => {
  const row = props.row
  if (!row || !isConcreteModelId(props.modelId) || !isConcreteModelId(row.upstream_model_id)) return undefined
  return row.upstream_model_id === props.modelId || row.aliases.includes(props.modelId) ? row : undefined
})
const editable = computed(() => !!boundRow.value?.editable && boundRow.value.effective_source !== 'protected')
const preview = computed<{ value: number | null; source: string }>(() => {
  const row = boundRow.value
  if (!row) return { value: null, source: 'unknown' }
  if (editable.value && props.draft !== undefined) {
    const parsed = parseContextCapacityInput(props.draft)
    if (!parsed.valid) return { value: null, source: 'invalid' }
    return parsed.value === null
      ? { value: row.automatic_context_window, source: row.automatic_source }
      : { value: parsed.value, source: 'custom' }
  }
  return { value: row.effective_context_window, source: row.effective_source }
})
const formattedValue = computed(() => formatContextCapacity(preview.value.value))
const fieldWidth = computed(() => `${Math.max(6, formattedValue.value.length + 2)}ch`)
const inputValid = computed(() => parseContextCapacityInput(inputValue.value).valid)
const sourceLabel = computed(() => ['custom', 'official', 'upstream', 'default', 'protected', 'invalid'].includes(preview.value.source)
  ? t(`${key}.sources.${preview.value.source}`)
  : t(`${key}.unknown`))
const detailsTitle = computed(() => {
  const row = boundRow.value
  const details = [t(`${key}.effective`), `${formattedValue.value} · ${sourceLabel.value}`]
  if (preview.value.value) details.push(t(`${key}.exactTokens`, { value: preview.value.value }))
  const upstream = row?.upstream?.context_window || row?.upstream?.max_context_window || row?.upstream?.max_input_tokens
  if (upstream && preview.value.value && preview.value.value > upstream) details.push(t(`${key}.exceedsUpstream`))
  details.push(t(`${key}.${editable.value ? 'editHint' : row ? 'readonly' : 'unavailable'}`))
  return details.join('\n')
})

watch(() => !editing.value || inputValid.value, valid => emit('validity', valid), { immediate: true, flush: 'sync' })
watch([() => props.modelId, () => props.row?.upstream_model_id, editable], cancelEdit)

async function startEdit(): Promise<void> {
  if (!editable.value) return
  // Editing starts with the displayed value, but merely opening or reformatting it is not an override.
  inputValue.value = props.draft !== undefined && !parseContextCapacityInput(props.draft).valid
    ? props.draft
    : preview.value.value ? formatContextCapacity(preview.value.value) : ''
  openingValue.value = inputValue.value
  editing.value = true
  emit('editing', true)
  await nextTick()
  inputElement.value?.focus()
  inputElement.value?.select()
}

function finishEdit(): void {
  if (!editing.value) return
  editing.value = false
  emit('editing', false)
}

function cancelEdit(): void {
  finishEdit()
}

function confirmEdit(): void {
  if (!editing.value) return
  if (!editable.value) {
    cancelEdit()
    return
  }
  const parsed = parseContextCapacityInput(inputValue.value)
  if (!parsed.valid) return
  const opening = parseContextCapacityInput(openingValue.value)
  const changed = !opening.valid || parsed.value !== opening.value
  const value = inputValue.value.trim()
  // Leave edit mode first so a following blur cannot submit this draft a second time.
  finishEdit()
  if (changed) emit('commit', value)
}

function handleKeydown(event: KeyboardEvent): void {
  if (event.isComposing) return
  if (event.key === 'Enter') {
    event.preventDefault()
    confirmEdit()
  } else if (event.key === 'Escape') {
    event.preventDefault()
    cancelEdit()
  }
}

onBeforeUnmount(() => {
  finishEdit()
  emit('validity', true)
})
</script>
