<template>
  <Select :model-value="modelValue" :options="options" value-key="id" label-key="display_name"
    :disabled="disabled" :placeholder="placeholder" :id="id" @update:model-value="select" />
</template>
<script setup lang="ts">
import { computed } from 'vue'
import { Select } from '@sub2api/plugin-ui'
import type { AccountAvailableModel } from './api'
const props = defineProps<{ modelValue: string; models: AccountAvailableModel[]; disabled?: boolean; placeholder?: string; id?: string }>()
const emit = defineEmits<{ 'update:modelValue': [value: string] }>()
const options = computed(() => props.models.map(model => ({ ...model })))
function select(value: string | number | boolean | null) { if (typeof value === 'string') emit('update:modelValue', value) }
</script>
