/* global Sub2APIPluginBridge */
(async () => {
  const bridge = new Sub2APIPluginBridge()
  window.addEventListener('pagehide', () => bridge.dispose(), { once: true })
  const context = await bridge.context()
  document.documentElement.dataset.theme = context.theme || 'light'
  const en = String(context.locale).startsWith('en')
  const text = (zh, english) => en ? english : zh
  document.getElementById('title').textContent = text('模型目录与容量参考', 'Model catalog and capacity references')
  document.getElementById('description').textContent = text('参考数据用于容量规划；账号自定义值与实时上游声明继续按既有优先级处理。', 'References support capacity planning. Account overrides and live upstream declarations retain their existing precedence.')
  document.getElementById('search-label').textContent = text('搜索模型或供应商', 'Search model or provider')
  const entries = await bridge.invoke('catalog.list')
  const input = document.getElementById('search'), list = document.getElementById('entries')
  const node = (tag, value) => { const element = document.createElement(tag); element.textContent = value; return element }
  const render = () => {
    const query = input.value.trim().toLowerCase()
    const found = entries.filter(entry => [entry.model_id, entry.provider, ...(entry.aliases || [])].some(value => value.toLowerCase().includes(query)))
    document.getElementById('count').textContent = text(`${found.length} 项匹配，显示前 40 项。`, `${found.length} matches; showing the first 40.`)
    list.replaceChildren()
    for (const entry of found.slice(0, 40)) {
      const row = node('article', '')
      row.append(node('h3', entry.model_id), node('p', `${entry.provider} / ${entry.product} · ${text('上下文', 'Context')}: ${(entry.context_window || entry.max_input_tokens || entry.max_context_window || 0).toLocaleString()} · ${text('最大输出', 'Max output')}: ${entry.max_output_tokens?.toLocaleString() || '—'}`))
      if (entry.conditions) row.append(node('p', entry.conditions))
      if (entry.reference) row.append(node('p', `${text('订阅参考最大窗口', 'Subscription reference maximum')}: ${entry.reference.max_context_window.toLocaleString()}`))
      row.append(node('small', `${text('核对日期', 'Verified')}: ${entry.verified_at}`), node('small', entry.source_url))
      list.append(row)
    }
    bridge.resize(document.documentElement.scrollHeight)
  }
  input.addEventListener('input', render)
  render()
})().catch(error => { document.getElementById('description').textContent = error.message || 'Catalog unavailable' })
