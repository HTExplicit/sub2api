// The account search names an upstream API key by a digest term instead of carrying the key: this prefix followed by
// the lowercase hex SHA-256 of the key's UTF-8 bytes.
export const ACCOUNT_KEY_DIGEST_PREFIX = 'sha256:'

const ACCOUNT_KEY_DIGEST_TERM = /^sha256:[0-9a-f]{64}$/

export function isAccountKeyDigestTerm(value: string): boolean {
  return ACCOUNT_KEY_DIGEST_TERM.test(value)
}

// Digest term of a search text, surrounding whitespace ignored. Null for an empty text and where crypto.subtle is
// unavailable (insecure context): the text can then only be searched as a name.
export async function accountKeyDigestTerm(text: string): Promise<string | null> {
  const trimmed = text.trim()
  const subtle = globalThis.crypto?.subtle
  if (!trimmed || !subtle) return null
  const digest = await subtle.digest('SHA-256', new TextEncoder().encode(trimmed))
  return ACCOUNT_KEY_DIGEST_PREFIX + Array.from(new Uint8Array(digest), n => n.toString(16).padStart(2, '0')).join('')
}
