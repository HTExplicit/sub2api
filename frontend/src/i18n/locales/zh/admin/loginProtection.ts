export default {
  loginProtection: {
    title: '登录防护',
    description: '累计面板登录错误，自动封禁来源 IP 对整个 Sub2API 的访问。',
    rules: '防护规则', enabled: '启用登录防护',
    window_minutes: '统计窗口（分钟）', max_failures: '允许错误次数', ban_hours: '封禁时长（小时）',
    range: '整数，1–{max}', invalidRange: '请输入 1–{max} 之间的整数。',
    validationFailed: '请检查以下设置：',
    summary: '{minutes} 分钟内超过 {failures} 次错误，即第 {trigger} 次错误后，封禁该 IP {hours} 小时。',
    scopeNote: '密码与登录二次验证码错误合并计数，成功登录不清零。同一公网 IP 的所有使用者共享封禁；封禁阻止面板、模型 API 和新的 WebSocket 请求，已有连接自然结束。',
    disableNote: '关闭后暂停计数和拦截，已有记录继续自然到期；重新开启后仍执行未到期的封禁。修改规则不会改变已有封禁的到期时间。',
    bans: '当前封禁 IP', blockedAt: '封禁时间', expiresAt: '到期时间',
    empty: '当前没有未到期的封禁。', reload: '刷新', unban: '解封', unbanIP: '解封 IP {ip}',
    unbanNote: '解封会同时清除该 IP 的错误计数。已封禁 IP 无法访问此页面，请从其他未封禁 IP 管理。',
    saved: '登录防护规则已保存', unbanned: '已解封 {ip}',
    loadFailed: '加载登录防护失败，请重试。', saveFailed: '保存登录防护失败。', unbanFailed: '解封失败，请重试。',
  },
}
