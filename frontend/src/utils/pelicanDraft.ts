import type { PelicanTestRequest } from '@/api/admin/pelicanTests'

export type PelicanDraft = Omit<PelicanTestRequest, 'client_task_id'>

function draftKey(): string {
  const user = JSON.parse(localStorage.getItem('auth_user') || 'null')
  return `pelican.draft.v1.${user?.id ?? 'current'}`
}

export function readPelicanDraft(): PelicanDraft | undefined {
  try {
    const value = JSON.parse(sessionStorage.getItem(draftKey()) || 'null')
    if (!value || !Array.isArray(value.targets) || value.targets.length > 5000 ||
      !value.targets.every((row: PelicanDraft['targets'][number]) => Number.isInteger(row.account_id) && row.account_id > 0 && typeof row.model_id === 'string' && typeof row.effort === 'string')) return
    return {
      repeat_count: typeof value.repeat_count === 'number' ? value.repeat_count : 1,
      generation_timeout_seconds: typeof value.generation_timeout_seconds === 'number' ? value.generation_timeout_seconds : 600,
      test_items: Array.isArray(value.test_items) ? value.test_items.filter((item: string) => item === 'pelican' || item === 'candy') : ['pelican', 'candy'],
      targets: value.targets.map((row: PelicanDraft['targets'][number]) => ({ account_id: row.account_id, model_id: row.model_id, effort: row.effort }))
    }
  } catch { return }
}

export function savePelicanDraft(value?: PelicanDraft): boolean {
  try {
    if (value?.targets.length) sessionStorage.setItem(draftKey(), JSON.stringify(value))
    else sessionStorage.removeItem(draftKey())
    return true
  } catch { return false }
}
