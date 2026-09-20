<template>
  <AccountFolderBar v-if="mode === 'account-taxonomy-navigation'" :folders="input.folders || []" :active-folder="input.activeFolder || ''"
    :total="input.total" :uncategorized-count="input.uncategorizedCount" :loading="input.loading" :error="input.error"
    @select="(value: string) => event('select', value)" @manage="event('manage')" @retry="event('retry')" />
  <AccountTaxonomyManager v-else-if="mode === 'account-taxonomy-manager'" show :folders="input.folders || []" :tags="input.tags || []"
    @close="event('close')" @changed="event('changed')" />
  <AccountBulkTaxonomyModal v-else-if="mode === 'account-taxonomy-bulk'" show :target="input.target || null" :folders="input.folders || []" :tags="input.tags || []"
    @close="event('close')" @stale="event('stale')" @updated="(job: AccountJob) => openHostJob(job.id)" />
  <BatchTestAccountModal v-else-if="mode === 'account-batch-test'" show :account-ids="input.accountIds || []"
    @close="event('close')" @submitted="(job: AccountJob) => openHostJob(job.id)" />
  <ImportDataModal v-else-if="mode === 'account-import'" show :folders="input.folders || []" :tags="input.tags || []" :groups="input.groups || []" :proxies="input.proxies || []"
    @close="event('close')" @imported="(job: AccountJob) => openHostJob(job.id)" />
  <AccountTaxonomyEditor v-else-if="mode === 'account-taxonomy-edit' && input.accountId" :account-id="input.accountId"
    :folder-id="input.folderId" :tag-ids="input.tagIds || []" :folders="input.folders || []" :tags="input.tags || []" @changed="event('changed')" />
</template>
<script setup lang="ts">
import { computed } from 'vue'
import { usePluginContext, emitHostEvent, openHostJob } from '@sub2api/plugin-ui'
import AccountFolderBar from './AccountFolderBar.vue'
import AccountTaxonomyManager from './AccountTaxonomyManager.vue'
import AccountBulkTaxonomyModal from './AccountBulkTaxonomyModal.vue'
import BatchTestAccountModal from './BatchTestAccountModal.vue'
import ImportDataModal from './ImportDataModal.vue'
import AccountTaxonomyEditor from './AccountTaxonomyEditor.vue'
import type { AccountJob, AccountManagementFolder, AccountManagementTag, AdminGroup, Proxy } from './api'
import type { AccountBulkTaxonomyTarget } from './AccountBulkTaxonomyModal.vue'
interface ViewProps {
  folders?: AccountManagementFolder[]; tags?: AccountManagementTag[]; activeFolder?: string
  total?: number; uncategorizedCount?: number; loading?: boolean; error?: boolean
  target?: AccountBulkTaxonomyTarget; accountIds?: number[]
  accountId?: number; folderId?: number | null; tagIds?: number[]
  groups?: AdminGroup[]; proxies?: Proxy[]
}
const context = usePluginContext()
const mode = computed(() => context.value.contribution_id)
const input = computed(() => (context.value.view_props || {}) as ViewProps)
function event(name: string, payload?: unknown) { void emitHostEvent(name, payload) }
</script>
