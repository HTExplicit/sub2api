import { computed, reactive, ref } from 'vue'
import { defineStore } from 'pinia'
import accountJobsAPI, {
  type AccountJob,
  type AccountJobItem,
  type AccountJobItemListParams,
  type AccountJobListParams,
  type DuplicateMergeRequest,
} from '@/api/admin/accountJobs'
import { useAppStore } from '@/stores/app'
import { i18n } from '@/i18n'
import { clearAccountOperationKeys } from '@/api/accountOperationIdempotency'

const POLL_INTERVAL_MS = 3_000
const TERMINAL_STATUSES = new Set(['succeeded', 'partially_succeeded', 'failed', 'canceled'])

export function isTerminalAccountJob(job: AccountJob): boolean {
  return TERMINAL_STATUSES.has(job.status)
}

export const useAccountJobsStore = defineStore('accountJobs', () => {
  const recentJobs = ref<AccountJob[]>([])
  const completedJobs = ref<AccountJob[]>([])
  const currentJob = ref<AccountJob | null>(null)
  const selectedJobID = ref<number | null>(null)
  const items = ref<AccountJobItem[]>([])
  const drawerOpen = ref(false)
  const embeddedOpen = ref(false)
  const historyOpen = ref(false)
  const connectionLost = ref(false)
  const trackedJobs = ref<Record<number, AccountJob>>({})
  const dismissedJobs = ref<Set<number>>(new Set())
  const itemFilter = ref('')
  const detailCache = new Map<number, { items: AccountJobItem[]; page: typeof itemPage; filter: string }>()
  const loadingJobs = ref(false)
  const loadingCurrent = ref(false)
  const jobPage = reactive({ total: 0, page: 1, pageSize: 20 })
  const itemPage = reactive({ total: 0, page: 1, pageSize: 20 })
  const listFilters = reactive({ kind: '', status: '' })
  const trackedStatuses = new Map<number, AccountJob['status']>()
  const notifiedJobs = new Set<number>()
  const failuresFocused = new Set<number>()
  let poller: ReturnType<typeof setInterval> | null = null
  let pollingEnabled = false
  let pollInFlight = false
  let generation = 0
  let listRequest: AbortController | null = null
  let currentRequest: AbortController | null = null
  let listRequestSerial = 0
  let currentRequestSerial = 0

  const activeJobs = computed(() => Object.values(trackedJobs.value).filter((job) => !isTerminalAccountJob(job)))
  const activeCount = computed(() => activeJobs.value.length)
  const visibleJobs = computed(() => Object.values(trackedJobs.value)
    .filter(job => !dismissedJobs.value.has(job.id))
    .sort((a, b) => b.id - a.id))
  let activeRequest: AbortController | null = null
  let recoveryNeeded = false

  function hasKnownActiveJobs(): boolean {
    return activeJobs.value.length > 0
  }

  function stopPolling(): void {
    if (poller) {
      clearInterval(poller)
      poller = null
    }
    pollingEnabled = false
  }

  function syncPollingState(): void {
    if (!pollingEnabled || pollInFlight) return
    if (!hasKnownActiveJobs() && !recoveryNeeded && !connectionLost.value) {
      stopPolling()
      return
    }
    if (!poller) {
      poller = setInterval(() => { void poll() }, POLL_INTERVAL_MS)
    }
  }

  function notifyTerminal(job: AccountJob): void {
    const appStore = useAppStore()
    const key = `admin.accountTasks.notifications.${job.status}`
    const message = String(i18n.global.t(key, { name: i18n.global.t(`admin.accountTasks.kinds.${job.kind}`) }))
    if (job.status === 'succeeded') appStore.showSuccess(message)
    else if (job.status === 'partially_succeeded' || job.status === 'canceled') {
      appStore.showWarning(message)
    } else {
      appStore.showError(message)
    }
  }

  function observeTrackedTransition(job: AccountJob): void {
    const previous = trackedStatuses.get(job.id)
    if (
      previous
      && !TERMINAL_STATUSES.has(previous)
      && TERMINAL_STATUSES.has(job.status)
      && !notifiedJobs.has(job.id)
    ) {
      notifiedJobs.add(job.id)
      if (currentJob.value?.id !== job.id || (!drawerOpen.value && !embeddedOpen.value)) notifyTerminal(job)
    }
    if (previous && isTerminalAccountJob(job) && !TERMINAL_STATUSES.has(previous)) {
      completedJobs.value = [...completedJobs.value, job]
    }
    trackedStatuses.set(job.id, job.status)
    if (trackedJobs.value[job.id]) trackedJobs.value[job.id] = job
  }

  function updateRecent(job: AccountJob, allowInsert = false): void {
    const matchesFilters = (!listFilters.kind || listFilters.kind === job.kind)
      && (!listFilters.status || listFilters.status === job.status)
    const index = recentJobs.value.findIndex((candidate) => candidate.id === job.id)
    if (index >= 0) {
      if (matchesFilters) recentJobs.value[index] = job
      else {
        recentJobs.value.splice(index, 1)
        jobPage.total = Math.max(0, jobPage.total - 1)
      }
      return
    }
    if (allowInsert && matchesFilters && jobPage.page === 1) {
      recentJobs.value = [job, ...recentJobs.value].slice(0, jobPage.pageSize)
      jobPage.total += 1
    }
  }

  function invalidateCurrentRequest(): void {
    currentRequest?.abort()
    currentRequest = null
    currentRequestSerial += 1
    loadingCurrent.value = false
  }

  function track(job: AccountJob, options: { open?: boolean; embedded?: boolean } = {}): void {
    observeTrackedTransition(job)
    trackedJobs.value[job.id] = job
    dismissedJobs.value.delete(job.id)
    listRequest?.abort()
    listRequestSerial += 1
    loadingJobs.value = false
    updateRecent(job, true)
    if (options.open !== false) {
      selectJob(job.id, job)
      embeddedOpen.value = !!options.embedded
      drawerOpen.value = !options.embedded
    }
    if (!isTerminalAccountJob(job)) startPolling(false)
  }

  async function loadRecent(params: AccountJobListParams = {}): Promise<void> {
    const requestGeneration = generation
    const detailSerial = currentRequestSerial
    const requestSerial = ++listRequestSerial
    listRequest?.abort()
    const controller = new AbortController()
    listRequest = controller
    if (Object.prototype.hasOwnProperty.call(params, 'kind')) listFilters.kind = params.kind ?? ''
    if (Object.prototype.hasOwnProperty.call(params, 'status')) listFilters.status = params.status ?? ''
    loadingJobs.value = true
    try {
      const page = await accountJobsAPI.list({
        page: params.page ?? jobPage.page,
        page_size: params.page_size ?? jobPage.pageSize,
        kind: listFilters.kind || undefined,
        status: listFilters.status || undefined,
      }, { signal: controller.signal })
      if (generation !== requestGeneration || requestSerial !== listRequestSerial) return
      for (const job of page.items) if (trackedJobs.value[job.id]) observeTrackedTransition(job)
      recentJobs.value = page.items
      jobPage.total = page.total
      jobPage.page = page.page
      jobPage.pageSize = page.page_size
      if (currentJob.value && detailSerial === currentRequestSerial && !loadingCurrent.value) {
        const updated = page.items.find((job) => job.id === currentJob.value?.id)
        if (updated) currentJob.value = updated
      }
    } catch (error) {
      if (controller.signal.aborted || generation !== requestGeneration || requestSerial !== listRequestSerial) return
      throw error
    } finally {
      if (listRequest === controller) listRequest = null
      if (generation === requestGeneration && requestSerial === listRequestSerial) loadingJobs.value = false
    }
  }

  async function loadCurrent(
    jobID: number,
    params: AccountJobItemListParams = {},
  ): Promise<void> {
    const requestGeneration = generation
    const requestSerial = ++currentRequestSerial
    currentRequest?.abort()
    const controller = new AbortController()
    currentRequest = controller
    loadingCurrent.value = true
    try {
      const job = await accountJobsAPI.get(jobID, { signal: controller.signal })
      if (generation !== requestGeneration || requestSerial !== currentRequestSerial || selectedJobID.value !== jobID) return
      let status = params.status ?? itemFilter.value
      let requestedPage = params.page ?? itemPage.page
      if (isTerminalAccountJob(job) && job.failed_count > 0 && !failuresFocused.has(jobID) && params.status === undefined) {
        status = 'failed'
        requestedPage = 1
      }
      const page = await accountJobsAPI.listItems(jobID, {
        page: requestedPage,
        page_size: params.page_size ?? itemPage.pageSize,
        status: status || undefined,
      }, { signal: controller.signal })
      if (generation !== requestGeneration || requestSerial !== currentRequestSerial || selectedJobID.value !== jobID) return
      observeTrackedTransition(job)
      currentJob.value = job
      updateRecent(job)
      items.value = page.items
      itemPage.total = page.total
      itemPage.page = page.page
      itemPage.pageSize = page.page_size
      itemFilter.value = status
      if (isTerminalAccountJob(job) && job.failed_count > 0) failuresFocused.add(jobID)
      detailCache.set(jobID, { items: page.items, page: { ...itemPage }, filter: itemFilter.value })
      connectionLost.value = false
    } catch (error) {
      if (controller.signal.aborted || generation !== requestGeneration || requestSerial !== currentRequestSerial) return
      controller.abort()
      if ((error as { status?: number; response?: { status?: number } })?.status === 404
        || (error as { response?: { status?: number } })?.response?.status === 404) {
        trackedStatuses.delete(jobID)
        delete trackedJobs.value[jobID]
        selectedJobID.value = null
        currentJob.value = null
        items.value = []
        itemPage.total = 0
        itemPage.page = 1
        const index = recentJobs.value.findIndex((candidate) => candidate.id === jobID)
        if (index >= 0) {
          recentJobs.value.splice(index, 1)
          jobPage.total = Math.max(0, jobPage.total - 1)
        }
      }
      throw error
    } finally {
      if (currentRequest === controller) currentRequest = null
      if (generation === requestGeneration && requestSerial === currentRequestSerial) loadingCurrent.value = false
    }
  }

  async function openJob(
    jobID: number,
    params: AccountJobItemListParams = {},
  ): Promise<void> {
    const known = trackedJobs.value[jobID] ?? recentJobs.value.find((job) => job.id === jobID)
    selectJob(jobID, known)
    embeddedOpen.value = false
    historyOpen.value = false
    drawerOpen.value = true
    try {
      await loadCurrent(jobID, params)
    } catch {
      useAppStore().showError(String(i18n.global.t('admin.accountTasks.loadFailed')))
    }
  }

  function closeDrawer(): void {
    invalidateCurrentRequest()
    drawerOpen.value = false
    embeddedOpen.value = false
  }

  function selectJob(id: number, job?: AccountJob): void {
    invalidateCurrentRequest()
    const cache = detailCache.get(id)
    selectedJobID.value = id
    currentJob.value = job ?? null
    items.value = cache?.items ?? []
    Object.assign(itemPage, cache?.page ?? { total: 0, page: 1, pageSize: 20 })
    itemFilter.value = cache?.filter ?? ''
  }

  function dismissJob(id: number): void {
    if (trackedJobs.value[id] && !isTerminalAccountJob(trackedJobs.value[id])) return
    dismissedJobs.value.add(id)
    if (currentJob.value?.id === id) closeDrawer()
  }

  // Recover every active operation, independently of history pagination/filters.
  async function recoverActive(signal: AbortSignal, epoch: number): Promise<void> {
    for (const status of ['pending', 'running']) {
      let page = 1
      for (;;) {
        const result = await accountJobsAPI.list({ status, page, page_size: 100 }, { signal })
        if (epoch !== generation || signal.aborted) return
        for (const job of result.items) {
          observeTrackedTransition(job)
          trackedJobs.value[job.id] = job
        }
        if (!result.items.length || page * result.page_size >= result.total) break
        page++
      }
    }
    if (epoch === generation) recoveryNeeded = false
  }

  async function refreshDrawer(): Promise<void> {
    try {
      const jobID = selectedJobID.value
      await Promise.all([
        loadRecent(),
        jobID === null ? Promise.resolve() : loadCurrent(jobID),
      ])
    } catch {
      useAppStore().showError(String(i18n.global.t('admin.accountTasks.loadFailed')))
    }
  }

  async function poll(): Promise<void> {
    if (!pollingEnabled || pollInFlight) return
    const epoch = generation
    pollInFlight = true
    const controller = new AbortController()
    activeRequest = controller
    try {
      if (recoveryNeeded) await recoverActive(controller.signal, epoch)
      const activeIDs = activeJobs.value.map(job => job.id)
      for (let offset = 0; offset < activeIDs.length; offset += 5) {
        await Promise.all(activeIDs.slice(offset, offset + 5).map(async id => {
          try {
            const job = await accountJobsAPI.get(id, { signal: controller.signal })
            if (epoch !== generation || controller.signal.aborted) return
            observeTrackedTransition(job)
            updateRecent(job)
            if (currentJob.value?.id === id) currentJob.value = job
          } catch (error) {
            if ((error as { status?: number })?.status === 404 && epoch === generation) {
              delete trackedJobs.value[id]
              trackedStatuses.delete(id)
              return
            }
            throw error
          }
        }))
      }
      if (epoch !== generation || controller.signal.aborted) return
      const jobID = selectedJobID.value
      if ((drawerOpen.value || embeddedOpen.value) && jobID !== null) {
        await loadCurrent(jobID)
      }
      connectionLost.value = false
    } catch (error) {
      if (epoch === generation && !(error as { code?: string })?.code?.includes('CANCEL')) connectionLost.value = true
    } finally {
      if (epoch === generation) {
        activeRequest = null
        pollInFlight = false
        syncPollingState()
      }
    }
  }

  function startPolling(immediate = true): void {
    pollingEnabled = true
    if (immediate) { recoveryNeeded = true; void poll() }
    syncPollingState()
  }

  async function openDrawer(): Promise<void> {
    historyOpen.value = true
    try { await loadRecent({ page: 1, kind: '', status: '' }) }
    catch { useAppStore().showError(String(i18n.global.t('admin.accountTasks.loadFailed'))) }
  }

  async function cancelJob(jobID: number): Promise<AccountJob> {
    const job = await accountJobsAPI.cancel(jobID)
    observeTrackedTransition(job)
    updateRecent(job)
    if (currentJob.value?.id === job.id) currentJob.value = job
    return job
  }

  async function retryJob(jobID: number): Promise<AccountJob> {
    const replacement = await accountJobsAPI.retryFailed(jobID)
    track(replacement, { embedded: embeddedOpen.value })
    void loadCurrent(replacement.id).catch(() => { connectionLost.value = true })
    return replacement
  }

  async function reviewDuplicates(accountIDs: number[]): Promise<AccountJob> {
    const job = await accountJobsAPI.reviewDuplicates(accountIDs)
    track(job, { embedded: embeddedOpen.value })
    void loadCurrent(job.id).catch(() => { connectionLost.value = true })
    return job
  }

  async function mergeDuplicates(request: DuplicateMergeRequest): Promise<AccountJob> {
    const job = await accountJobsAPI.mergeDuplicates(request)
    track(job, { embedded: embeddedOpen.value })
    void loadCurrent(job.id).catch(() => { connectionLost.value = true })
    return job
  }

  function clear(): void {
    clearAccountOperationKeys()
    stopPolling()
    activeRequest?.abort()
    activeRequest = null
    pollInFlight = false
    recoveryNeeded = false
    listRequest?.abort()
    currentRequest?.abort()
    generation += 1
    recentJobs.value = []
    completedJobs.value = []
    currentJob.value = null
    selectedJobID.value = null
    items.value = []
    drawerOpen.value = false
    embeddedOpen.value = false
    historyOpen.value = false
    connectionLost.value = false
    trackedJobs.value = {}
    dismissedJobs.value = new Set()
    detailCache.clear()
    itemFilter.value = ''
    loadingJobs.value = false
    loadingCurrent.value = false
    jobPage.total = 0
    jobPage.page = 1
    jobPage.pageSize = 20
    itemPage.total = 0
    itemPage.page = 1
    itemPage.pageSize = 20
    listFilters.kind = ''
    listFilters.status = ''
    trackedStatuses.clear()
    notifiedJobs.clear()
    failuresFocused.clear()
  }

  return {
    recentJobs,
    completedJobs,
    activeJobs,
    activeCount,
    currentJob,
    items,
    drawerOpen,
    embeddedOpen,
    historyOpen,
    connectionLost,
    visibleJobs,
    itemFilter,
    dismissJob,
    loadingJobs,
    loadingCurrent,
    jobPage,
    itemPage,
    track,
    loadRecent,
    loadCurrent,
    openJob,
    openDrawer,
    refreshDrawer,
    closeDrawer,
    cancelJob,
    retryJob,
    reviewDuplicates,
    mergeDuplicates,
    startPolling,
    stopPolling,
    clear,
  }
})
