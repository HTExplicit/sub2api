export default {
  reasoningRecovery: {
    title: 'Reasoning Recovery',
    description: 'Whether the gateway recovers automatically when an OpenAI upstream rejects a request because its reasoning ciphertext is no longer valid. Applies to all OpenAI accounts.',
    enabled: 'Recover invalid reasoning ciphertext',
    enabledHint: 'When on, after an explicit signature-verification error and before any semantic output has been sent, the gateway strips only the ciphertext field of the rejected reasoning items, retries at most once more on the same account, and remembers that old ciphertext for 24 hours; new reasoning is not removed. Auto-passthrough accounts are covered as well. This may add upstream charges and does not restore the original reasoning chain. When off, such requests return the error directly.',
    scope: 'Applies only to requests actually sent over HTTP/SSE on the native Responses, Compact and Chat-to-Responses paths. Messages, WebSocket (including HTTP ingress routed to WS) and native Chat-only upstreams are not covered. The 24 hours are a logical limit; Redis persistence files and backups may retain historical bytes.',
    save: 'Save',
    saved: 'Reasoning recovery setting saved',
    reload: 'Refresh',
    loadFailed: 'Loading failed',
    saveFailed: 'Saving failed'
  }
}
