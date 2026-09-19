<template>
  <ExtensionFields v-if="model?.reasoning_efforts?.length" name="account.test"
    :values="{ reasoning_effort: modelValue }" :context="{ reasoning_efforts: model.reasoning_efforts, default_reasoning_effort: model.default_reasoning_effort }" :disabled="disabled"
    @update:values="value => emit('update:modelValue', value.reasoning_effort || '')" @validity="value => emit('validity', value)" />
</template>
<script setup lang="ts">
import { watch } from 'vue'
import type { AccountAvailableModel } from '@/types'
import ExtensionFields from '@/components/plugins/ExtensionFields.vue'
const props = defineProps<{ modelValue: string; model?: AccountAvailableModel; disabled?: boolean }>()
const emit = defineEmits<{ 'update:modelValue': [value: string]; validity: [valid: boolean] }>()
watch(() => props.model?.reasoning_efforts, levels => {
  if (!levels?.length) { if (props.modelValue) emit('update:modelValue', ''); emit('validity', true) }
}, { immediate: true })
</script>
