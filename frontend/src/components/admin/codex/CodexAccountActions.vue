<template>
  <template v-if="eligible">
    <button type="button" data-test="codex-harvest" :class="buttonClass" @click="emit('open', 'harvest', [...accountIds])">
      {{ text('采集并验证 Codex 路由', 'Acquire and verify Codex route') }}
    </button>
    <button type="button" data-test="codex-stop" :class="buttonClass" @click="emit('open', 'stop', [...accountIds])">
      {{ text('停止路由续期', 'Stop route renewal') }}
    </button>
  </template>
</template>

<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import type { AccountSelectionIdentity } from '@/composables/useAccountSelectionMetadata'
import { isCodexTicketSelection, type CodexTicketOperation } from '@/utils/codexTickets'

const props = withDefaults(defineProps<{
  accountIds: number[]
  accounts?: AccountSelectionIdentity[]
  variant?: 'buttons' | 'menu'
}>(), { accounts: () => [], variant: 'buttons' })
const emit = defineEmits<{ open: [operation: CodexTicketOperation, accountIds: number[]] }>()
const { locale } = useI18n()
const text = (zh: string, en: string) => locale?.value?.startsWith('en') ? en : zh
const eligible = computed(() => isCodexTicketSelection(props.accountIds, props.accounts))
const buttonClass = computed(() => props.variant === 'menu'
  ? 'flex w-full items-center gap-2 px-4 py-2 text-left text-sm hover:bg-gray-100 dark:hover:bg-dark-700'
  : 'btn btn-secondary btn-sm')
</script>
