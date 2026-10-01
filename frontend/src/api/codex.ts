export interface CodexModelsManifestResult {
  content: string
  modelCount: number
  responseBytes: number
}

function normalizeCodexGatewayRoot(baseUrl: string): string {
  const fallback = typeof window !== 'undefined' ? window.location.origin : ''
  return (baseUrl || fallback).trim().replace(/\/+$/, '').replace(/\/v1$/i, '')
}

// The ChatGPT-style manifest route takes no client_version: the gateway asks
// with its configured current Codex version, so a stale pinned version cannot
// filter newer models out of the downloaded catalog.
export function buildCodexModelsManifestUrl(baseUrl: string): string {
  return `${normalizeCodexGatewayRoot(baseUrl)}/backend-api/codex/models`
}

// Version-free remote catalog for model_catalog_url (Codex 0.156.0+). Codex
// appends its own client_version, which selects the same server-side Codex
// manifest as the download above.
export function buildCodexModelCatalogUrl(baseUrl: string): string {
  return `${normalizeCodexGatewayRoot(baseUrl)}/v1/models`
}

function isCodexModelsManifest(value: unknown): value is { models: unknown[] } {
  return typeof value === 'object' && value !== null && Array.isArray((value as { models?: unknown }).models)
}

export async function fetchCodexModelsManifest(
  baseUrl: string,
  apiKey: string,
  signal?: AbortSignal
): Promise<CodexModelsManifestResult> {
  const response = await fetch(buildCodexModelsManifestUrl(baseUrl), {
    method: 'GET',
    headers: {
      Accept: 'application/json',
      Authorization: `Bearer ${apiKey}`
    },
    cache: 'no-store',
    signal
  })

  if (!response.ok) {
    throw new Error(`Codex models request failed with status ${response.status}`)
  }

  const text = await response.text()
  const payload: unknown = JSON.parse(text)
  if (!isCodexModelsManifest(payload)) {
    throw new Error('Codex models response is not a valid manifest')
  }

  return {
    content: JSON.stringify(payload, null, 2),
    modelCount: payload.models.length,
    responseBytes: new TextEncoder().encode(text).byteLength
  }
}
