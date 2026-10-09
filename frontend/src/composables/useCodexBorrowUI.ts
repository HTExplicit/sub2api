import { computed, onBeforeUnmount, onMounted, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { accountsAPI } from '@/api/admin/accounts'
import type { BorrowTargetStatus } from '@/api/admin/codexGatewayBorrow'
import type { AccountListItem } from '@/types'
import { resolveOpenAIWSModeFromExtra } from '@/utils/openaiWsMode'

/** Inventory is local to each page; never discover models or test accounts here. */
export function useCodexBorrowInventory(signal: AbortSignal) {
  const { t, te } = useI18n()
  const accounts = ref<AccountListItem[]>([])
  const accountMap = computed(() => new Map(accounts.value.map(account => [account.id, account])))
  function accountById(id: number) { return accountMap.value.get(id) }
  function accountName(id: number) { return accountById(id)?.name || t('admin.codexGatewayBorrow.unknownAccount') }
  function accountStateLabel(account?: AccountListItem) {
    if (!account) return t('admin.codexGatewayBorrow.unknownAccount')
    const key = `admin.codexGatewayBorrow.states.${account.status}`
    const parts = [te(key) ? t(key) : account.status]
    if (!account.schedulable) parts.push(t('admin.codexGatewayBorrow.paused'))
    if (account.rate_limit_reset_at && Date.parse(account.rate_limit_reset_at) > Date.now()) parts.push(t('admin.codexGatewayBorrow.rateLimited'))
    return parts.join(' · ')
  }
  function proxyLabel(account?: AccountListItem) {
    if (!account) return '—'
    if (account.proxy) return `${account.proxy.name} #${account.proxy.id}`
    if (account.proxy_id) return `#${account.proxy_id}`
    return t('admin.codexGatewayBorrow.direct')
  }
  function wsLabel(account: AccountListItem) {
    const keys = ['openai_oauth_responses_websockets_v2_mode', 'openai_oauth_responses_websockets_v2_enabled', 'responses_websockets_v2_enabled', 'openai_ws_enabled']
    if (!keys.some(key => account.extra?.[key] !== undefined)) return t('admin.codexGatewayBorrow.wsUnspecified')
    return resolveOpenAIWSModeFromExtra(account.extra, { modeKey: keys[0], enabledKey: keys[1], fallbackEnabledKeys: keys.slice(2) })
  }
  function modelMappingLabel(account: AccountListItem) {
    const mapping = account.credentials?.model_mapping
    if (!mapping || typeof mapping !== 'object' || !Object.keys(mapping).length) return t('admin.codexGatewayBorrow.noModelRestriction')
    const entries = Object.entries(mapping).filter((entry): entry is [string, string] => typeof entry[1] === 'string')
    return `${t('admin.codexGatewayBorrow.localMappings')}: ${entries.map(([from, to]) => from === to ? from : `${from} → ${to}`).join(', ')}`
  }
  async function loadAccounts() {
    const found: AccountListItem[] = []
    for (let page = 1; !signal.aborted; page++) {
      const response = await accountsAPI.list(page, 100, { platform: 'openai', types: 'oauth,setup-token' }, { signal })
      found.push(...response.items.filter(account => account.platform === 'openai' && (account.type === 'oauth' || account.type === 'setup-token')))
      if (!response.items.length || page * 100 >= response.total) break
    }
    if (!signal.aborted) accounts.value = found
  }
  return { accounts, loadAccounts, accountById, accountName, accountStateLabel, proxyLabel, wsLabel, modelMappingLabel }
}

/** A local tick expires the display without requesting or renewing any route. */
export function useCodexBorrowClock() {
  const now = ref(Date.now())
  const serverClockKnown = ref(false)
  const serverOffsetMs = ref(0)
  let timer: ReturnType<typeof setInterval> | undefined
  function receiveServerTime(generatedAt: string) {
    const time = Date.parse(generatedAt)
    serverClockKnown.value = Number.isFinite(time)
    if (serverClockKnown.value) {
      serverOffsetMs.value = time - Date.now()
      now.value = time
    }
  }
  function remainingSeconds(expiresAt?: string) {
    const expires = expiresAt ? Date.parse(expiresAt) : NaN
    return Number.isFinite(expires) ? Math.max(0, Math.ceil((expires - now.value) / 1000)) : 0
  }
  function cacheUsable(target?: BorrowTargetStatus) {
    return !!target?.cache_valid && (!target.expires_at || Date.parse(target.expires_at) > now.value)
  }
  onMounted(() => { timer = setInterval(() => { now.value = Date.now() + serverOffsetMs.value }, 1000) })
  onBeforeUnmount(() => { if (timer !== undefined) clearInterval(timer) })
  return { now, serverClockKnown, serverOffsetMs, receiveServerTime, cacheUsable, remainingSeconds }
}

export function borrowTargetState(target: BorrowTargetStatus | undefined, now: number): 'ready' | 'expired' | 'validating' | 'waiting' | 'rejected' {
  if (!target) return 'waiting'
  if (target.cache_valid && (!target.expires_at || Date.parse(target.expires_at) > now)) return 'ready'
  if (target.state === 'expired' || ((target.cache_valid || target.state === 'ready' || target.state === 'valid') && !!target.expires_at && Date.parse(target.expires_at) <= now)) return 'expired'
  if (target.state === 'validating' || target.reason === 'validating') return 'validating'
  if (['waiting', 'missing', 'disabled'].includes(target.state) || ['not_prepared', 'not_verified', 'not_configured', 'disabled'].includes(target.reason)) return 'waiting'
  return 'rejected'
}

const knownReasons = new Set([
  'account_unavailable', 'account_inactive', 'model_not_allowed', 'model_mapping_mismatch', 'identity_changed',
  'not_prepared', 'not_verified', 'not_configured', 'disabled', 'route_expired',
  'source_qualified', 'source_probe_failed', 'source_response_not_qualified', 'source_routing_cookie_unavailable',
  'target_probe_passed', 'target_probe_failed', 'target_route_changed', 'target_state_missing', 'target_state_changed', 'validating',
  'candidate_ready', 'validated', 'no_matched_cache'
])
export function borrowReasonLabel(reason: string, t: (key: string) => string): string {
  return knownReasons.has(reason) ? t(`admin.codexGatewayBorrow.reasons.${reason}`) : reason
}
