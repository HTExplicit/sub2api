export default {
  reasoningRecovery: {
    title: '推理恢复',
    description: 'OpenAI 上游拒绝请求中已失效的推理密文时，网关是否自动恢复。对全部 OpenAI 账号生效。',
    enabled: '失效推理密文恢复',
    enabledHint: '开启后，遇到明确的验签错误且尚未输出语义内容时，只剥离被拒 reasoning 的密文字段，在同一账号最多额外重试一次，并记忆该旧密文 24 小时；不删除新推理。自动透传账号同样适用。可能增加调用费用，不代表恢复原推理链。关闭后，这类请求直接返回错误。',
    scope: '仅适用于实际 HTTP/SSE 的原生 Responses、Compact 与 Chat→Responses 路径。Messages、WebSocket（包括 HTTP 入口转 WS）、原生 Chat-only 上游不启用。24 小时是逻辑期限，Redis 持久化文件与备份可能保留历史字节。',
    save: '保存',
    saved: '推理恢复设置已保存',
    reload: '刷新',
    loadFailed: '加载失败',
    saveFailed: '保存失败'
  }
}
