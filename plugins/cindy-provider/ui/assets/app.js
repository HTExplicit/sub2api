/* global Sub2APIPluginBridge */
(async () => {
  const bridge = new Sub2APIPluginBridge()
  window.addEventListener('pagehide', () => bridge.dispose(), { once: true })
  const context = await bridge.context()
  document.documentElement.dataset.theme = context.theme || 'light'
  const en = String(context.locale).startsWith('en')
  const text = (zh, english) => en ? english : zh
  const node = (tag, value = '') => { const element = document.createElement(tag); element.textContent = value; return element }
  const app = document.getElementById('app')
  new ResizeObserver(() => bridge.resize(document.documentElement.scrollHeight)).observe(app)
  app.append(node('h2', text('Cindy 目录与策略', 'Cindy catalog and policies')))
  app.append(node('p', text('目录提供精确模型名称、协议能力与计费参考。更改设置不清除已有账号健康记录。', 'The catalog supplies exact model identities, protocol capabilities and pricing references. Settings changes preserve existing account health records.')))
  let config = await bridge.config()
  const fields = [
    ['balance_detection', '余额不足与临时健康状态识别', 'Detect exhausted balance and transient health states'],
    ['catalog_enabled', '完整模型目录与价格参考', 'Full model catalog and pricing references'],
    ['search_enabled', 'Cindy 搜索', 'Cindy search']
  ]
  const controls = fields.map(([key, zh, english]) => {
    const input = node('input'); input.type = 'checkbox'; input.checked = !!config[key]
    const label = node('label'); label.append(input, document.createTextNode(text(zh, english))); app.append(label)
    return { key, input }
  })
  const status = node('p'); status.setAttribute('role', 'status')
  const save = node('button', text('保存设置', 'Save settings')); save.type = 'button'
  save.onclick = async () => {
    save.disabled = true
    try {
      config = await bridge.save({ ...config, ...Object.fromEntries(controls.map(({ key, input }) => [key, input.checked])) })
      controls.forEach(({ key, input }) => { input.checked = !!config[key] })
      status.textContent = text('设置已保存', 'Settings saved')
      await refresh()
    } catch (error) { status.textContent = error.message || text('保存失败', 'Save failed') }
    finally { save.disabled = false }
  }
  app.append(save, status)
  const search = node('input'); search.type = 'search'; search.setAttribute('aria-label', text('搜索模型', 'Search models')); search.placeholder = text('搜索模型', 'Search models')
  const count = node('p'), list = node('section'); let entries = []
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
  const refresh = async () => { entries = await bridge.invoke('provider.describe'); render() }
  search.addEventListener('input', render)
  app.append(search, count, list)
  await refresh()
})().catch(error => { document.getElementById('app').textContent = error.message || 'Cindy provider unavailable' })
