<template>
  <div>
    <label for="pelican-gallery-account" class="text-xs text-muted">{{ text('账号', 'Account') }}</label>
    <Select id="pelican-gallery-account" :model-value="modelValue" :options="choices" :aria-label="text('按账号名称或 ID 搜索', 'Search account name or ID')" :search-placeholder="text('输入账号名称或 ID', 'Account name or ID')" remote :loading="loading" class="mt-1" @search="search" @update:model-value="$emit('update:modelValue', String($event || ''))" />
    <p v-if="error" role="alert" class="mt-1 text-xs text-red-700 dark:text-red-300">{{ error }}</p>
  </div>
</template>
<script setup lang="ts">
import { computed, onBeforeUnmount, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import Select from '@/components/common/Select.vue'
import { accountsAPI } from '@/api/admin/accounts'
import { extractApiErrorMessage } from '@/utils/apiError'

const props = defineProps<{ modelValue: string }>()
defineEmits<{ 'update:modelValue': [string] }>()
const { locale } = useI18n()
const text = (zh: string, en: string) => locale.value.startsWith('zh') ? zh : en
const found = ref<{ value: string; label: string }[]>([])
const labels = new Map<string, string>()
const loading = ref(false), error = ref('')
let controller: AbortController | undefined
const choices = computed(() => {
  const items = [{ value: '', label: text('全部账号', 'All accounts') }, ...found.value]
  if (props.modelValue && !items.some(item => item.value === props.modelValue)) items.push({ value: props.modelValue, label: labels.get(props.modelValue) || `#${props.modelValue}` })
  return items
})
async function search(query: string) {
  controller?.abort()
  const request = new AbortController(); controller = request; loading.value = true; error.value = ''
  try {
    const page = await accountsAPI.list(1, 30, { search: query, lite: 'true' }, { signal: request.signal })
    if (request.signal.aborted) return
    found.value = page.items.map(item => ({ value: String(item.id), label: `${item.name} #${item.id}` }))
    for (const item of found.value) labels.set(item.value, item.label)
  } catch (value) { if (!request.signal.aborted) error.value = extractApiErrorMessage(value, text('读取账号失败', 'Could not load accounts')) }
  finally { if (controller === request) loading.value = false }
}
onBeforeUnmount(() => controller?.abort())
</script>
