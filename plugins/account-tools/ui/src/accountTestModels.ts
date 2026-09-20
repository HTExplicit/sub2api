import type { AccountAvailableModel } from './api'
import { filterCindyAccountTestModels, pickCindyAccountTestDefault, type CindyAccountLike } from './cindyOpenAIDefaults'

const geminiPriority = new Map(['gemini-3.1-flash-image', 'gemini-2.5-flash-image', 'gemini-3.5-flash', 'gemini-2.5-flash', 'gemini-2.5-pro', 'gemini-3-flash-preview', 'gemini-3-pro-preview', 'gemini-2.0-flash'].map((id, index) => [id, index]))

export function prepareAccountTestModels(account: CindyAccountLike, models: AccountAvailableModel[]): AccountAvailableModel[] {
  const filtered = filterCindyAccountTestModels(account, models)
  return account.platform === 'gemini' || account.platform === 'antigravity'
    ? [...filtered].sort((a, b) => (geminiPriority.get(a.id) ?? Number.MAX_SAFE_INTEGER) - (geminiPriority.get(b.id) ?? Number.MAX_SAFE_INTEGER))
    : filtered
}

export function accountTestModelsForMode(account: CindyAccountLike | null, models: AccountAvailableModel[], mode = 'text') {
  if (account?.platform !== 'grok') return models
  const image = (id: string) => id === 'grok-imagine' || id === 'grok-imagine-edit' || id.startsWith('grok-imagine-image')
  const video = (id: string) => id.startsWith('grok-imagine-video') || id.startsWith('grok-video')
  return models.filter(model => {
    const id = model.id.toLowerCase()
    return mode === 'image' ? image(id) : mode === 'video' ? video(id) : mode === 'text' && !image(id) && !video(id)
  })
}

export function defaultAccountTestModel(account: CindyAccountLike | null, models: AccountAvailableModel[], mode = 'text'): string {
  if (!models.length) return ''
  if (account?.platform === 'grok') {
    return (mode === 'text' ? models.find(m => m.id.includes('grok-4.5')) || models.find(m => m.id === 'grok') : undefined)?.id || models[0].id
  }
  return pickCindyAccountTestDefault(account, models)?.id
    || (account?.platform === 'gemini' ? models[0].id : models.find(m => m.id.includes('sonnet'))?.id)
    || models[0].id
}
