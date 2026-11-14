import { computed, watch, type Ref } from 'vue'
import type { Account } from '@/types'

type AccountRange = { start: number; end: number }
type RequestUsage<T> = (account: T, options?: { force?: boolean }) => void

/** Prepare adjacent account windows through the page's existing batch request. */
export function useAccountUsagePrefetch<T extends Pick<Account, 'id'>>(
  accounts: Ref<T[]>,
  range: Ref<AccountRange | null>,
  enabled: Ref<boolean>,
  request: Ref<RequestUsage<T> | null | undefined>,
) {
  const prefetchWindow = computed(() => {
    const current = range.value
    const size = current ? current.end - current.start + 1 : 0
    const targets = enabled.value && current
      ? [
        ...accounts.value.slice(Math.max(0, current.start - size), current.start),
        ...accounts.value.slice(current.end + 1, current.end + 1 + size),
      ]
      : []
    return { targets, accounts: accounts.value, request: request.value }
  })
  watch(prefetchWindow, (current, previous) => {
    if (!current.request) return
    const retained = previous && previous.accounts === current.accounts && previous.request === current.request
      ? new Set(previous.targets.map(account => account.id)) : new Set<number>()
    // Retained failures do not auto-retry as the adjacent windows move.
    for (const account of current.targets) {
      if (!retained.has(account.id)) current.request(account)
    }
  }, { immediate: true })
}
