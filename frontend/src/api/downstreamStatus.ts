export type DownstreamReleaseStatus =
  | 'current'
  | 'sync_pending'
  | 'candidate_testing'
  | 'review_required'
  | 'release_ready'
  | 'approval_pending'
  | 'deploying'
  | 'failed'

export interface DownstreamStatusLink {
  label: 'official' | 'candidate' | 'review' | 'release' | 'production'
  url: string
}

export interface DownstreamStatusInfo {
  status: DownstreamReleaseStatus
  official_latest: string
  downstream_base: string
  candidate_pr?: { number: number; title: string; url: string }
  review_issue?: { number: number; title: string; url: string }
  downstream_release?: { tag: string; url: string }
  production_run?: { status: string; conclusion: string; url: string }
  links: DownstreamStatusLink[]
}

const REPOSITORY = 'HTExplicit/sub2api'
const API_ROOT = `https://api.github.com/repos/${REPOSITORY}`
const HTML_ROOT = `https://github.com/${REPOSITORY}`
const CACHE_TTL_MS = 5 * 60 * 1000

let cached: { key: string; at: number; value: DownstreamStatusInfo } | null = null

const trimVersion = (value: string): string => value.trim().replace(/^v/, '')
const upstreamBase = (value: string): string => trimVersion(value).split('-codexrip.')[0] || ''
const referencesOfficialVersion = (title: string, official: string): boolean =>
  (title.match(/\bv\d+\.\d+\.\d+\b/gi) || []).some(version => trimVersion(version) === official)

async function githubJSON<T>(path: string): Promise<T> {
  const response = await fetch(`${API_ROOT}${path}`, {
    headers: { Accept: 'application/vnd.github+json' },
    cache: 'no-store'
  })
  if (!response.ok) throw new Error(`GitHub status request failed: ${response.status}`)
  return response.json() as Promise<T>
}

export async function fetchDownstreamStatus(
  currentVersion: string,
  officialLatest: string,
  force = false
): Promise<DownstreamStatusInfo> {
  const current = trimVersion(currentVersion)
  const official = trimVersion(officialLatest)
  const key = `${current}|${official}`
  if (!force && cached?.key === key && Date.now() - cached.at < CACHE_TTL_MS) return cached.value

  const [release, pulls, runs] = await Promise.all([
    githubJSON<{ tag_name: string; html_url: string }>('/releases/latest').catch(() => null),
    githubJSON<Array<{
      number: number
      title: string
      html_url: string
      head: { ref: string }
      labels: Array<{ name: string }>
    }>>('/pulls?state=open&per_page=30').catch(() => []),
    githubJSON<{ workflow_runs: Array<{
      id: number
      display_title: string
      status: string
      conclusion: string | null
      html_url: string
    }> }>('/actions/workflows/production-deploy.yml/runs?event=workflow_dispatch&per_page=30')
      .catch(() => ({ workflow_runs: [] }))
  ])

  const expectedBranch = `sync/upstream-${official}`
  const candidate = pulls.find(item => item.head.ref === expectedBranch)
  const releaseTag = release?.tag_name?.replace(/^v/, '') || ''
  const relevantRelease = release && upstreamBase(releaseTag) === official ? release : null
  const reviewIssue = !candidate && !relevantRelease && upstreamBase(current) !== official
    ? (await githubJSON<Array<{
        number: number
        title: string
        html_url: string
        labels: Array<{ name: string }>
        pull_request?: unknown
      }>>('/issues?state=open&labels=upstream-review-required&per_page=30').catch(() => []))
        .find(item =>
          !item.pull_request &&
          item.labels.some(label => label.name === 'upstream-review-required') &&
          referencesOfficialVersion(item.title, official)
        )
    : undefined
  const relevantRun = relevantRelease
    ? runs.workflow_runs.find(item => item.display_title.includes(relevantRelease.tag_name))
    : undefined
  let deployJobStatus = ''
  if (relevantRun && relevantRun.status !== 'completed') {
    const jobs = await githubJSON<{ jobs: Array<{ name: string; status: string }> }>(
      `/actions/runs/${relevantRun.id}/jobs?per_page=20`
    ).catch(() => ({ jobs: [] }))
    deployJobStatus = jobs.jobs.find(job => job.name === 'deploy')?.status || ''
  }

  let status: DownstreamReleaseStatus = upstreamBase(current) === official ? 'current' : 'sync_pending'
  if (candidate) {
    status = candidate.labels.some(label => label.name === 'upstream-review-required')
      ? 'review_required'
      : 'candidate_testing'
  } else if (reviewIssue) {
    status = 'review_required'
  } else if (relevantRelease && current !== releaseTag) {
    if (!relevantRun) {
      status = 'release_ready'
    } else if (relevantRun.conclusion === 'failure' || relevantRun.conclusion === 'cancelled') {
      status = 'failed'
    } else if (
      relevantRun.status === 'queued' ||
      relevantRun.status === 'waiting' ||
      deployJobStatus === 'queued' ||
      deployJobStatus === 'waiting'
    ) {
      status = 'approval_pending'
    } else if (relevantRun.status !== 'completed') {
      status = 'deploying'
    } else if (relevantRun.conclusion !== 'success') {
      status = 'failed'
    }
  }

  const value: DownstreamStatusInfo = {
    status,
    official_latest: official,
    downstream_base: upstreamBase(current),
    candidate_pr: candidate
      ? { number: candidate.number, title: candidate.title, url: candidate.html_url }
      : undefined,
    review_issue: reviewIssue
      ? { number: reviewIssue.number, title: reviewIssue.title, url: reviewIssue.html_url }
      : undefined,
    downstream_release: relevantRelease
      ? { tag: relevantRelease.tag_name, url: relevantRelease.html_url }
      : undefined,
    production_run: relevantRun
      ? { status: relevantRun.status, conclusion: relevantRun.conclusion || '', url: relevantRun.html_url }
      : undefined,
    links: [
      { label: 'official', url: `https://github.com/Wei-Shaw/sub2api/releases/tag/v${official}` },
      ...(candidate ? [{ label: 'candidate' as const, url: candidate.html_url }] : []),
      ...(reviewIssue ? [{ label: 'review' as const, url: reviewIssue.html_url }] : []),
      ...(relevantRelease ? [{ label: 'release' as const, url: relevantRelease.html_url }] : []),
      ...(relevantRun ? [{ label: 'production' as const, url: relevantRun.html_url }] : []),
      ...(!candidate && !reviewIssue && !relevantRelease
        ? [{ label: 'candidate' as const, url: `${HTML_ROOT}/pulls?q=is%3Apr+head%3A${expectedBranch}` }]
        : [])
    ]
  }
  cached = { key, at: Date.now(), value }
  return value
}

export function clearDownstreamStatusCache(): void {
  cached = null
}
