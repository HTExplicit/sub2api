// Public UI SDK. The sandbox receives a short-lived bridge token, never the
// administrator's session credentials. Network actions are performed by the host.
class PluginBridge {
  constructor() {
    this.token = new URLSearchParams(location.hash.slice(1)).get('bridge_token') || ''
    this.sequence = 0
    this.pending = new Map()
    this.contextListeners = new Set()
    this.preferenceListeners = new Set()
    this.listener = event => {
      const message = event.data
      if (event.source !== parent || !message || message.source !== 'sub2api-plugin-host' || message.bridge_token !== this.token) return
      if (message.type === 'extension.context.updated') { for (const listener of this.contextListeners) listener(message.context); return }
      if (message.type === 'preference.updated') { for (const listener of this.preferenceListeners) listener(message.key, message.value); return }
      const request = this.pending.get(message.request_id)
      if (!request) return
      this.pending.delete(message.request_id)
      clearTimeout(request.timer)
      request.cleanup?.()
      if (message.ok) request.resolve(message)
      else {
        const error = new Error(message.error || 'Operation failed')
        error.code = message.code
        error.status = message.status
        error.response = { status: message.status, data: { code: message.code, message: error.message } }
        request.reject(error)
      }
    }
    window.addEventListener('message', this.listener)
    this.notify('sub2api.plugin.ready')
  }

  notify(type, fields = {}) {
    parent.postMessage({ source: 'sub2api-plugin-ui', bridge_token: this.token, type, ...fields }, '*')
  }

  request(type, fields = {}, signal) {
    const request_id = `extension-${++this.sequence}`
    return new Promise((resolve, reject) => {
      if (signal?.aborted) { reject(new DOMException('Request canceled', 'AbortError')); return }
      const abort = () => {
        const request = this.pending.get(request_id)
        if (!request) return
        clearTimeout(request.timer); this.pending.delete(request_id)
        this.notify('extension.cancel', { target_request_id: request_id })
        reject(new DOMException('Request canceled', 'AbortError'))
      }
      const timer = setTimeout(() => { this.pending.get(request_id)?.cleanup?.(); this.pending.delete(request_id); reject(new Error('Operation timed out')) }, 30000)
      this.pending.set(request_id, { resolve, reject, timer, cleanup: () => signal?.removeEventListener('abort', abort) })
      signal?.addEventListener('abort', abort, { once: true })
      try { this.notify(type, { ...fields, request_id }) }
      catch (error) { clearTimeout(timer); this.pending.get(request_id)?.cleanup?.(); this.pending.delete(request_id); reject(error) }
    })
  }

  async context() { return (await this.request('extension.context')).context }
  async config() { return (await this.request('config.load')).config }
  async status() { return (await this.request('plugin.status')).result }
  async save(config) { return (await this.request('config.save', { config })).config }
  async invoke(operation, payload = {}) { return (await this.request('extension.invoke', { operation, payload })).result }
  async submit(operation, items, operation_key) { return (await this.request('extension.job.submit', { operation, items, operation_key })).job }
  async job(job_id) { return (await this.request('extension.job.get', { job_id })).job }
  async resource(operation, input = {}, signal) { return (await this.request('extension.resource', { operation, input }, signal)).result }
  async event(name, payload) { return this.request('extension.event', { name, payload }) }
  async openJob(job_id) { return this.request('extension.job.open', { job_id }) }
  async preference(key) { return (await this.request('preference.read', { key })).value }
  async savePreference(key, value) { return this.request('preference.write', { key, value }) }
  onContextChange(listener) { this.contextListeners.add(listener); return () => this.contextListeners.delete(listener) }
  onPreferenceChange(listener) { this.preferenceListeners.add(listener); return () => this.preferenceListeners.delete(listener) }
  resize(height) { this.notify('ui.resize', { height }) }
  dispose() {
    window.removeEventListener('message', this.listener)
    for (const request of this.pending.values()) { clearTimeout(request.timer); request.cleanup?.(); request.reject(new Error('Plugin view closed')) }
    this.pending.clear()
    this.contextListeners.clear()
    this.preferenceListeners.clear()
  }
}

globalThis.Sub2APIPluginBridge = PluginBridge
