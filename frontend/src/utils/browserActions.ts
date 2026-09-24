// Browser helpers formerly routed through the plugin bridge (`ui.confirm`, `ui.download`).

export async function confirmAction(message: string): Promise<boolean> {
  return window.confirm(message)
}

export async function downloadBlob(blob: Blob, filename: string): Promise<void> {
  const url = URL.createObjectURL(blob)
  const link = document.createElement('a')
  link.href = url
  link.download = Array.from(filename, character => character.charCodeAt(0) < 32 ? '_' : character)
    .join('').replace(/[\\/:*?"<>|]/g, '_').slice(0, 180)
  document.body.append(link)
  link.click()
  link.remove()
  window.setTimeout(() => URL.revokeObjectURL(url), 1000)
}
