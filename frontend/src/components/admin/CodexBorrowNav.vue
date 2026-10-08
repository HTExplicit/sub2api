<template>
  <nav :aria-label="t('admin.codexGatewayBorrow.navLabel')">
    <ol class="grid grid-cols-1 gap-1 rounded-lg border border-gray-200 bg-white p-1 sm:grid-cols-2 dark:border-dark-600 dark:bg-dark-800">
      <li v-for="(item, index) in items" :key="item.path" class="min-w-0">
        <RouterLink
          :to="item.path"
          :aria-current="route.path === item.path ? 'page' : undefined"
          class="flex min-h-11 items-center gap-3 rounded-md px-3 py-2 text-sm transition-colors focus-visible:outline focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-primary-500"
          :class="route.path === item.path
            ? 'bg-primary-50 font-medium text-primary-700 dark:bg-primary-900/30 dark:text-primary-300'
            : 'text-gray-700 hover:bg-gray-50 dark:text-gray-300 dark:hover:bg-dark-700'"
        >
          <span aria-hidden="true" class="flex h-6 w-6 shrink-0 items-center justify-center rounded-full border border-current text-xs">{{ index + 1 }}</span>
          <span>{{ t(item.label) }}</span>
        </RouterLink>
      </li>
    </ol>
  </nav>
</template>

<script setup lang="ts">
import { RouterLink, useRoute } from 'vue-router'
import { useI18n } from 'vue-i18n'

const { t } = useI18n()
const route = useRoute()
const items = [
  { path: '/admin/codex-gateway-borrow', label: 'admin.codexGatewayBorrow.navSettings' },
  { path: '/admin/codex-gateway-borrow/status', label: 'admin.codexGatewayBorrow.navStatus' },
]
</script>
