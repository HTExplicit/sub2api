export default {
  deepseekReasoning: {
    title: 'DeepSeek 默认推理强度',
    description: '下游请求没有设置推理强度时，使用这里配置的默认档位。',
    enabled: '启用 DeepSeek 缺省推理强度',
    effort: '默认推理强度',
    unsaved: '有未保存的修改',
    rules: '只补未设置的请求，仍受分组的推理强度映射、上限和拒绝规则约束。客户端明确指定档位或关闭思考时，继续按原有规则处理。Max 可能增加推理耗时和消耗。',
    scope: '覆盖 Responses、Chat Completions 和 Messages。按最终映射后的 DeepSeek 模型生效，包含中转线路；能力未知或不支持所选档位的旧型号不补值。关闭后恢复原有行为。',
    saved: 'DeepSeek 默认推理强度已保存',
    loadFailed: '无法加载 DeepSeek 推理配置',
    saveFailed: '无法保存 DeepSeek 推理配置'
  }
}
