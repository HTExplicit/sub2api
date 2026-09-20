<template>
  <Pagination :total="total" :page="page" :page-size="pageSize" :page-size-options="options"
    @update:page="$emit('update:page', $event)" @update:page-size="resize" />
</template>
<script setup lang="ts">
import { computed } from 'vue'
import { Pagination, usePersistentDraft, usePluginContext } from '@sub2api/plugin-ui'
defineProps<{ total: number; page: number; pageSize: number }>()
const emit = defineEmits<{ 'update:page': [page: number]; 'update:pageSize': [size: number] }>()
const context = usePluginContext()
const preference = usePersistentDraft('table-page-size')
const options = computed(() => Array.isArray(context.value.table_page_size_options) ? context.value.table_page_size_options as number[] : undefined)
function resize(size: number) { preference.value = String(size); emit('update:pageSize', size) }
</script>
