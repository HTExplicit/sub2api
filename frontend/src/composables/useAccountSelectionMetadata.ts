import { computed, onBeforeUnmount, ref, watch, type Ref } from 'vue'
import { list } from '@/api/admin/accounts'
import type { Account } from '@/types'
import { useAccountViewContext, type CapturedAccountView } from './useAccountViewContext'

export type AccountSelectionIdentity = Pick<Account, 'id' | 'platform' | 'type' | 'parent_account_id'> & Partial<Pick<Account, 'status'>>

// Retain only the non-secret identity fields needed by declared extension
// filters. Unknown cross-page selections stay ineligible until the read returns.
export function useAccountSelectionMetadata(ids: Ref<number[]>, rows: Ref<AccountSelectionIdentity[]>, origin?: () => CapturedAccountView | undefined) {
  const view = useAccountViewContext()
  const identities = ref(new Map<number, AccountSelectionIdentity>())
  let generation = 0
  let controller: AbortController | null = null
  function remember(accounts: AccountSelectionIdentity[]) {
    const next = new Map(identities.value)
    for (const account of accounts) next.set(account.id, { id: account.id, platform: account.platform, type: account.type, status: account.status, parent_account_id: account.parent_account_id })
    identities.value = next
  }
  watch([() => ids.value.join(','), rows, () => view?.revision() || ''], async () => {
    const version = ++generation
    controller?.abort(); controller = new AbortController()
    const signal = controller.signal
    remember(rows.value)
    const missing = ids.value.filter(id => !identities.value.has(id))
    try {
      const scope = origin ? origin() : view?.capture()
      for (let offset = 0; offset < missing.length; offset += 100) {
        const filters = { account_ids: missing.slice(offset, offset + 100).join(','), lite: '1', include_scheduler_score: '0' }
        const page = scope ? await list(1, 100, filters, { signal }, scope) : await list(1, 100, filters, { signal })
        if (version !== generation || signal.aborted) return
        remember(page.items)
      }
    } catch { /* Missing identity never implies eligibility. */ }
  }, { immediate: true })
  onBeforeUnmount(() => { generation++; controller?.abort() })
  const selectedAccounts = computed(() => ids.value.flatMap(id => { const account = identities.value.get(id); return account ? [account] : [] }))
  return { remember, selectedAccounts }
}
