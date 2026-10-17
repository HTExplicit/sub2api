import type { PelicanResultSummary } from '@/api/admin/pelicanTests'

const labels: Record<string, [string, string]> = {
  passed: ['通过', 'Passed'], invalid: ['未通过', 'Not passed'], uncertain: ['证据不足', 'Inconclusive'],
  not_reviewed: ['未审核', 'Not reviewed'], error: ['异常', 'Error'], pending: ['等待中', 'Pending'],
  running: ['进行中', 'Running'], cancelled: ['已取消', 'Cancelled'], complete: ['已完成', 'Completed'],
  failed: ['请求失败', 'Request failed'], incomplete: ['未完成', 'Incomplete'], skipped: ['已跳过', 'Skipped'],
  candy: ['糖果检测中', 'Testing candy'], generating: ['生成动画中', 'Generating'], rendering: ['多帧截图中', 'Rendering frames'],
  reviewing: ['视觉审核中', 'Reviewing'], preparing: ['准备中', 'Preparing'], queued: ['排队中', 'Queued'],
  not_selected: ['未选择', 'Not selected']
}
export function pelicanLabel(value: string, locale = 'zh') { return labels[value]?.[locale.startsWith('zh') ? 0 : 1] || value }
export function pelicanTone(value: string) {
  if (value === 'passed') return 'bg-emerald-50 text-emerald-800 dark:bg-emerald-950 dark:text-emerald-200'
  if (value === 'invalid' || value === 'uncertain') return 'bg-amber-50 text-amber-800 dark:bg-amber-950 dark:text-amber-200'
  if (['error', 'failed', 'incomplete'].includes(value)) return 'bg-red-50 text-red-800 dark:bg-red-950 dark:text-red-200'
  return 'bg-gray-100 text-gray-600 dark:bg-dark-800 dark:text-gray-300'
}
export function pelicanFailed(item: PelicanResultSummary) { return ['failed', 'incomplete', 'skipped'].includes(item.status) || !!item.evaluation && ['invalid', 'error'].includes(item.evaluation.verdict) }
export function decodePelicanEvidence(value?: string) { if (!value) return ''; return new TextDecoder().decode(Uint8Array.from(atob(value), c => c.charCodeAt(0))) }
