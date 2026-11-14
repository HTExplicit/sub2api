<template>
  <DataTable
    v-model:selected-keys="selectedKeys"
    :columns="columns"
    :data="rows"
    row-key="id"
    :virtualize-threshold="50"
    :overscan="2"
    :estimate-row-height="estimateRowHeight"
    :preserve-virtual-row-shells="true"
    selectable
  >
    <template #cell-name="{ row }">
      <DataTableEditorProbe :row-id="row.id" :initial-value="row.name" />
      <span data-test="row-detail">{{ row.detail.label }}</span>
    </template>
    <template #cell-usage="{ row }">
      <span data-test="row-usage">{{ usageById[row.id]?.value ?? '' }}</span>
    </template>
    <template #cell-locale>
      <span data-test="row-locale">{{ localeState.locale }} | {{ localeState.label }}</span>
    </template>
  </DataTable>
</template>

<script setup lang="ts">
import { ref } from 'vue'
import DataTable from '../../DataTable.vue'
import type { Column } from '../../types'
import DataTableEditorProbe from './DataTableEditorProbe.vue'

withDefaults(defineProps<{
  rows: Array<{ id: number; name: string; detail: { label: string } }>
  usageById?: Record<number, { value: string }>
  localeState?: { locale: string; label: string }
  estimateRowHeight?: number
}>(), {
  usageById: () => ({}),
  localeState: () => ({ locale: 'en', label: 'Name' }),
  estimateRowHeight: 56,
})

const columns: Column[] = [
  { key: 'name', label: 'Name' },
  { key: 'usage', label: 'Usage' },
  { key: 'locale', label: 'Locale' },
]
const selectedKeys = ref<Array<string | number>>([])
</script>
