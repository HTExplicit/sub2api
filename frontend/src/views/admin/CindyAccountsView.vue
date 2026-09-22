<template>
  <AccountViewPage :plugin-key="CINDY_ACCOUNT_VIEW_ALIAS.plugin_key" :view-id="CINDY_ACCOUNT_VIEW_ALIAS.view_id" />
</template>

<script setup lang="ts">
import AccountViewPage from '@/components/plugins/AccountViewPage.vue'
import { CINDY_ACCOUNT_VIEW_ALIAS } from '@/components/plugins/accountView'
</script>
