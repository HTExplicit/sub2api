<template>
  <AccountOperationDialog :show="show" :title="title" :job="job" width="normal" @close="emit('close')">
    <p data-test="confirm-dialog" class="text-sm leading-relaxed text-muted">{{ message }}</p>
    <p v-if="error" role="alert" class="mt-3 text-sm text-red-600">{{ error }}</p>
    <template #footer>
      <button class="btn btn-secondary" :disabled="busy" @click="emit('close')">{{ t('common.cancel') }}</button>
      <button data-test="confirm-dialog-submit" class="btn" :class="danger ? 'btn-danger' : 'btn-primary'" :disabled="busy" @click="start">{{ busy ? t('common.processing') : confirmText || t('common.confirm') }}</button>
    </template>
  </AccountOperationDialog>
</template>
<script setup lang="ts">
import { ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import AccountOperationDialog from './AccountOperationDialog.vue'
import type { AccountJob } from '@/api/admin/accountJobs'
const props = defineProps<{ show: boolean; title: string; message: string; danger?: boolean; confirmText?: string; execute: () => Promise<AccountJob> }>()
const emit = defineEmits<{ close: []; submitted: [job: AccountJob] }>()
const { t } = useI18n()
const job = ref<AccountJob | null>(null), busy = ref(false), error = ref('')
watch(() => props.show, show => { if (show) { job.value = null; error.value = '' } })
watch(() => props.execute, () => { job.value = null; error.value = '' })
async function start() {
  if (busy.value || job.value) return
  busy.value = true; error.value = ''
  try { job.value = await props.execute(); emit('submitted', job.value) }
  catch { error.value = t('admin.accountTasks.actionFailed') }
  finally { busy.value = false }
}
</script>
