import type { SystemPromptBinding } from '@/api/admin/systemPrompts'

// Largest account selection one binding request accepts.
export const systemPromptBindingLimit = 1000

// The platforms whose requests pass an insertion point: the backend's SystemPromptPlatforms
// (backend/internal/service/system_prompt.go), the platforms forwarded as Messages, Chat Completions, Responses or
// Gemini. TypeSafe is not one of them: System One has no system or instructions field.
const systemPromptPlatforms: ReadonlySet<string> = new Set([
  'anthropic', 'openai', 'gemini', 'antigravity', 'grok', 'kimi', 'zhipu', 'deepseek', 'minimax', 'opencode_go'
])

/**
 * Whether an account of this platform can receive a system prompt. A binding is offered only where it can: the
 * backend stores none for the other accounts and does not count them.
 */
export function takesSystemPrompt(platform: string): boolean {
  return systemPromptPlatforms.has(platform)
}

/** Reads an account's extra.system_prompt value; anything else is inherit. */
export function readSystemPromptBinding(value: unknown): SystemPromptBinding {
  const binding = value && typeof value === 'object' ? value as { mode?: unknown; prompt_id?: unknown } : {}
  if (binding.mode === 'off') return { mode: 'off' }
  if (binding.mode === 'custom' && typeof binding.prompt_id === 'string' && binding.prompt_id.trim()) {
    return { mode: 'custom', prompt_id: binding.prompt_id.trim() }
  }
  return { mode: 'inherit' }
}
