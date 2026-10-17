<template>
  <div v-if="evaluation" class="flex flex-wrap gap-1.5" :aria-label="text('分项检测结果', 'Test results')">
    <span v-for="item in items" :key="item.key" class="rounded px-2 py-1 text-xs" :class="tone(item.state)">{{ item.label }} · {{ label(item.state) }}</span>
  </div>
</template>
<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import type { PelicanEvaluation } from '@/api/admin/pelicanTests'
import { pelicanLabel, pelicanTone } from '@/utils/pelicanDisplay'
const props = defineProps<{ evaluation?: PelicanEvaluation }>()
const { locale } = useI18n()
const text = (zh: string, en: string) => locale.value.startsWith('zh') ? zh : en
const label = (value: string) => pelicanLabel(value, locale.value)
const tone = pelicanTone
const items = computed(() => [
  { key: 'pelican', label: text('鹈鹕生成', 'Pelican'), state: props.evaluation?.generation || '' },
  { key: 'candy', label: text('糖果', 'Candy'), state: props.evaluation?.candy || '' },
  { key: 'review', label: text('视觉审核', 'Visual review'), state: props.evaluation?.review || '' }
].filter(item => item.state && item.state !== 'not_selected'))
</script>
