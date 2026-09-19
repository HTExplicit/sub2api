// Public UI SDK. The sandbox receives a short-lived bridge token, never the
// administrator's session credentials. Network actions are performed by the host.
class PluginBridge {
  constructor() {
    this.token = new URLSearchParams(location.hash.slice(1)).get('bridge_token') || ''
    this.sequence = 0
    this.pending = new Map()
    this.listener = event => {
      const message = event.data
      if (event.source !== parent || !message || message.source !== 'sub2api-plugin-host' || message.bridge_token !== this.token) return
      const request = this.pending.get(message.request_id)
      if (!request) return
      this.pending.delete(message.request_id)
      clearTimeout(request.timer)
      if (message.ok) request.resolve(message)
      else request.reject(new Error(message.error || 'Operation failed'))
    }
    window.addEventListener('message', this.listener)
    this.notify('sub2api.plugin.ready')
  }

  notify(type, fields = {}) {
    parent.postMessage({ source: 'sub2api-plugin-ui', bridge_token: this.token, type, ...fields }, '*')
  }

  request(type, fields = {}) {
    const request_id = `extension-${++this.sequence}`
    return new Promise((resolve, reject) => {
      const timer = setTimeout(() => { this.pending.delete(request_id); reject(new Error('Operation timed out')) }, 30000)
      this.pending.set(request_id, { resolve, reject, timer })
      this.notify(type, { ...fields, request_id })
    })
  }

  async context() { return (await this.request('extension.context')).context }
  async config() { return (await this.request('config.load')).config }
  async status() { return (await this.request('plugin.status')).result }
  async save(config) { return (await this.request('config.save', { config })).config }
  async invoke(operation, payload = {}) { return (await this.request('extension.invoke', { operation, payload })).result }
  async submit(operation, items, operation_key) { return (await this.request('extension.job.submit', { operation, items, operation_key })).job }
  async job(job_id) { return (await this.request('extension.job.get', { job_id })).job }
  resize(height) { this.notify('ui.resize', { height }) }
  dispose() {
    window.removeEventListener('message', this.listener)
    for (const request of this.pending.values()) { clearTimeout(request.timer); request.reject(new Error('Plugin view closed')) }
    this.pending.clear()
  }
}

globalThis.Sub2APIPluginBridge = PluginBridge
