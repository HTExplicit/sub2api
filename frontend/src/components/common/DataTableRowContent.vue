<template>
  <slot :row="row" :index="index" />
</template>

<script setup lang="ts">
// Each row owns the reactive reads in its content slot.
defineProps<{
  row: any
  index: number
}>()
</script>
