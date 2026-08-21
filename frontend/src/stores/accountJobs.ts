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

const POLL_INTERVAL_MS = 3_000
const TERMINAL_STATUSES = new Set(['succeeded', 'partially_succeeded', 'failed', 'canceled'])

export function isTerminalAccountJob(job: AccountJob): boolean {
  return TERMINAL_STATUSES.has(job.status)
}

export const useAccountJobsStore = defineStore('accountJobs', () => {
  const recentJobs = ref<AccountJob[]>([])
  const currentJob = ref<AccountJob | null>(null)
  const selectedJobID = ref<number | null>(null)
  const items = ref<AccountJobItem[]>([])
  const drawerOpen = ref(false)
  const loadingJobs = ref(false)
  const loadingCurrent = ref(false)
  const jobPage = reactive({ total: 0, page: 1, pageSize: 20 })
  const itemPage = reactive({ total: 0, page: 1, pageSize: 20 })
  const listFilters = reactive({ kind: '', status: '' })
  const trackedStatuses = new Map<number, AccountJob['status']>()
  const notifiedJobs = new Set<number>()
  let poller: ReturnType<typeof setInterval> | null = null
  let generation = 0

  const activeJobs = computed(() => recentJobs.value.filter((job) => !isTerminalAccountJob(job)))
  const activeCount = computed(() => activeJobs.value.length)

  function notifyTerminal(job: AccountJob): void {
    const appStore = useAppStore()
    const key = `admin.accountTasks.notifications.${job.status}`
    const message = String(i18n.global.t(key, { id: job.id }))
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
      notifyTerminal(job)
    }
    if (previous) trackedStatuses.set(job.id, job.status)
  }

  function upsertRecent(job: AccountJob): void {
    const index = recentJobs.value.findIndex((candidate) => candidate.id === job.id)
    if (index >= 0) {
      recentJobs.value[index] = job
      return
    }
    recentJobs.value = [job, ...recentJobs.value]
  }

  function track(job: AccountJob, options: { open?: boolean } = {}): void {
    const previous = trackedStatuses.get(job.id)
    if (previous) observeTrackedTransition(job)
    else trackedStatuses.set(job.id, job.status)
    upsertRecent(job)
    if (options.open !== false) {
      currentJob.value = job
      selectedJobID.value = job.id
      items.value = []
      itemPage.total = 0
      itemPage.page = 1
      drawerOpen.value = true
    }
  }

  async function loadRecent(params: AccountJobListParams = {}): Promise<void> {
    const requestGeneration = generation
    if (Object.prototype.hasOwnProperty.call(params, 'kind')) listFilters.kind = params.kind ?? ''
    if (Object.prototype.hasOwnProperty.call(params, 'status')) listFilters.status = params.status ?? ''
    loadingJobs.value = true
    try {
      const page = await accountJobsAPI.list({
        page: params.page ?? jobPage.page,
        page_size: params.page_size ?? jobPage.pageSize,
        kind: listFilters.kind || undefined,
        status: listFilters.status || undefined,
      })
      if (generation !== requestGeneration) return
      for (const job of page.items) observeTrackedTransition(job)
      recentJobs.value = page.items
      jobPage.total = page.total
      jobPage.page = page.page
      jobPage.pageSize = page.page_size
      if (currentJob.value) {
        const updated = page.items.find((job) => job.id === currentJob.value?.id)
        if (updated) currentJob.value = updated
      }
    } finally {
      if (generation === requestGeneration) loadingJobs.value = false
    }
  }

  async function loadCurrent(
    jobID: number,
    params: AccountJobItemListParams = {},
  ): Promise<void> {
    const requestGeneration = generation
    loadingCurrent.value = true
    try {
      const [job, page] = await Promise.all([
        accountJobsAPI.get(jobID),
        accountJobsAPI.listItems(jobID, {
          page: params.page ?? itemPage.page,
          page_size: params.page_size ?? itemPage.pageSize,
          status: params.status,
        }),
      ])
      if (generation !== requestGeneration || selectedJobID.value !== jobID) return
      observeTrackedTransition(job)
      currentJob.value = job
      upsertRecent(job)
      items.value = page.items
      itemPage.total = page.total
      itemPage.page = page.page
      itemPage.pageSize = page.page_size
    } finally {
      if (generation === requestGeneration) loadingCurrent.value = false
    }
  }

  async function openJob(
    jobID: number,
    params: AccountJobItemListParams = {},
  ): Promise<void> {
    if (selectedJobID.value !== jobID) {
      items.value = []
      itemPage.total = 0
      itemPage.page = 1
    }
    const known = recentJobs.value.find((job) => job.id === jobID)
    selectedJobID.value = jobID
    currentJob.value = known ?? null
    drawerOpen.value = true
    await loadCurrent(jobID, params)
  }

  function closeDrawer(): void {
    drawerOpen.value = false
  }

  function openDrawer(): void {
    drawerOpen.value = true
  }

  async function cancelJob(jobID: number): Promise<AccountJob> {
    const job = await accountJobsAPI.cancel(jobID)
    observeTrackedTransition(job)
    upsertRecent(job)
    if (currentJob.value?.id === job.id) currentJob.value = job
    return job
  }

  async function retryJob(jobID: number): Promise<AccountJob> {
    const replacement = await accountJobsAPI.retryFailed(jobID)
    track(replacement)
    return replacement
  }

  async function reviewDuplicates(accountIDs: number[]): Promise<AccountJob> {
    const job = await accountJobsAPI.reviewDuplicates(accountIDs)
    track(job)
    return job
  }

  async function mergeDuplicates(request: DuplicateMergeRequest): Promise<AccountJob> {
    const job = await accountJobsAPI.mergeDuplicates(request)
    track(job)
    return job
  }

  async function poll(): Promise<void> {
    try {
      await loadRecent()
      const jobID = selectedJobID.value
      if (drawerOpen.value && jobID) {
        await loadCurrent(jobID)
      }
    } catch {
      console.error('Account job polling failed')
    }
  }

  function startPolling(): void {
    if (poller) return
    void poll()
    poller = setInterval(() => {
      void poll()
    }, POLL_INTERVAL_MS)
  }

  function stopPolling(): void {
    if (poller) clearInterval(poller)
    poller = null
  }

  function clear(): void {
    stopPolling()
    generation += 1
    recentJobs.value = []
    currentJob.value = null
    selectedJobID.value = null
    items.value = []
    drawerOpen.value = false
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
  }

  return {
    recentJobs,
    activeJobs,
    activeCount,
    currentJob,
    items,
    drawerOpen,
    loadingJobs,
    loadingCurrent,
    jobPage,
    itemPage,
    track,
    loadRecent,
    loadCurrent,
    openJob,
    openDrawer,
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
