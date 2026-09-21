/* global Sub2APIPluginBridge */
{
let disposed = false
let observer, stopContext = () => {}
;(async () => {
  const bridge = new Sub2APIPluginBridge()
  window.addEventListener('pagehide', () => { disposed = true; stopContext(); observer?.disconnect(); bridge.dispose() }, { once: true })
  let context = await bridge.context()
  if (disposed) return
  document.documentElement.dataset.theme = context.theme || 'light'
  let en = String(context.locale).startsWith('en')
  const text = (zh, english) => en ? english : zh
  const node = (tag, value = '') => { const element = document.createElement(tag); element.textContent = value; return element }
  const translations = []
  const translated = (tag, zh, english) => {
    const element = node(tag, text(zh, english))
    translations.push(() => { element.textContent = text(zh, english) })
    return element
  }
  let updateAvailability = () => {}, renderCatalog = () => {}
  stopContext = bridge.onContextChange(next => {
    if (disposed) return
    context = next; en = String(next.locale).startsWith('en')
    document.documentElement.dataset.theme = next.theme || 'light'
    translations.forEach(update => update())
    updateAvailability(); renderCatalog()
  })
  const app = document.getElementById('app')
  observer = new ResizeObserver(() => { if (!disposed) bridge.resize(document.documentElement.scrollHeight) })
  observer.observe(app)
  app.append(translated('h2', 'Cindy 目录与策略', 'Cindy catalog and policies'))
  app.append(translated('p', '目录提供精确模型名称、协议能力与计费参考。更改设置不清除已有账号健康记录。', 'The catalog supplies exact model identities, protocol capabilities and pricing references. Settings changes preserve existing account health records.'))
  let config = await bridge.config()
  if (disposed) return
  const fields = [
    ['balance_detection', '余额不足与临时健康状态识别', 'Detect exhausted balance and transient health states'],
    ['catalog_enabled', '完整模型目录与价格参考', 'Full model catalog and pricing references'],
    ['search_enabled', 'Cindy 搜索', 'Cindy search']
  ]
  const controls = fields.map(([key, zh, english]) => {
    const input = node('input'); input.type = 'checkbox'; input.checked = !!config[key]
    const label = node('label'); label.append(input, translated('span', zh, english)); app.append(label)
    return { key, input }
  })
  const status = node('p'); status.setAttribute('role', 'status')
  const save = translated('button', '保存设置', 'Save settings'); save.type = 'button'
  let saving = false
  updateAvailability = () => {
    save.disabled = saving || context.available === false
    controls.forEach(({ input }) => { input.disabled = context.available === false })
  }
  updateAvailability()
  save.onclick = async () => {
    if (disposed || saving || context.available === false) return
    const submitted = Object.fromEntries(controls.map(({ key, input }) => [key, input.checked]))
    saving = true; updateAvailability()
    try {
      const result = await bridge.save({ ...config, ...submitted })
      if (disposed) return
      config = result
      let dirty = false
      controls.forEach(({ key, input }) => {
        if (input.checked === submitted[key]) input.checked = !!config[key]
        else dirty = true
      })
      status.textContent = dirty ? text('设置已保存；后续修改仍未保存', 'Settings saved; later edits are still unsaved') : text('设置已保存', 'Settings saved')
      await refresh()
    } catch (error) { if (!disposed) status.textContent = error.message || text('保存失败', 'Save failed') }
    finally { if (!disposed) { saving = false; updateAvailability() } }
  }
  app.append(save, status)
  const search = node('input'); search.type = 'search'
  const translateSearch = () => { search.setAttribute('aria-label', text('搜索模型', 'Search models')); search.placeholder = text('搜索模型', 'Search models') }
  translateSearch(); translations.push(translateSearch)
  const count = node('p'), list = node('section'); let entries = [], refreshRevision = 0
  const render = () => {
    const query = search.value.trim().toLowerCase()
    const found = entries.filter(entry => JSON.stringify(entry).toLowerCase().includes(query))
    count.textContent = text(`${found.length} 项匹配，显示前 40 项。`, `${found.length} matches; showing the first 40.`)
    list.replaceChildren()
    found.slice(0, 40).forEach(entry => {
      const row = node('article')
      row.append(node('h3', entry.public_id || entry.id || entry.model_id), node('p', `${entry.live_upstream_id || entry.upstream_model || ''} · ${(entry.endpoints || []).join(', ')}`))
      list.append(row)
    })
  }
  renderCatalog = render
  const refresh = async () => {
    if (disposed || context.available === false) return
    const revision = ++refreshRevision
    try {
      const result = await bridge.invoke('provider.describe')
      if (disposed || revision !== refreshRevision) return
      if (!Array.isArray(result)) throw new Error(text('目录格式无效', 'Invalid catalog response'))
      entries = result; render()
    } catch (error) { if (!disposed && revision === refreshRevision) count.textContent = error.message || text('目录不可用', 'Catalog unavailable') }
  }
  search.addEventListener('input', render)
  app.append(search, count, list)
  await refresh()
})().catch(error => { if (!disposed) document.getElementById('app').textContent = error.message || 'Cindy provider unavailable' })
}
