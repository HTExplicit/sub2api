import ImportWorker from './accountImport.worker?worker&inline'
import { AccountImportParseError } from './accountImportParser'
import type { AdminDataPayload } from './api'

export function readAccountImportFiles(files: File[], signal: AbortSignal): Promise<AdminDataPayload> {
  return new Promise((resolve, reject) => {
    if (signal.aborted) { reject(new DOMException('Aborted', 'AbortError')); return }
    const worker = new ImportWorker()
    const cleanup = () => { signal.removeEventListener('abort', abort); worker.terminate() }
    const abort = () => { cleanup(); reject(new DOMException('Aborted', 'AbortError')) }
    signal.addEventListener('abort', abort, { once: true })
    worker.onmessage = (event) => {
      cleanup()
      if (event.data.error) reject(new AccountImportParseError(event.data.error.code, event.data.error.fileIndex))
      else resolve(event.data.payload)
    }
    worker.onerror = () => { cleanup(); reject(new AccountImportParseError('parse', 0)) }
    worker.postMessage(files)
  })
}
