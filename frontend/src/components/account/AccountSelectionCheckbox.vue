<template>
  <label class="inline-flex h-10 min-h-10 w-10 min-w-10 shrink-0 cursor-pointer items-center justify-center rounded-none hover:bg-primary-50 dark:hover:bg-primary-900/20" @click.stop @pointerdown.stop @mousedown.stop>
    <input type="checkbox" class="h-5 w-5 cursor-pointer rounded-none border-gray-300 text-primary-600 focus:ring-primary-500"
      :checked="checked" :indeterminate="indeterminate" :aria-label="label"
      @change="emit('change', ($event.target as HTMLInputElement).checked)" />
  </label>
</template>

<script setup lang="ts">
defineProps<{ checked: boolean; indeterminate?: boolean; label: string }>()
const emit = defineEmits<{ change: [checked: boolean] }>()
</script>
