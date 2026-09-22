<template>
  <ExtensionDisplay v-if="admission.allowed || admission.reason === 'unavailable'" :name="contribution.id" :plugin-id="contribution.plugin_id"
    :plugin-key="contribution.plugin_key" :package-sha="contribution.package_sha256"
    :values="values" :show-label="showLabel" />
</template>
<script setup lang="ts">
import { computed } from 'vue'
import type { Account } from '@/types'
import type { PluginContribution } from '@/api/admin/plugins'
import { accountDisplayValues } from './accountView'
import { contributionAdmission } from './contributionAdmission'
import ExtensionDisplay from './ExtensionDisplay.vue'
const props = defineProps<{ contribution: PluginContribution; account: Account; showLabel?: boolean }>()
const values = computed(() => accountDisplayValues(props.contribution, props.account))
const admission = computed(() => contributionAdmission(props.contribution, { account: props.account }))
</script>
