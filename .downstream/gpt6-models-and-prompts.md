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

账号绑定存于 `accounts.extra.system_prompt`（`inherit`、`off`、`custom` + `prompt_id`，缺省为继承）。账号编辑、行菜单和批量栏（≤1000 个）通过 `PUT /api/v1/admin/system-prompts/bindings` 写入，普通账号编辑保留该键。继承的账号使用站点默认；全局开关关闭时一律不注入。

绑定只属于请求有注入点的平台，即 `service.SystemPromptPlatforms` 的十个平台（下表四种最终协议）。TypeSafe 只走 System One，请求没有 system / instructions 字段，转发时原样发送，所以它的账号不注入、不计入使用统计、不持有绑定：

- 账号编辑和行菜单不提供该入口；批量栏在选中的账号都已加载且都不可注入时也不提供。选中的是一组 ID，可以包含其他页、全部结果或导入结果里未加载的账号，这类选择保留入口，批量弹窗提示 TypeSafe 账号会被跳过。
- 绑定接口跳过这类账号并返回实际写入的账号数。
- 创建账号（数据导入、复制账号同此路径）时丢弃这类账号带入的 `extra.system_prompt`；批量编辑和 extra 合并接口在任何平台都不写该键，已有账号的绑定只由绑定接口写入。
- 迁移 `268_purge_system_prompt_bindings_without_insertion_point.sql` 一次性删除它们已存的绑定。

提示词库里 259 随旧默认规则一起带入的“先抓取远程规则文件”块已失效：它指向 `/skills/security-research/current/`，这条公开路由随远程 Skill 注册表在 #206 删除，现在落到前端回退页并返回 HTML，模型读不到 Markdown 原文。迁移 `269_strip_removed_remote_rules_bootstrap_from_system_prompts.sql` 只按形状删除该块（含该路径的代码围栏段、其后仍提到 `REMOTE_ROOT` 的段落，以及紧邻其前的简短抓取说明）；路径出现在其他位置，或去掉该块后正文为空的提示词原样保留并输出通知。其余段落、库顺序、全局开关、默认提示词和其他设置不动，没有该路径的配置不写入，可重复执行。

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

## 失效推理密文恢复

侧栏“扩展功能 → 推理恢复”（`/admin/reasoning-recovery`）：一个全局开关和保存按钮，默认开启，没有账号级选项；页面打开时读取一次，读取失败时显示错误、不显示开关。存为 `settings.reasoning_recovery_config` 一行 JSON（`{"enabled": <bool>}`），没有该行即开启；`GET/PUT /api/v1/admin/reasoning-recovery` 以标准响应封装读写 `{"enabled": <bool>}`，管理员认证、无二次验证，保存记入操作审计，请求体不是严格的 `{"enabled": <bool>}` 时返回 400 且不保存。请求路径只读内存中的布尔值（保存即替换，60 秒后台刷新以同步多实例），不查询数据库，每次转发开始时读取一次。启动时读取失败或存储值不是严格的 `{"enabled": <bool>}` 则停止启动；运行中刷新失败保留当前值。

开启时，OpenAI 平台的 API Key、OAuth 和 Setup Token 账号（含自动透传）在原生 Responses（含 compact）与 Chat Completions 转 Responses 的 HTTP 请求上生效：上游以结构化错误码 `invalid_encrypted_content` 或 `thinking_signature_invalid` 拒绝、且尚无语义输出时，去掉推理项的 `encrypted_content`，在同一账号重发一次：上游用 `input[N]` 指明条目时只去掉该项，否则去掉请求里全部推理项的密文。上游未用 `input[N]` 指明条目、而请求里还有其他密文载体（压缩项、多智能体 `agent_message` 的正文段）时，只有上游错误消息按条目 id 点名的是带密文的推理项才恢复，否则不恢复；这条点名规则只在推理项 id 随请求发出的路径上成立（API Key 账号与自动透传），OAuth / Setup Token 的非透传请求发出前会去掉推理项 id，遇到其他密文载体仍不恢复。其他载体的密文任何情况下都不改动。被去掉的密文按来源在 Redis 记忆 24 小时，期间同源请求发送前即去掉；记忆按每批 64 条读写，读与写共用每个请求 100 毫秒的 I/O 预算。本账号修不好时换号：在 `POST /v1/responses`（含 compact 与自动透传）上，上游报告请求里的密文无法解密或校验（结构化错误码、同类错误消息，或 `could not be decrypted` 的措辞），而本账号无法修复（不能恢复，或去掉推理密文后的重发、发送前已去掉密文的请求仍得到这类拒绝），且客户端尚未收到本次尝试的任何输出时，请求原样交给分组里的下一个账号——密文只能由签发它的上游组织解开，别的账号可能直接接受。每个请求最多因此换号 3 次（同时计入正常换号预算），下一个账号各自按上述规则恢复一次；被换下的账号不计健康惩罚，也不做同账号重试。换号上限或可选账号用尽时，客户端收到第一个报告密文问题的账号的那个错误；途中某个账号给出自己的终态（例如读得懂密文但请求本身无效、恢复重发遇到 5xx）或网关侧容量限制时，返回该结果。没有账号给出回答时，被这次换号挪到后续账号上的粘性绑定回到第一个报告密文问题的账号，调度器保留在别处的绑定不动；有账号接受或已开始输出时，会话留在该账号。普通的请求校验错误、带 `previous_response_id` 或 `conversation` 的请求、上游状态为 401/402/403/407/429 时不换号；先试过透传账号再换到非透传账号时，沿用既有规则整块去掉加密推理项。经原生 Responses 路径转发的其他平台账号同样适用；Chat Completions 转 Responses 不换号。Ops 续接诊断用 `recovery.account_mismatch` 标出这样结束的尝试。Messages 桥接与 WebSocket 不恢复。关闭时不重发也不预先去除，签名拒绝仍作为请求级终态返回，不切换账号。

迁移 `267_purge_account_reasoning_options.sql` 一次性删除各账号 `extra` 中原有的两个账号级推理选项键（键名见该文件）；此后没有代码读写它们，已存的账号级取值不带入全局开关。Chat Completions 转 Responses 的请求只由客户端消息转换而来，网关不保存响应中的推理项。

## 账号连接测试与批量测试

单账号、批量和定时测试共用同一测试路径，错误按上游 v0.2.8 原样显示：`API returned <状态码>: <原始响应>`、`Request failed: <错误>` 及流内 `error`/`response.failed` 的上游 message；服务日志 `Account test error:` 与界面文本相同。成功仍要求有效协议终态和可见文本，失败终态附上游原始内容，例如 `stream ended before terminal (last event: …)`、`completed without visible text (finish_reason=…)`、`finish_reason=…`。

批量测试移植上游 PR #6522：`POST /api/v1/admin/accounts/batch-test`（`account_ids`，`model_id` 可空）以 SSE 逐账号返回 `batch_start`/`account_started`/`account_result`/`batch_complete`，含首字延迟、上游实际模型和原始错误；关闭窗口或页面即停止。下游只增加：同时最多 10 个、同一上游主机（base_url 主机，无则平台+类型）最多 3 个、每账号 90 秒上限、15 秒 SSE 保活；`model_id` 为空时由服务端逐账号选模型——账号支持平台默认测试模型时用它，否则取模型列表或映射中第一个文本对话模型（排除图像、音频、嵌入、实时、语音和 `codex-auto-review`），单账号测试弹窗的默认模型用同一规则。结果只保存在页面和浏览器本地快照；重试即重新提交失败账号。

测试成功后走上游 `RecoverAccountState`：清除 error 状态及可恢复的限流和临时不可调度，不改手动停用和调度开关。

## 界面与验证边界

公共插件 context 的同值刷新不再重建 iframe。主题按 URL/变量差异更新，新 CSS 及字体准备完成再替换旧外观；加载失败保留最后成功版本。内容测量按帧合并，使用自然尺寸而非随滚动变化的 viewport 坐标。

离线真实 Chromium 验证了左侧导航、分类栏和右侧账号详情：两次正常 15 秒刷新、未保存输入与滚动保留、主题失败恢复、真实字体加载、菜单伸缩及跨 lg 布局。跨 lg 后表格切换为文档流时，其原容器滚动值会归零，这是原有响应式行为，不属于本轮刷新验收结果。

本地测试只覆盖实际修改风险，正常 PR 必需检查继续执行；同代码的成功结果复用。提示词重构使用离线编排、模拟上游与隔离 PostgreSQL 验证，不发真实模型请求。生产镜像及部署指针由工作区接手手册和交付证据维护，离线或部署健康结果不代表模型质量。
