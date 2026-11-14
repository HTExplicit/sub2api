<template>
  <div :data-editor-row="rowId">
    <input v-model="draft" data-test="row-editor" :aria-label="`Edit row ${rowId}`" @click.stop />
    <button type="button" data-test="editor-counter" @click.stop="edits++">{{ edits }}</button>
  </div>
</template>

<script setup lang="ts">
import { ref } from 'vue'

const props = defineProps<{ rowId: number; initialValue: string }>()
const draft = ref(props.initialValue)
const edits = ref(0)
</script>
