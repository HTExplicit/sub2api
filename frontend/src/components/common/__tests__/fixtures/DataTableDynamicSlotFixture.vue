<template>
  <DataTable
    :columns="columns"
    :data="rows"
    row-key="id"
    :virtualize-threshold="50"
    :overscan="2"
    :estimate-row-height="56"
    :preserve-virtual-row-shells="true"
  >
    <template #[slotName]="{ row }">
      <span data-test="dynamic-cell">{{ label }} {{ row.id }}</span>
    </template>
  </DataTable>
</template>

<script setup lang="ts">
import DataTable from '../../DataTable.vue'
import type { Column } from '../../types'

defineProps<{
  rows: Array<{ id: number; name: string; detail: { label: string } }>
  slotName: string
  label: string
}>()

const columns: Column[] = [
  { key: 'name', label: 'Name' },
  { key: 'usage', label: 'Usage' },
]
</script>
