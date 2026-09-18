import { describe, it, expect } from 'vitest'
import { normalizeCodexTicketProxy } from '../codexTicketProxy'
describe('ticket proxy parsing', () => {
  it('accepts labeled BestProxy-style fields in any order without losing credentials', () => {
    const value = normalizeCodexTicketProxy('Password: a@b:#\nPort: 2721\nUsername: user+zone\nProxy Server: proxy.example.com')
    const parsed = new URL(value)
    expect(parsed.protocol).toBe('http:')
    expect(decodeURIComponent(parsed.username)).toBe('user+zone')
    expect(decodeURIComponent(parsed.password)).toBe('a@b:#')
    expect(parsed.port).toBe('2721')
  })
  it('preserves explicit ports and supports standard and colon forms', () => {
    expect(normalizeCodexTicketProxy('h.example:80:u:p')).toBe('http://u:p@h.example:80')
    expect(normalizeCodexTicketProxy('u:p@h.example:8080')).toBe('http://u:p@h.example:8080')
    expect(normalizeCodexTicketProxy('socks5h://u:p@h.example:1080')).toBe('socks5h://u:p@h.example:1080')
    expect(normalizeCodexTicketProxy('密码：p\n主机：h.example\n端口：8080\n用户名：u')).toBe('http://u:p@h.example:8080')
  })
  it('rejects ambiguity, duplicates and incomplete credentials', () => {
    for (const value of ['u:p:host:90', 'Host: h\nPort: 80\nPort: 90', 'Host: h\nPort: 80\nUsername: u', 'http://h:70000']) expect(() => normalizeCodexTicketProxy(value)).toThrow()
  })
})
