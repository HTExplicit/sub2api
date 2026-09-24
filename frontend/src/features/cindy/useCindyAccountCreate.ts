import { computed, onBeforeUnmount, reactive, ref, watch } from 'vue'
import type { AccountCreateDefaultTarget, ProviderCreateRequestV1 } from '@/types/accountCreate'
import { cindyCreateChoice } from './accountForm'

export const accountCreateDefaultTargets: AccountCreateDefaultTarget[] = ['concurrency', 'priority', 'rate_multiplier', 'load_factor', 'responses_mode']
interface Draft {
  contribution: typeof cindyCreateChoice
  values: Record<string, string>
  explicit: Set<AccountCreateDefaultTarget>
  catalog: string[]
  catalogLoaded: boolean
  catalogLoading: boolean
  catalogFailed: boolean
}
export function useCindyAccountCreate(options: {
  platform(): string; accountType(): string; baseURL(): string; actorID(): number | undefined
  loadCatalog(platform: 'cindy'): Promise<string[]>
}) {
  const draftState = ref<Draft>()
  const draft = computed(() => options.platform() === 'cindy' ? draftState.value : undefined)
  const required = computed(() => options.platform() === 'cindy')
  const choices = computed(() => [cindyCreateChoice])
  const contribution = computed(() => draft.value?.contribution)
  const definition = computed(() => contribution.value?.account_create)
  const available = computed(() => required.value && !!draft.value && !!options.actorID() && options.accountType() === 'apikey' &&
    options.baseURL().trim() === cindyCreateChoice.account_create.credential_ui.base_url)
  const values = computed<Record<string, string>>({ get: () => draft.value?.values || {}, set: value => { if (draft.value) draft.value.values = { ...value } } })
  const fieldsValid = computed(() => typeof (values.value.device_id ?? '') === 'string' && (values.value.device_id || '').length <= 64)
  let sequence = 0, muted = 0
  function activate(platform: string) {
    if (platform !== 'cindy') return undefined
    draftState.value ||= reactive({ contribution: cindyCreateChoice, values: {}, explicit: new Set<AccountCreateDefaultTarget>(), catalog: [], catalogLoaded: false, catalogLoading: false, catalogFailed: false })
    return draftState.value
  }
  function markExplicit(target: AccountCreateDefaultTarget) { if (!muted) draft.value?.explicit.add(target) }
  function preserveIntent<T>(write: () => T): T { muted++; try { return write() } finally { muted-- } }
  async function refreshCatalog() {
    const selected = draft.value
    if (!selected || !available.value || selected.catalogLoading) return
    const request = ++sequence, actor = options.actorID(), endpoint = options.baseURL()
    selected.catalogLoading = true; selected.catalogFailed = false
    const current = () => request === sequence && draft.value === selected && actor === options.actorID() && available.value && endpoint === options.baseURL()
    try {
      const ids = await options.loadCatalog('cindy')
      if (!current()) return
      if (!Array.isArray(ids) || ids.length > 10000 || ids.some(id => typeof id !== 'string' || !id || id.length > 256)) throw new Error('Invalid Cindy catalog')
      selected.catalog = [...new Set(ids)]; selected.catalogLoaded = true
    } catch { if (current()) selected.catalogFailed = true }
    finally { if (request === sequence) selected.catalogLoading = false }
  }
  function request(): ProviderCreateRequestV1 {
    if (!draft.value || !available.value || !fieldsValid.value) throw new Error('Cindy creation is unavailable')
    return { values: Object.fromEntries(Object.entries(values.value).filter(([key]) => key === 'device_id')),
      inherit_defaults: accountCreateDefaultTargets.filter(key => !draft.value!.explicit.has(key)) }
  }
  function assertRequest(expected: ProviderCreateRequestV1) {
    if (JSON.stringify(request()) !== JSON.stringify(expected)) throw new Error('Cindy create draft changed')
  }
  function reconcile() { if (!draft.value) return false; draft.value.catalogLoaded = false; void refreshCatalog(); return true }
  function reset() { sequence++; draftState.value = undefined }
  watch(() => [options.platform(), options.accountType(), options.baseURL(), available.value], () => {
    sequence++
    if (draftState.value) draftState.value.catalogLoading = false
    if (available.value && !draft.value?.catalogLoaded) void refreshCatalog()
  }, { flush: 'post' })
  watch(options.actorID, reset, { flush: 'sync' })
  onBeforeUnmount(reset)
  return { choices, draft, required, contribution, definition, available, stale: computed(() => false), values, fieldsValid,
    activate, markExplicit, preserveIntent, reconcile, refreshCatalog, request, assertRequest, reset }
}
