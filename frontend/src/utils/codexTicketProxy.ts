// Parsing only: never save settings or contact a network endpoint on blur.
export function normalizeCodexTicketProxy(input: string, protocol = 'http'): string {
  let raw = input.trim()
  if (!raw) return ''
  if (raw.length > 16384) throw new Error('too_long')
  if (/[\r\n]/.test(raw)) {
    const fields: Record<string, string> = {}
    const labels: Record<string, string> = {
      'proxy server': 'host', proxy_server: 'host', server: 'host', host: 'host', hostname: 'host', 主机: 'host', 服务器: 'host', 节点: 'host', 代理服务器: 'host',
      port: 'port', 端口: 'port', username: 'username', user: 'username', 用户名: 'username', 账号: 'username', 帐号: 'username',
      password: 'password', pass: 'password', 密码: 'password', protocol: 'protocol', scheme: 'protocol', 协议: 'protocol'
    }
    for (const line of raw.split(/\r?\n/).map(value => value.trim()).filter(value => value && value !== '(' && value !== ')')) {
      const match = /^([^:：=]+)[:：=](.*)$/.exec(line)
      const key = match && labels[match[1].trim().toLowerCase()]
      if (!key || !match) throw new Error('unknown_field')
      if (key in fields) throw new Error('duplicate_field')
      fields[key] = match[2].trim()
    }
    if (!fields.host || !fields.port || ('username' in fields) !== ('password' in fields)) throw new Error('incomplete')
    const host = fields.host.includes(':') && !fields.host.startsWith('[') ? `[${fields.host}]` : fields.host
    const credentials = 'username' in fields ? `${encodeURIComponent(fields.username)}:${encodeURIComponent(fields.password)}@` : ''
    raw = `${fields.protocol || protocol}://${credentials}${host}:${fields.port}`
  } else if (!raw.includes('://')) {
    const parts = raw.split(':')
    if (!raw.includes('@') && !raw.startsWith('[') && parts.length >= 4) {
      if (!/^\d+$/.test(parts[1])) throw new Error('ambiguous')
      raw = `${protocol}://${encodeURIComponent(parts[2])}:${encodeURIComponent(parts.slice(3).join(':'))}@${parts[0]}:${parts[1]}`
    } else raw = `${protocol}://${raw}`
  }
  let url: URL
  try { url = new URL(raw) } catch { throw new Error('invalid_url') }
  if (!['http:', 'https:', 'socks5:', 'socks5h:'].includes(url.protocol) || !url.hostname || url.search || url.hash || (url.pathname && url.pathname !== '/')) throw new Error('invalid_url')
  // URL removes default ports, so check and preserve the explicit authority.
  const authority = raw.slice(raw.indexOf('://') + 3).split('/')[0]
  const portMatch = /:(\d+)$/.exec(authority)
  if (!portMatch || +portMatch[1] < 1 || +portMatch[1] > 65535) throw new Error('invalid_port')
  const user = url.username ? `${url.username}${url.password ? ':' + url.password : ''}@` : ''
  return `${url.protocol}//${user}${url.hostname}:${portMatch[1]}`
}
