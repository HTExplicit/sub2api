import { AccountImportParseError, parseAccountImportFiles } from '@/utils/accountImportParser'

self.onmessage = async (event: MessageEvent<File[]>) => {
  try {
    self.postMessage({ payload: await parseAccountImportFiles(event.data) })
  } catch (error) {
    self.postMessage({ error: error instanceof AccountImportParseError
      ? { code: error.code, fileIndex: error.fileIndex, detail: error.detail }
      : { code: 'parse', fileIndex: 0, detail: error instanceof Error ? error.message : String(error) } })
  }
}
