import type { AdminDataPayload } from '@/types'

export class AccountImportParseError extends Error {
  // detail is the original JSON.parse error text or the concrete shape problem.
  constructor(public code: 'parse' | 'shape', public fileIndex: number, public detail = '') {
    super(detail || code)
  }
}

function accountImportShapeProblem(value: AdminDataPayload): string {
  if (!value || typeof value !== 'object' || Array.isArray(value)) return 'the file must contain a JSON object'
  if (value.type && !['sub2api-data', 'sub2api-bundle'].includes(value.type)) return `type "${value.type}" is not sub2api-data or sub2api-bundle`
  if (value.version && ![1, 2].includes(value.version)) return `version ${value.version} is not 1 or 2`
  if (!Array.isArray(value.accounts)) return 'accounts must be an array'
  if (!Array.isArray(value.proxies)) return 'proxies must be an array'
  return ''
}

export async function parseAccountImportFiles(files: Blob[]): Promise<AdminDataPayload> {
  const result: AdminDataPayload = {
    type: 'sub2api-data', version: 1, exported_at: new Date().toISOString(),
    accounts: [], proxies: [], skipped_shadows: 0,
  }
  for (let index = 0; index < files.length; index++) {
    let value: AdminDataPayload
    try { value = JSON.parse(await files[index]!.text()) } catch (error) {
      throw new AccountImportParseError('parse', index, error instanceof Error ? error.message : String(error))
    }
    const problem = accountImportShapeProblem(value)
    if (problem) throw new AccountImportParseError('shape', index, problem)
    if (files.length === 1) return value
    result.version = Math.max(result.version || 1, value.version || 1)
    for (const account of value.accounts) result.accounts.push(account)
    for (const proxy of value.proxies) result.proxies.push(proxy)
    result.skipped_shadows = Number(result.skipped_shadows || 0) + Number(value.skipped_shadows || 0)
  }
  return result
}
