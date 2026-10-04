// The account types whose upstream model list the backend can read, per platform: fetchUpstreamModelList and the
// request builders of backend/internal/service/upstream_models.go refuse every other pair before any upstream is
// called. A platform without an entry has no model list at all (TypeSafe: System One).
const upstreamModelSyncAccountTypes = new Map<string, readonly string[]>([
  ['anthropic', ['oauth', 'setup-token', 'apikey']],
  ['openai', ['oauth', 'apikey']],
  ['gemini', ['oauth', 'apikey']],
  ['antigravity', ['oauth', 'upstream', 'apikey']],
  ['grok', ['oauth', 'apikey']],
  ['kimi', ['apikey']],
  ['zhipu', ['apikey']],
  ['deepseek', ['apikey']],
  ['minimax', ['apikey']],
  ['opencode_go', ['apikey']]
])

// A credential as Account.GetCredential reads it: text, or a number as text.
function credential(credentials: Record<string, unknown> | undefined, key: string): string {
  const value = credentials?.[key]
  return typeof value === 'string' ? value.trim() : typeof value === 'number' ? String(value) : ''
}

/**
 * Whether 同步上游支持的模型 can work for an account of this platform, type and credentials. Every place that
 * offers or starts a sync asks here, so none is sent that the backend refuses before it reaches the upstream.
 * Two pairs depend on the credentials as well, as in the backend: an Antigravity API-key account lists models only
 * through a compatible gateway, whose base URL ends in /antigravity (buildAntigravityAPIKeyModelsRequest), and a
 * Gemini OAuth account with a project is Code Assist, which has no model list (buildGeminiUpstreamModelsRequest).
 */
export function supportsUpstreamModelSync(platform: string, type: string, credentials?: Record<string, unknown>): boolean {
  if (!upstreamModelSyncAccountTypes.get(platform)?.includes(type)) return false
  if (platform === 'antigravity' && type === 'apikey') {
    return /\/antigravity$/i.test(credential(credentials, 'base_url').replace(/\/+$/, ''))
  }
  if (platform === 'gemini' && type === 'oauth') return credential(credentials, 'project_id') === ''
  return true
}
