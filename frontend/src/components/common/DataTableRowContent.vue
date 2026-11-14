<template>
  <slot :row="row" :index="index" />
</template>

<script setup lang="ts">
// A row owns the reactive reads performed by its forwarded cell slots.
defineProps<{
  row: any
  index: number
}>()
</script>
