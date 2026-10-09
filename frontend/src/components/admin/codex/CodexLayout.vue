<template>
  <AppLayout>
    <div class="mx-auto max-w-[1440px] space-y-6" data-ui="codex-console">
      <header class="space-y-2">
        <h1 class="text-2xl font-semibold text-ink">Codex</h1>
        <p class="max-w-3xl text-sm text-muted">{{ text('配置线路借用、管理客户端身份，并核实请求实际使用了什么。', 'Configure route borrowing, manage client identity, and inspect what requests actually used.') }}</p>
      </header>
      <nav class="flex flex-wrap gap-1 border-b border-line" :aria-label="text('Codex 功能', 'Codex features')">
        <RouterLink v-for="item in tabs" :key="item.path" :to="item.path" class="min-h-11 border-b-2 px-4 py-3 text-sm focus-visible:outline focus-visible:outline-2 focus-visible:outline-primary-500" :class="route.path === item.path ? 'border-primary-500 font-semibold text-primary-700 dark:text-primary-300' : 'border-transparent text-muted hover:text-ink'" :aria-current="route.path === item.path ? 'page' : undefined">{{ item.label }}</RouterLink>
      </nav>
      <slot />
    </div>
  </AppLayout>
</template>

<script setup lang="ts">
import { computed } from 'vue'
import { RouterLink, useRoute } from 'vue-router'
import { useI18n } from 'vue-i18n'
import AppLayout from '@/components/layout/AppLayout.vue'
const route = useRoute()
const { locale } = useI18n()
const text = (zh: string, en: string) => locale.value.startsWith('en') ? en : zh
const tabs = computed(() => [
  { path: '/admin/codex', label: text('使用与状态', 'Setup & status') },
  { path: '/admin/codex/identity', label: text('客户端身份', 'Client identity') },
  { path: '/admin/codex/advanced', label: text('高级设置', 'Advanced') }
])
</script>
