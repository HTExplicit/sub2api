# GPT-6 模型、Codex 路由与提示词

官方基线见 [upstream-base](upstream-base)。内置提示词能力在宿主内原生运行，随宿主交付；第三方插件框架独立保留。GPT-6 Sol/Luna 的定价与识别沿用官方实现，Codex 专属路由、指令模板、实时容量和日期快照约定见下文。

## 模型同步与容量

账号模型管理的“同步最新支持模型”补齐内置列表；“同步上游支持的模型”才请求账号目录并保存快照。两者保留人工模型和映射。

同步返回的 `model_list_source` 单独标明型号名单来自实时上游还是配置回退，与能力补充来源分开；只有实时目录可用来确认账号当时公布了哪些型号。

| 接入方式 | GPT-6 Sol / Luna 上下文 | 输出与推理 |
| --- | --- | --- |
| 官方 API | 1,050,000 | 最大输出 128,000；none、low、medium、high、xhigh、max |
| Codex 打包参考 | 默认 272,000，扩展最大 872,000 | 默认 medium；Sol 支持客户端 Ultra 委派，Luna 最高 Max |
| 账号原始目录 | 按实际声明及既有覆盖优先级 | 实时能力优先于旧快照和默认值 |

API Key 使用完整 Responses。OAuth 目录保留实际 Lite 与上下文字段，回退值标记为“Codex 参考”。Ultra 不作为原生 API reasoning effort 发送。Cindy 库存不因 OpenAI 发布新模型而扩大。

两款型号有独立价卡及严格大于 272,000 输入的长请求倍率，保留人工定价。未知 GPT-6 名称不套 Astra 身份或价格。固定来源与指令摘要见 [官方提取记录](../backend/internal/pkg/openai/gpt6_codex_reference.json)。

## Cookie 路由状态与指纹

账号的 Cookie 候选、模型验证结果和原生 turn-state 各自独立。采集取得完整成功响应和允许的 Cookie 后生成候选；业务出口完整完成且原始响应模型匹配，才能取得该模型的限时资格。292/312 仅是观测长度，不代表模型质量。

- 恢复 Cookie 只使用 `__cflb` 和 `__oailb`。基础设施 Cookie 使用官方允许集合，登录 Cookie 不进入共享传输状态。宿主处理 Cookie；普通插件头修改继续禁止 Cookie。
- 同一 Cookie 值不延长首次采集时间，删除有持久标记；资格绑定主体、profile、路由、Cookie 版本及实际连接。本地上限为 120 秒，更早的上游到期时间优先。
- 每账号与型号使用固定候选和当前槽；后台清除到期 Cookie 原值，保留摘要、删除标记和诊断账本。同值且未缩短期限的响应不会反复写入新状态。清除任务在采集开关关闭时仍执行，整个插件停用期间不承诺后台清除。
- 未知轮换出口通过实际连接租约复用，连接丢失后不能自动在新连接上继续发送旧资格。客户端 WS 在只有 HTTP 资格时使用已有 HTTP bridge，页面明确显示 WS 入站、HTTP 出站；原生 WS 验证只证明该次连接。
- 已登记且有请求需求的账号在到期前 20 秒续获。每周期最多一次采集与一次复验，连续两个失败周期后持久停止；闲置账号不持续探测。`fail_closed` 保留。
- 旧 292 不转换为 Cookie 验证成功，旧停止状态保留。原先默认的 Astra/5.6-Sol 名单只在旧配置版本迁移时追加 6-Sol/6-Luna；自定义名单保留，之后显式修改不自动补回。
- 原生 turn-state 在同一轮固定首次值，下一轮或换账号清除。
- ChatGPT 边缘自 2026-09-23 起不再为 `/backend-api/codex/responses` 的流式响应发送 `Content-Type`。宿主对声明接受 SSE 的 200 响应补回 `text/event-stream`，资格读取对无类型响应按 SSE 帧识别；缺少这一处理时采集、复验和业务观测都只能得到 `routing_incomplete`。

账号详情右栏的“Codex 出站指纹”读取配置和已经记录的出站事实，刷新不会发模型请求。页面展示 profile 来源、UA、版本、身份摘要及一致性、传输、Cookie 名称和模型验证结果。新建账号采用官方 Windows CLI 0.156.0 的应用层参照；已有配置保留，可显式选择新版参照，设备种子和手动 UA 覆盖不被重置。

本机官方 CLI 通过隔离合成认证、本地可信证书和受控 HTTP/WS 端点取得参照。生产 Go TLS 的实际值与参照分开展示；未捕获生产 ClientHello 时不宣称 JA3/JA4 相同。原始 Cookie、STATE、Bearer 和代理密码不进入这些页面或通用诊断。

管理员接口位于 `/api/v1/admin/accounts/:id/codex-fingerprint`，profile 选择使用其 `/profile` 子路径和账号版本 CAS。`/codex-routing/validate` 是独立、不登记自动续期的限额验证操作，固定支持三款 GPT-6，一次 UUID 绑定一个账号和最多七个出站阶段；重复操作不能重新花费相同阶段。

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

## 292 采集代理输入

在“Codex 路由设置”的采集代理输入框粘贴地址。支持完整代理 URI、主机端口、两种顺序的四段认证格式、引用分列、中英文标签以及多条记录；IPv6 使用方括号。协议选择仅用于输入未写协议的情况，明确的 URI 或标签协议不会被覆盖。

例如 `proxy.example:8080:user:p@ss` 按候选解析后选择对应端点；`node.example:8080:user.example:9090` 可能有两种合法解释，需要从脱敏列表中明确选择。多个格式识别出同一端点及认证信息时去重，不同密码不合并。输入中的 `%40` 在 URI 认证字段中解码一次，在原始四段式或标签值中保持字面量；含分隔符或边界空白的列值使用引号。

`POST /api/v1/admin/settings/openai-codex-ticket/proxy-parse` 只解析候选，不存储输入、不连接网络。保存和测试携带原文、缺省协议及可选 `proxy_selection_id`，服务端重新解析并核对选择，最终仅保存一个规范代理地址。修改输入、缺省协议或管理员会话会失效旧选择和测试结果；格式错误、未选择或选择过期不会发起连接。测试和实际采集使用同一选定端点，保持证书信任及采集连接隔离。

## 账号连接测试与批量测试

单账号、批量和定时测试共用同一测试路径，错误按上游 v0.2.8 原样显示：`API returned <状态码>: <原始响应>`、`Request failed: <错误>` 及流内 `error`/`response.failed` 的上游 message；服务日志 `Account test error:` 与界面文本相同。成功仍要求有效协议终态和可见文本，失败终态附上游原始内容，例如 `stream ended before terminal (last event: …)`、`completed without visible text (finish_reason=…)`、`finish_reason=…`。

批量测试移植上游 PR #6522：`POST /api/v1/admin/accounts/batch-test`（`account_ids`，`model_id` 可空）以 SSE 逐账号返回 `batch_start`/`account_started`/`account_result`/`batch_complete`，含首字延迟、上游实际模型和原始错误；关闭窗口或页面即停止。下游只增加：同时最多 10 个、同一上游主机（base_url 主机，无则平台+类型）最多 3 个、每账号 90 秒上限、15 秒 SSE 保活；`model_id` 为空时由服务端逐账号选模型——账号支持平台默认测试模型时用它，否则取模型列表或映射中第一个文本对话模型（排除图像、音频、嵌入、实时、语音和 `codex-auto-review`），单账号测试弹窗的默认模型用同一规则。结果只保存在页面和浏览器本地快照；重试即重新提交失败账号。

测试成功后走上游 `RecoverAccountState`：清除 error 状态及可恢复的限流和临时不可调度，不改手动停用和调度开关。

## 界面与验证边界

公共插件 context 的同值刷新不再重建 iframe。主题按 URL/变量差异更新，新 CSS 及字体准备完成再替换旧外观；加载失败保留最后成功版本。内容测量按帧合并，使用自然尺寸而非随滚动变化的 viewport 坐标。

离线真实 Chromium 验证了左侧导航、分类栏和右侧账号详情：两次正常 15 秒刷新、未保存输入与滚动保留、主题失败恢复、真实字体加载、菜单伸缩及跨 lg 布局。跨 lg 后表格切换为文档流时，其原容器滚动值会归零，这是原有响应式行为，不属于本轮刷新验收结果。

本地测试只覆盖实际修改风险，正常 PR 必需检查继续执行；同代码的成功结果复用。提示词重构使用离线编排、模拟上游与隔离 PostgreSQL 验证，不发真实模型请求。生产镜像及部署指针由工作区接手手册和交付证据维护，离线或部署健康结果不代表模型质量。
