/* global Sub2APIPluginBridge */
(async () => {
  const bridge = new Sub2APIPluginBridge()
  window.addEventListener('pagehide', () => bridge.dispose(), { once: true })
  const context = await bridge.context()
  document.documentElement.dataset.theme = context.theme || 'light'
  const en = String(context.locale).startsWith('en')
  await bridge.invoke('tools.describe')
  document.getElementById('title').textContent = en ? 'Account tools' : '账号工具'
  document.getElementById('description').textContent = en ? 'Manage folders and tags from Accounts. Connection tests offer reasoning levels supported by each model; batch tests retain each selection when retried.' : '在账号页管理文件夹与标签。连接测试按每个模型实际支持的档位选择推理强度，批量任务重试保留各账号的原选择。'
  bridge.resize(document.documentElement.scrollHeight)
})().catch(error => { document.getElementById('description').textContent = error.message || 'Account tools unavailable' })
