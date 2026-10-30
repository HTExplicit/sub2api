# GPT-6 模型、Codex 运行与提示词

官方基线见 [upstream-base](upstream-base)。源码当前行为见 [DOWNSTREAM.md](../DOWNSTREAM.md)，设置、身份与回滚边界见 [原生契约](native-domains.md)。本页仅维护提示词和账号测试的细则。

## 模型同步与容量

模型名称、模板、容量、metadata、manifest／ETag 和客户端目录采用固定官方版本；本站容量解析器、覆盖、来源绑定、自动刷新及 Cindy 专属 registry 已退役。完整规则见 [模型目录契约](../DOWNSTREAM.md#model-discovery-capacity-and-client-catalogs)，历史 SQL、已存值与诊断记录保留。

手动同步先经挑选器确认增删，取消不改表单，保存账号后才持久化；来源或账号变化使旧请求失效。推理档位按实际型号与转发线路能力保留；鹈鹕仍用已有 64000 默认输出预算或显式更小值。

## Codex 流式响应

对于接受 SSE 的 `/backend-api/codex/responses` 请求，上游 200 响应缺少 `Content-Type` 时补为 `text/event-stream`。终态及协议转换以 [转发契约](../DOWNSTREAM.md#openai-forwarding-and-model-identity) 为准。

## 系统提示词

“扩展功能 → 系统提示词”维护全局开关、一个站点默认和最多 50 条库项（名称、正文 ≤64 KiB、prepend/append、auto/system/developer）。`settings.system_prompts` 存整份 JSON；`GET/PUT /api/v1/admin/system-prompts` 读写配置并返回使用统计，删除仍被自定义账号引用的项返回 409。默认关闭且无默认项，不产生注入；生产开关状态由运维记录维护。

账号的 `accounts.extra.system_prompt` 为 inherit/off/custom＋prompt_id，缺省继承。账号编辑、行菜单与批量栏（≤1000）只通过 `PUT /api/v1/admin/system-prompts/bindings` 写绑定。普通编辑保留该键，批量 extra 合并不写；创建、导入和复制只保留有注入点平台的有效绑定。

支持范围以 `service.SystemPromptPlatforms` 为准，包含 Cline／Command Code。TypeSafe 的 System One 无注入位置，不提供入口、不计使用统计、不持有绑定；绑定接口跳过并返回实际写入数。批量选择含未加载账号时保留入口并提示跳过不支持平台。迁移 268 只清理无注入位置平台的旧绑定。

请求只读内存快照和调度账号绑定，不查库；保存立即替换快照，其他进程在 60 秒内刷新。读取失败不注入并告警。全局关闭一律零注入；每个最终协议最多注入一次：

| 最终协议 | 投递 |
| --- | --- |
| Responses | auto/system 加入 instructions 前/后；developer 输入项放最前或开头 system/developer 之后。Lite、compact 不注入。 |
| Chat Completions | 在消息开头或开头 system/developer 后插入；上游仅支持 system 时 developer 转 system。 |
| Anthropic Messages | 顶层 system 前/后，developer 转 system；Claude OAuth 伪装前注入，随原 system 迁入 `[System Instructions]`。 |
| Gemini／Antigravity | systemInstruction parts 前/后。 |

count_tokens／countTokens 不注入，`prompt_cache_key` 不改写。JSON/SSE/WS 回显的 instructions 还原客户端原值，未传为 null。Claude OAuth 伪装块只由上游网关设置及块编辑器维护。

迁移 259 导入旧纯文本正文，默认关闭、无默认项，并删除旧提示词／Skill 表；旧模板、版本、Skill 注册表及 `/skills/security-research/current/` 已删除，旧镜像回退须有对应表的迁移前备份。迁移 269 仅按形状移除引用已删除远程规则路径的 bootstrap 块；其他正文、顺序与设置保留，清除后为空或不符合形状时不改写。

## Codex 运行设置

当前入口为“Codex → 高级设置” `/admin/codex/advanced`；旧 `/admin/codex-runtime` 兼容跳转。压缩开关只用于 OAuth 发往 Codex 后端的流式 Responses，compact／models 保持明文；保存沿用 TOTP。`GET/PUT /api/v1/admin/settings/codex-runtime` 的 request_zstd 及缺省规则见 [设置契约](native-domains.md#settings)。

## 失效推理密文恢复

采用官方 HTTP／WS 恢复；本站去密文、24 小时拒绝记忆、额外 SSE／缺失 ID 修复及专用换号均已退役，独立页面与 `GET/PUT /api/v1/admin/reasoning-recovery` 已删除。旧设置、历史 SQL 与诊断账本保留，不再控制转发。现行边界见 [密文恢复契约](../DOWNSTREAM.md#openai-encrypted-content-recovery)。

## 账号连接测试与批量测试

单号、批量与定时测试显示上游原文错误及失败终态，不额外脱敏或截断；成功要求有效协议终态与可见文本。Codex 另列实际发出的请求头，不含 Authorization。测试计划和实际 effort 按型号及实际线路能力标注，不为不支持的路径提供选项。

`POST /api/v1/admin/accounts/batch-test` 接受 account_ids 和可空 model_id，以 SSE 返回 batch_start／account_started／account_result／batch_complete，包含首字延迟、实际模型和原始错误。关闭窗口或页面即停；并发 10、同一上游主机 3、单号 90 秒、保活 15 秒。空 model_id 使用平台默认，账号不支持时取首个文本对话型号；单号弹窗共用解析规则。

结果保留在页面及本地快照，重试只重新提交失败账号。成功后用官方 RecoverAccountState 清除可恢复错误／限流，不改变手动停用或调度开关。关闭测试不刷新列表，保留位置，既有自动刷新延后 15 秒继续。
