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
  app.append(node('h2', text('账号流量观测', 'Account traffic observations')))
  app.append(node('p', text('记录实际业务转发的结果，不触发测试请求。观测数据不用于限制账号或改变计费。', 'Records real gateway attempts without running test requests. Observations do not restrict accounts or change billing.')))
  const status = node('p'); status.setAttribute('role', 'status')
  if (context.mode === 'configuration' || context.mode === 'admin.settings') {
    let config = await bridge.config()
    const input = node('input'); input.type = 'checkbox'; input.checked = !!config.telemetry_enabled
    const label = node('label'); label.append(input, document.createTextNode(text('启用账号流量观测', 'Enable account traffic observations')))
    const theme = node('input'); theme.type = 'checkbox'; theme.checked = !!config.theme_enabled
    const themeLabel = node('label'); themeLabel.append(theme, document.createTextNode(text('启用平面主题', 'Enable flat theme')))
    const save = node('button', text('保存设置', 'Save settings')); save.type = 'button'
    save.onclick = async () => {
      save.disabled = true
      try { config = await bridge.save({ ...config, telemetry_enabled: input.checked, theme_enabled: theme.checked }); input.checked = !!config.telemetry_enabled; theme.checked = !!config.theme_enabled; status.textContent = text('设置已保存', 'Settings saved') }
      catch (error) { status.textContent = error.message || text('保存失败', 'Save failed') }
      finally { save.disabled = false }
    }
    app.append(label, themeLabel, save, status)
    return
  }
  const list = node('section'), refresh = node('button', text('刷新观测', 'Refresh observations')); refresh.type = 'button'
  const load = async () => {
    refresh.disabled = true
    try {
      const rows = await bridge.invoke('telemetry.read')
      list.replaceChildren()
      for (const row of rows) {
        const article = node('article'); article.append(node('h3', row.protocol.toUpperCase()))
        if (!row.observed_since_ms) { article.append(node('p', text('尚无观测数据', 'No observations yet'))); list.append(article); continue }
        const facts = node('dl')
        for (const [label, value] of [
          [text('累计开始', 'Started'), row.started], [text('已结束', 'Finished'), row.finished],
          [text('尚未结束', 'Unfinished'), row.unfinished], [text('近 60 秒请求', 'Requests in last 60s'), row.requests_last_60s],
          [text('成功完成率', 'Completed successfully'), row.completion_rate == null ? '—' : `${(row.completion_rate * 100).toFixed(1)}%`],
          ['429', row.upstream_429], ['5xx', row.upstream_5xx], [text('取消或未完成', 'Cancelled or incomplete'), row.cancelled],
          [text('其他失败', 'Other failures'), row.failed_other], [text('历史槽位峰值', 'Peak observed slots'), row.peak_in_flight]
        ]) facts.append(node('dt', label), node('dd', String(value)))
        article.append(facts, node('p', `${text('开始观测', 'Observed since')}: ${new Date(row.observed_since_ms).toLocaleString(en ? 'en' : 'zh-CN')}`))
        list.append(article)
      }
      status.textContent = rows.length ? '' : text('尚无观测数据', 'No observations yet')
    } catch (error) { status.textContent = error.message || text('观测暂不可用', 'Observations unavailable') }
    finally { refresh.disabled = false }
  }
  refresh.onclick = load
  app.append(refresh, status, list)
  await load()
})().catch(error => { document.getElementById('app').textContent = error.message || 'Observations unavailable' })
