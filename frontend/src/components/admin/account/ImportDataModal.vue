<template>
  <ExtensionModal :show="show" name="account-import" plugin-key="codexrip.account-tools" independent-core :context="{ view_props: choices }" @close="emit('close')" @job="job => emit('imported', job)" />
</template>
<script setup lang="ts">
import { computed } from 'vue'
import ExtensionModal from '@/components/plugins/ExtensionModal.vue'
import type { AccountManagementFolder, AccountManagementTag, AdminGroup, Proxy } from '@/types'
import type { AccountJob } from '@/api/admin/accountJobs'
const props = withDefaults(defineProps<{ show: boolean; folders?: AccountManagementFolder[]; tags?: AccountManagementTag[]; groups?: AdminGroup[]; proxies?: Proxy[] }>(), { folders: () => [], tags: () => [], groups: () => [], proxies: () => [] })
const emit = defineEmits<{ close: []; imported: [job: AccountJob] }>()
const choices = computed(() => ({
  folders: props.folders.map(({ id, name }) => ({ id, name })),
  tags: props.tags.map(({ id, name }) => ({ id, name })),
  groups: props.groups.map(({ id, name, platform, wire_platform, provider_profile }) => ({ id, name, platform, wire_platform, provider_profile })),
  proxies: props.proxies.map(({ id, name }) => ({ id, name }))
}))
</script>
