# GPT-6 模型、Codex 运行与提示词

官方基线见 [upstream-base](upstream-base)。内置提示词能力在宿主内原生运行，随宿主交付；第三方插件框架独立保留。GPT-6 Sol/Luna 的定价与识别沿用官方实现，指令模板、实时容量和日期快照约定见下文。

## 模型同步与容量

账号模型管理的“同步最新支持模型”补齐内置列表；“同步上游支持的模型”才请求账号目录并保存快照。两者保留人工模型和映射。

同步返回的 `model_list_source` 单独标明型号名单来自实时上游还是配置回退，与能力补充来源分开；只有实时目录可用来确认账号当时公布了哪些型号。Anthropic（`limit=1000`、`after_id`/`has_more`）与 Gemini（`pageSize=1000`、`nextPageToken`）目录按页取全。

容量对所有主机使用同一优先级：自定义覆盖 > 适用官方 API 规格 > 来源身份匹配的本账号上游声明 > models.dev 参考 > 未知。官方记录一旦胜出，缺失的独立输入/输出字段保持未知，不从中转或 Codex 订阅观测补齐。声明与 models.dev 参考存放在账号 `extra.upstream_model_metadata` 的模型条目中（`context_window`、`max_context_window`、`max_input_tokens`、`max_output_tokens`、`source: upstream|registry`、`observed_at`），旧的 `upstream_model_context_capacities` 已由迁移 258 并入；快照另存规范化 `source_identity`，缺失或不匹配的旧观测只供管理员诊断。有效窗口使用 `max_context_window`，否则 `context_window`，再否则独立输入限制。分组对外容量取能服务该模型的全部 `active` 账号（含 OAuth）的最小值；未知容量不写入 `/v1/models`，Codex 清单保留真实上游字段，无证据的本地生成条目不伪造容量；图像、视频和音频模型不带文本容量。

`active` 的 API Key 账号每 24 小时分批重新同步（失败退避），创建或修改凭据后立即同步；OAuth 账号不轮询，Codex 清单经过网关时记录其声明。手动同步保留。

| 来源 | GPT-6 Sol / Luna 上下文 | 输出与推理 |
| --- | --- | --- |
| 官方 API 文档 | 1,050,000（有适用条目时作为有效容量） | 最大输出 128,000；none、low、medium、high、xhigh、max |
| Codex 订阅或账号上游声明 | 仅在官方 API 规格缺失时作为完整来源 | 默认与最大值按来源原样保留；不覆盖已适用的官方 API 记录 |
| models.dev | 仅在官方 API 与来源绑定的上游声明均缺失时使用 | 缺失字段不从其他低优先级来源拼接 |

GPT 参考目录按官方 API 规格维护：GPT-5.5/GPT-5.4/GPT-5.6/GPT-6 为 1,050,000，GPT-5.4-mini/GPT-5.2/Daybreak Red 为 400,000，最大输出按官方条目记录。只收录官方文档列出的日期快照别名；Qwen 参考只适用于百炼（dashscope）主机。

API Key 使用完整 Responses。OAuth 目录保留实际 Lite 与上下文字段。Ultra 不作为原生 API reasoning effort 发送。Cindy 库存不因 OpenAI 发布新模型而扩大。

两款型号有独立价卡及严格大于 272,000 输入的长请求倍率，保留人工定价。未知 GPT-6 名称不套 Astra 身份或价格。固定来源与指令摘要见 [官方提取记录](../backend/internal/pkg/openai/gpt6_codex_reference.json)。

## Codex 流式响应

ChatGPT 边缘自 2026-09-23 起不再为 `/backend-api/codex/responses` 的流式响应发送 `Content-Type`。宿主对声明接受 SSE 的 200 响应补回 `text/event-stream`。

## 系统提示词

侧栏“扩展功能 → 系统提示词”：一个全局开关、一个站点默认提示词和提示词库（名称、正文 ≤64 KiB、位置 prepend/append、角色 auto/system/developer，最多 50 条）。整份配置存为 `settings.system_prompts` 一行 JSON；`GET/PUT /api/v1/admin/system-prompts` 读写整份配置并返回账号使用统计，删除仍被自定义账号使用的提示词返回 409。

账号绑定存于 `accounts.extra.system_prompt`（`inherit`、`off`、`custom` + `prompt_id`，缺省为继承）。账号编辑、行菜单和批量栏（≤1000 个）通过 `PUT /api/v1/admin/system-prompts/bindings` 写入，普通账号编辑保留该键。继承的账号在所有平台使用站点默认；全局开关关闭时一律不注入。

请求路径只读内存快照（保存即替换，60 秒后台刷新以同步多实例）和调度账号自带的绑定，不查询数据库；配置不可用时不注入并告警，不向客户端返回错误。每个最终协议只注入一次：

| 最终协议 | 投递 |
| --- | --- |
| Responses | auto/system 追加到 `instructions` 前/后（空行分隔）；developer 作为 developer 输入项放最前，或放在开头 system/developer 之后。Codex Responses Lite 与 compact 请求不注入。 |
| Chat Completions | 在开头或开头 system/developer 之后插入一条消息；上游只接受 system 时 developer 按 system 发送。 |
| Anthropic Messages | 顶层 system 前/后（developer 按 system）。Claude OAuth 伪装前注入，随客户端 system 一起迁入 `[System Instructions]`。 |
| Gemini / Antigravity | `systemInstruction` parts 前/后。 |

count_tokens / countTokens 不注入。`prompt_cache_key` 不改写。响应中回显的 `instructions`（JSON、SSE、WS）还原为客户端原值（未传则为 null）。

Claude OAuth 伪装系统块只由上游设置决定：设置 → 网关的 `enable_claude_oauth_system_prompt_injection`、`claude_oauth_system_prompt` 与 `claude_oauth_system_prompt_blocks` 块编辑器。

迁移 `259_system_prompt_library.sql` 把旧 v2 规则里的纯文本正文导入提示词库（开关关闭、无默认，请求行为不变），启用的旧 Claude 规则只在与设置不同时写回设置，`prompt_skills` 的 off 转为新键，并删除 12 张旧提示词/Skill 表及其保护函数。旧模板、版本、历史、Skill 注册表与 `/skills/security-research/current/` 均已删除；回滚到旧镜像需先恢复这些表的备份。

## Codex 运行设置

侧栏“Codex 运行设置”（`/admin/codex-runtime`）有一个开关“压缩 Codex Responses 请求体”：开启时 OAuth 账号发往 ChatGPT Codex 后端的流式 `/responses` 请求体以 zstd 压缩发送，compact、models 等其他请求保持明文。保存沿用 TOTP 二次验证，对应 `GET/PUT /api/v1/admin/settings/codex-runtime` 的 `{"request_zstd": <bool>}`；接口契约与未保存时的默认值见 [native domains](native-domains.md)。

## 账号连接测试与批量测试

单账号、批量和定时测试共用同一测试路径，错误按上游 v0.2.8 原样显示：`API returned <状态码>: <原始响应>`、`Request failed: <错误>` 及流内 `error`/`response.failed` 的上游 message；服务日志 `Account test error:` 与界面文本相同。成功仍要求有效协议终态和可见文本，失败终态附上游原始内容，例如 `stream ended before terminal (last event: …)`、`completed without visible text (finish_reason=…)`、`finish_reason=…`。

批量测试移植上游 PR #6522：`POST /api/v1/admin/accounts/batch-test`（`account_ids`，`model_id` 可空）以 SSE 逐账号返回 `batch_start`/`account_started`/`account_result`/`batch_complete`，含首字延迟、上游实际模型和原始错误；关闭窗口或页面即停止。下游只增加：同时最多 10 个、同一上游主机（base_url 主机，无则平台+类型）最多 3 个、每账号 90 秒上限、15 秒 SSE 保活；`model_id` 为空时由服务端逐账号选模型——账号支持平台默认测试模型时用它，否则取模型列表或映射中第一个文本对话模型（排除图像、音频、嵌入、实时、语音和 `codex-auto-review`），单账号测试弹窗的默认模型用同一规则。结果只保存在页面和浏览器本地快照；重试即重新提交失败账号。

测试成功后走上游 `RecoverAccountState`：清除 error 状态及可恢复的限流和临时不可调度，不改手动停用和调度开关。

## 界面与验证边界

公共插件 context 的同值刷新不再重建 iframe。主题按 URL/变量差异更新，新 CSS 及字体准备完成再替换旧外观；加载失败保留最后成功版本。内容测量按帧合并，使用自然尺寸而非随滚动变化的 viewport 坐标。

离线真实 Chromium 验证了左侧导航、分类栏和右侧账号详情：两次正常 15 秒刷新、未保存输入与滚动保留、主题失败恢复、真实字体加载、菜单伸缩及跨 lg 布局。跨 lg 后表格切换为文档流时，其原容器滚动值会归零，这是原有响应式行为，不属于本轮刷新验收结果。

本地测试只覆盖实际修改风险，正常 PR 必需检查继续执行；同代码的成功结果复用。提示词重构使用离线编排、模拟上游与隔离 PostgreSQL 验证，不发真实模型请求。生产镜像及部署指针由工作区接手手册和交付证据维护，离线或部署健康结果不代表模型质量。
