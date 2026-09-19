import { computed, onBeforeUnmount, ref, watch, type Ref } from 'vue'
import { list } from '@/api/admin/accounts'
import { allCanManageCodexTickets, type TicketAccountIdentity } from '@/utils/codexTicketEligibility'

export function useTicketSelection(ids: Ref<number[]>, rows: Ref<TicketAccountIdentity[]>) {
  const identities = ref(new Map<number, TicketAccountIdentity>())
  let generation = 0
  let controller: AbortController | null = null
  function remember(accounts: TicketAccountIdentity[]) {
    const next = new Map(identities.value)
    for (const a of accounts) next.set(a.id, { id: a.id, platform: a.platform, type: a.type, parent_account_id: a.parent_account_id })
    identities.value = next
  }
  watch([() => ids.value.join(','), rows], async () => {
    const version = ++generation
    controller?.abort()
    controller = new AbortController()
    const signal = controller.signal
    remember(rows.value)
    const missing = ids.value.filter(id => !identities.value.has(id))
    try {
      for (let i = 0; i < missing.length; i += 100) {
        const page = await list(1, 100, { account_ids: missing.slice(i, i + 100).join(','), lite: '1', include_scheduler_score: '0' }, { signal })
        if (version !== generation || signal.aborted) return
        remember(page.items)
      }
    } catch { /* Unknown account identities cannot expose the batch action. */ }
  }, { immediate: true })
  onBeforeUnmount(() => { generation++; controller?.abort() })
  return { remember, canHarvestTickets: computed(() => allCanManageCodexTickets(ids.value, identities.value)) }
}
