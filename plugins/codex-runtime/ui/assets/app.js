/* global Sub2APIPluginBridge */
(async () => {
  const bridge = new Sub2APIPluginBridge()
  const app = document.getElementById('app')
  window.addEventListener('pagehide', () => bridge.dispose(), { once: true })
  const context = await bridge.context()
  document.documentElement.dataset.theme = context.theme || 'light'
  const en = String(context.locale).startsWith('en')
  const text = (zh, english) => en ? english : zh
  const el = (tag, label, className) => { const node = document.createElement(tag); if (label) node.textContent = label; if (className) node.className = className; return node }
  const resize = () => bridge.resize(document.documentElement.scrollHeight)
  new ResizeObserver(resize).observe(app)
  const result = el('div', '', 'result')
  const show = (message, error = false) => { result.className = `result ${error ? 'error' : ''}`; result.textContent = message }
  const button = (label, action, primary = false) => {
    const control = el('button', label, primary ? 'primary' : '')
    control.type = 'button'
    control.onclick = async () => { control.disabled = true; try { await action() } catch (error) { show(error.message, true) } finally { control.disabled = false } }
    return control
  }
  const configuration = context.mode === 'configuration' || context.mode === 'admin.settings'
  app.append(el('h2', configuration ? text('Codex 运行设置', 'Codex runtime settings') : text('Cookie 路由操作', 'Cookie route operation')))
  if (configuration) {
    let config = await bridge.config()
    const enabled = el('input'); enabled.type = 'checkbox'; enabled.checked = !!config.enabled
    const toggle = el('label', '', 'check'); toggle.append(enabled, document.createTextNode(text('启用 Cookie 采集与业务出口复验', 'Enable Cookie acquisition and route verification'))); app.append(toggle)
    const compression = el('input'); compression.type = 'checkbox'; compression.checked = config.request_zstd !== false
    const compressionToggle = el('label', '', 'check'); compressionToggle.append(compression, document.createTextNode(text('压缩 Codex Responses 请求体', 'Compress Codex Responses requests'))); app.append(compressionToggle)
    const address = el('textarea'); address.rows = 4; address.autocomplete = 'off'; address.spellcheck = false; address.value = config.proxy_url || ''
    const addressLabel = el('label', text('采集代理', 'Acquisition proxy')); addressLabel.append(address); app.append(addressLabel)
    const controls = el('div', '', 'controls'), field = el('label', text('协议', 'Protocol'), 'field'), protocol = el('select')
    for (const scheme of ['http', 'https', 'socks5', 'socks5h']) { const option = el('option', scheme); option.value = scheme; protocol.append(option) }
    protocol.value = /^(https?|socks5h?):\/\//.exec(address.value)?.[1] || 'http'
    field.append(protocol); controls.append(field)
    controls.append(button(text('测试连接', 'Test connection'), async () => {
      const value = await bridge.invoke('proxy.test', { proxy_url: address.value, protocol: protocol.value })
      result.replaceChildren()
      const connected = value.network_reachable && ['proxy_reachable', 'target_http_status'].includes(value.code)
      result.append(el('p', connected ? text(`代理链路已连接，目标返回 HTTP ${value.http_status}；本次未发送账号凭据。`, `Proxy connected; target returned HTTP ${value.http_status}. No account credential was sent.`) : text('代理连接或证书验证失败。', 'Proxy connection or certificate verification failed.')))
      for (const stage of value.stages || []) result.append(el('p', `${stage.name}: ${stage.success ? '✓' : '✗'} ${stage.message || ''}`))
      if (value.certificate_fingerprint) result.append(el('p', `${text('证书指纹', 'Certificate fingerprint')}: ${value.certificate_fingerprint}`))
    }))
    controls.append(button(text('复制', 'Copy'), async () => {
      address.focus(); address.select()
      if (!document.execCommand('copy')) throw new Error(text('复制失败，请手动复制。', 'Copy failed; copy the selected text manually.'))
      show(text('已复制', 'Copied'))
    }))
    controls.append(button(text('清除代理并停用票据', 'Clear proxy and stop tickets'), async () => {
      config = await bridge.save({ ...config, proxy_url: '', enabled: false }); address.value = ''; enabled.checked = false; show(text('已清除并关闭', 'Cleared and disabled'))
    }))
    app.append(controls, el('p', text('连接测试验证代理与证书；成功连接不代表 Cookie 路由已验证。', 'Connection tests verify the proxy and certificate; they do not verify the Cookie route.'), 'muted'))
    const actions = el('div', '', 'actions')
    actions.append(button(text('保存设置', 'Save settings'), async () => {
      config = await bridge.save({ ...config, enabled: enabled.checked, proxy_url: address.value, proxy_protocol: protocol.value, request_zstd: compression.checked })
      address.value = config.proxy_url || ''; show(text('设置已保存', 'Settings saved'))
    }, true)); app.append(actions, result)
    return
  }
  const status = await bridge.status()
  const settings = JSON.parse(status.status_json || '{}')
  const accountIDs = context.account_id ? [context.account_id] : context.account_ids || []
  if (accountIDs.length > 100) throw new Error(text('一次最多操作 100 个账号。', 'At most 100 accounts can be operated at once.'))
  app.append(el('p', text(`已选择 ${accountIDs.length} 个账号。每个账号和模型每周期最多一次采集加一次业务出口复验；有效资格默认跳过。`, `${accountIDs.length} accounts selected. Each account/model cycle has one acquisition and one business-route verification; valid qualifications are skipped.`), 'muted'))
  const modelBox = el('div', '', 'models'), choices = []
  for (const model of settings.models || []) { const check = el('input'); check.type = 'checkbox'; check.value = model; check.checked = true; choices.push(check); const label = el('label', '', 'check'); label.append(check, document.createTextNode(model)); modelBox.append(label) }
  app.append(modelBox)
  const force = el('input'); force.type = 'checkbox'
  if (context.operation !== 'stop') { const label = el('label', '', 'check'); label.append(force, document.createTextNode(text('重新采集并复验仍有效的资格', 'Reacquire and verify a still-valid route'))); app.append(label) }
  let operationKey = null
  const actions = el('div', '', 'actions')
  actions.append(button(context.operation === 'stop' ? text('停止续期', 'Stop renewal') : text('开始采集', 'Start acquisition'), async () => {
    const models = choices.filter(item => item.checked).map(item => item.value)
    if (!models.length || !accountIDs.length) throw new Error(text('请选择账号和模型。', 'Select accounts and models.'))
    const items = accountIDs.flatMap(account_id => models.map(model => ({ account_id, payload: { model, force: force.checked }, label: model })))
    operationKey ||= `codex-ticket-${crypto.randomUUID()}`
    await bridge.submit(context.operation === 'stop' ? 'stop' : 'harvest', items, operationKey)
    show(text('任务已开始；关闭窗口不会停止任务。', 'Job started; closing this view will not stop it.'))
    operationKey = null
  }, true)); app.append(actions, result)
})().catch(error => { document.getElementById('app').textContent = error.message || 'Unable to load plugin view' })
