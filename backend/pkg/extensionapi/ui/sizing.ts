/** Intrinsic iframe content sizing; viewport height must never be its input. */
export function createPluginSizing(root: HTMLElement, send: (height: number) => void, inline: () => boolean) {
  const doc = root.ownerDocument
  const view = doc.defaultView!
  const panels = new Set<HTMLElement>()
  let frame: number | null = null
  let stopped = false
  let previous: number | undefined

  function measure() {
    frame = null
    if (stopped) return
    const rootTop = Math.max(0, root.getBoundingClientRect().top + view.scrollY)
    let bottom = rootTop + Math.max(root.offsetHeight, root.scrollHeight)
    for (const menu of doc.querySelectorAll<HTMLElement>('[role="listbox"], [role="menu"]')) {
      const box = menu.getBoundingClientRect()
      if (box.height) bottom = Math.max(bottom, box.bottom + view.scrollY)
    }
    const present = new Set(doc.querySelectorAll<HTMLElement>('[role="dialog"] .modal-content'))
    for (const panel of panels) if (!present.has(panel)) { observer.unobserve(panel); panels.delete(panel) }
    for (const panel of present) {
      if (!panels.has(panel)) { panels.add(panel); observer.observe(panel) }
      if (!panel.getBoundingClientRect().height) continue
      const body = panel.querySelector<HTMLElement>('.modal-body')
      const natural = panel.scrollHeight + (body ? Math.max(0, body.scrollHeight - body.clientHeight) : 0)
      bottom = Math.max(bottom, natural + 32)
    }
    const height = Math.max(inline() ? 0 : 120, Math.min(1200, Math.ceil(bottom) + (inline() ? 0 : 24)))
    if (height !== previous) { previous = height; send(height) }
  }
  function schedule() {
    if (!stopped && frame === null) frame = view.requestAnimationFrame(measure)
  }
  const observer = new ResizeObserver(schedule)
  const mutations = new MutationObserver(schedule)
  observer.observe(root)
  mutations.observe(doc.body, { childList: true, subtree: true, attributes: true, attributeFilter: ['class', 'style', 'hidden', 'open'] })
  view.addEventListener('resize', schedule)
  doc.fonts?.addEventListener('loadingdone', schedule)
  void doc.fonts?.ready.then(schedule)
  schedule()
  return {
    schedule,
    dispose() {
      stopped = true
      if (frame !== null) view.cancelAnimationFrame(frame)
      observer.disconnect()
      mutations.disconnect()
      view.removeEventListener('resize', schedule)
      doc.fonts?.removeEventListener('loadingdone', schedule)
      panels.clear()
    }
  }
}
