import type { AdminDataPayload } from '@/types'

export class AccountImportParseError extends Error {
  constructor(public code: 'parse' | 'shape', public fileIndex: number) {
    super(code)
  }
}

export async function parseAccountImportFiles(files: Blob[]): Promise<AdminDataPayload> {
  const result: AdminDataPayload = {
    type: 'sub2api-data', version: 1, exported_at: new Date().toISOString(),
    accounts: [], proxies: [], skipped_shadows: 0,
  }
  for (let index = 0; index < files.length; index++) {
    let value: AdminDataPayload
    try { value = JSON.parse(await files[index]!.text()) } catch {
      throw new AccountImportParseError('parse', index)
    }
    if (!value || typeof value !== 'object' || Array.isArray(value)
      || (value.type && !['sub2api-data', 'sub2api-bundle'].includes(value.type))
      || (value.version && ![1, 2].includes(value.version))
      || !Array.isArray(value.accounts) || !Array.isArray(value.proxies)) {
      throw new AccountImportParseError('shape', index)
    }
    if (files.length === 1) return value
    result.version = Math.max(result.version || 1, value.version || 1)
    for (const account of value.accounts) result.accounts.push(account)
    for (const proxy of value.proxies) result.proxies.push(proxy)
    result.skipped_shadows = Number(result.skipped_shadows || 0) + Number(value.skipped_shadows || 0)
  }
  return result
}
