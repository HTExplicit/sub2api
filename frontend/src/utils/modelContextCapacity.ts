export type ContextCapacityInputResult =
  | { valid: true; value: number | null }
  | { valid: false; error: 'invalid' | 'out_of_range' | 'fractional' }

/** Resolve a public model without guessing when aliases or upstream IDs are ambiguous. */
export function findContextCapacityRow<T extends { upstream_model_id: string; aliases: string[] }>(
  rows: readonly T[],
  publicID: string
): T | undefined {
  if (!publicID || publicID.trim() !== publicID) return undefined
  const aliases = rows.filter(row => row.aliases.includes(publicID))
  // An explicit public alias takes precedence over another row with the same upstream ID.
  if (aliases.length) return aliases.length === 1 ? aliases[0] : undefined
  const direct = rows.filter(row => row.upstream_model_id === publicID)
  return direct.length === 1 ? direct[0] : undefined
}

/** Parse decimal K/M without floating-point rounding; an empty draft clears an override. */
export function parseContextCapacityInput(raw: string): ContextCapacityInputResult {
  const input = raw.trim()
  if (!input) return { valid: true, value: null }

  const match = /^(\d+)(?:\.(\d+))?\s*([km])?$/i.exec(input)
  if (!match) return { valid: false, error: 'invalid' }

  const fraction = match[2] ?? ''
  const scale = match[3]?.toLowerCase() === 'm' ? 6 : match[3] ? 3 : 0
  let digits = match[1] + fraction
  if (fraction.length > scale) {
    const tailLength = fraction.length - scale
    if (!/^0+$/.test(digits.slice(-tailLength))) {
      return { valid: false, error: 'fractional' }
    }
    digits = digits.slice(0, -tailLength)
  } else {
    digits += '0'.repeat(scale - fraction.length)
  }

  digits = digits.replace(/^0+/, '') || '0'
  // Check the length before BigInt conversion so pasted input cannot create an enormous integer.
  if (digits.length > 16) return { valid: false, error: 'out_of_range' }
  const tokens = BigInt(digits)
  if (tokens <= 0n || tokens > BigInt(Number.MAX_SAFE_INTEGER)) {
    return { valid: false, error: 'out_of_range' }
  }
  return { valid: true, value: Number(tokens) }
}

/** Keep every token in display and edit values, including binary-sized vendor limits. */
export function formatContextCapacity(value: number | null | undefined): string {
  if (!value || !Number.isSafeInteger(value) || value < 0) return '—'
  const tokens = BigInt(value)
  const scale = value >= 1_000_000 ? 6 : value >= 1_000 ? 3 : 0
  if (!scale) return String(value)
  const divisor = 10n ** BigInt(scale)
  const fraction = String(tokens % divisor).padStart(scale, '0').replace(/0+$/, '')
  return `${tokens / divisor}${fraction ? `.${fraction}` : ''}${scale === 6 ? 'M' : 'K'}`
}

export function areContextCapacityDraftsValid(drafts: Record<string, string>): boolean {
  return Object.values(drafts).every((raw) => parseContextCapacityInput(raw).valid)
}

/** Only touched model IDs are submitted; null explicitly removes that model's override. */
export function buildContextOverridePatch(drafts: Record<string, string>): Record<string, number | null> {
  return Object.fromEntries(Object.entries(drafts).map(([modelID, raw]) => {
    const parsed = parseContextCapacityInput(raw)
    if (!parsed.valid) throw new Error(`Invalid context capacity for model ${modelID}`)
    return [modelID, parsed.value]
  }))
}
