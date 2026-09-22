/* global Sub2APIPluginBridge */
(async () => {
  const bridge = new Sub2APIPluginBridge()
  window.addEventListener('pagehide', () => bridge.dispose(), { once: true })
  const context = await bridge.context()
  document.documentElement.dataset.theme = context.theme || 'light'
  const en = String(context.locale).startsWith('en')
  const text = (zh, english) => en ? english : zh
  document.getElementById('title').textContent = text('提示词应用策略', 'Prompt application policy')
  document.getElementById('description').textContent = text('使用当前已发布模板。应用前核对内容摘要与长度，并遵守模板的启用、压缩请求和可见性设置。', 'Uses the currently published template. Content integrity, activation, compaction and visibility settings are checked before application.')
  const limits = await bridge.invoke('prompt.describe')
  const list = document.getElementById('limits')
  for (const [label, value] of [[text('模板上限', 'Template limit'), limits.document_limit], [text('组合提示词上限', 'Compiled prompt limit'), limits.compiled_limit]]) {
    const term = document.createElement('dt'), definition = document.createElement('dd')
    term.textContent = label; definition.textContent = `${(value / 1024).toLocaleString()} KiB`
    list.append(term, definition)
  }
  bridge.resize(document.documentElement.scrollHeight)
})().catch(error => { document.getElementById('description').textContent = error.message || 'Policy unavailable' })
