<template>
  <BaseDialog :show="show" :title="t('admin.accounts.batchTest.title')" @close="emit('close')">
    <form id="batch-test-accounts" @submit.prevent="submit">
      <p class="mb-4 text-sm text-gray-600 dark:text-gray-300">{{ t('admin.accounts.batchTest.description', { count: accountIds.length }) }}</p>
      <label for="batch-test-model" class="mb-2 block text-sm font-medium">{{ t('admin.accounts.batchTest.model') }}</label>
      <input id="batch-test-model" v-model="model" class="input w-full" maxlength="256" :disabled="busy"
        :placeholder="t('admin.accounts.batchTest.defaultModel')" />
    </form>
    <template #footer>
      <button type="button" class="btn btn-secondary" :disabled="busy" @click="emit('close')">{{ t('common.cancel') }}</button>
      <button type="submit" form="batch-test-accounts" class="btn btn-primary" :disabled="busy || !accountIds.length">{{ t(busy ? 'common.submitting' : 'admin.accounts.batchTest.start') }}</button>
    </template>
  </BaseDialog>
</template>

<script setup lang="ts">
import { ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import BaseDialog from '@/components/common/BaseDialog.vue'
import accountJobsAPI, { type AccountJob } from '@/api/admin/accountJobs'
import { useAppStore } from '@/stores/app'

const props = defineProps<{ show: boolean; accountIds: number[] }>()
const emit = defineEmits<{ close: []; submitted: [job: AccountJob] }>()
const { t } = useI18n()
const model = ref('')
const busy = ref(false)
watch(() => props.show, (show) => { if (show) model.value = '' })
async function submit() {
  if (busy.value || !props.accountIds.length) return
  busy.value = true
  try {
    emit('submitted', await accountJobsAPI.batchTest([...props.accountIds], model.value.trim()))
    emit('close')
  } catch { useAppStore().showError(t('admin.accounts.batchTest.submitFailed')) }
  finally { busy.value = false }
}
</script>
