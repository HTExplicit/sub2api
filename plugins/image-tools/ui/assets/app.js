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
  app.append(node('h2', text('图像工具', 'Image tools')))
  app.append(node('p', text('Image Studio 使用账号现有的图像能力与分组权限。每次上游请求生成一张图像，一项任务最多四张。关闭功能会停止新的生成操作，已保存的结果保留。', 'Image Studio uses existing account image capabilities and group permissions. Each upstream request generates one image, with up to four images per job. Disabling the feature stops new generation work and preserves saved results.')))
  let config = await bridge.config()
  const fields = [['studio_enabled', '启用 Image Studio', 'Enable Image Studio'], ['responses_image_enabled', '启用 Cindy Responses 图像桥接', 'Enable Cindy Responses image bridge']]
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
      config = await bridge.save(Object.fromEntries(controls.map(({ key, input }) => [key, input.checked])))
      controls.forEach(({ key, input }) => { input.checked = !!config[key] })
      status.textContent = text('设置已保存', 'Settings saved')
    } catch (error) { status.textContent = error.message || text('保存失败', 'Save failed') }
    finally { save.disabled = false }
  }
  app.append(save, status)
})().catch(error => { document.getElementById('app').textContent = error.message || 'Image tools unavailable' })
