import { AccountImportParseError, parseAccountImportFiles } from '@/utils/accountImportParser'

self.onmessage = async (event: MessageEvent<File[]>) => {
  try {
    self.postMessage({ payload: await parseAccountImportFiles(event.data) })
  } catch (error) {
    self.postMessage({ error: error instanceof AccountImportParseError
      ? { code: error.code, fileIndex: error.fileIndex }
      : { code: 'parse', fileIndex: 0 } })
  }
}
