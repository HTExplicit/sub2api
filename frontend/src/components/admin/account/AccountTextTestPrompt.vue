<template>
  <ExtensionFields name="account.test.prompt" :values="{ prompt: modelValue }" :context="{}" :disabled="disabled"
    @update:values="value => emit('update:modelValue', value.prompt || '')" @validity="valid => emit('validity', valid)" />
</template>
<script setup lang="ts">
import ExtensionFields from '@/components/plugins/ExtensionFields.vue'
defineProps<{ modelValue: string; disabled?: boolean }>()
const emit = defineEmits<{ 'update:modelValue': [value: string]; validity: [valid: boolean] }>()
</script>
