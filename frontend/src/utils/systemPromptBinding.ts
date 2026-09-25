import type { SystemPromptBinding } from '@/api/admin/systemPrompts'

// Largest account selection one binding request accepts.
export const systemPromptBindingLimit = 1000

/** Reads an account's extra.system_prompt value; anything else is inherit. */
export function readSystemPromptBinding(value: unknown): SystemPromptBinding {
  const binding = value && typeof value === 'object' ? value as { mode?: unknown; prompt_id?: unknown } : {}
  if (binding.mode === 'off') return { mode: 'off' }
  if (binding.mode === 'custom' && typeof binding.prompt_id === 'string' && binding.prompt_id.trim()) {
    return { mode: 'custom', prompt_id: binding.prompt_id.trim() }
  }
  return { mode: 'inherit' }
}
