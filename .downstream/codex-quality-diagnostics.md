# Codex 固定账号质量诊断

此入口用于管理员在真实 `/v1/responses` 链路上，对一个固定的 OpenAI OAuth 账号运行有预算的对照。完整响应、上游模型声明和回答质量是不同证据；服务健康或完整响应不代表回答质量。

诊断发送走该账号的普通转发路径：代理、传输、出站身份、请求头和 zstd 压缩都与业务流量相同，没有路由采集、Cookie、连接租约或 acquire/verify 阶段。诊断授权、预算、原始观测和私有选号仍独立于普通调度。

## 接口与范围

所有管理操作位于 `/api/v1/admin/accounts/:id/codex-quality-runs`，要求真实管理员身份。运行绑定该管理员自己的业务 Key 及其分组、目标账号、账号代理、凭据主体、模型、推理强度和题面 SHA-256。

| 操作 | 请求 | 行为 |
| --- | --- | --- |
| `POST` 基础路径 | `run_id`、`api_key_id`、`prompt_sha256`、`model`、`reasoning_effort`、`max_sends`、`ttl_seconds` | 创建或恢复同一运行并换发短时 grant。`max_sends` 为 0 或缺省时取 60，范围 1–60；`ttl_seconds` 为 0 或缺省时取 7200，范围 60–7200 |
| `GET /:run_id` | 无正文 | 任一管理员可读绑定、预算和每次发送的原始上游观测；不返回 grant |
| `POST /:run_id/close` | 空对象 | 仅创建者可关闭；吊销 grant，保留账本 |

`POST /:run_id/renew-route` 已删除，请求返回 404。成功响应为 `{"code":0,"message":"success","data":<运行视图>}`；服务端拒绝为 HTTP 409，消息给出具体原因。

`run_id` 与 trial 使用规范化 UUID。`model` 与 `reasoning_effort` 必填，去除首尾空白，模型名最多 256 字节。同一 run 恢复时，管理员、Key、账号、分组、代理、凭据主体、模型、强度、题面和 `max_sends` 都不能改变；换发 grant 使旧 grant 失效，已花费次数保留，预算不增长；closed 运行不能重开。

创建时校验推理强度：须属于账号测试按模型解析出的档位（#224）；解析不到档位时须为 `none`、`minimal`、`low`、`medium`、`high`、`xhigh` 之一，账号模型元数据声明该模型无推理时拒绝。Key 分组（OpenAI 或组合分组）的推理强度上限、映射或超限拒绝会改写或拒绝该强度时也拒绝创建：普通转发在准入后套用该策略，最终强度与绑定值不同的发送会被拒绝。创建、准入、私有选号和发送前预留都重新检查账号：

- 手动停调度（`schedulable=false`）、非影子的 OpenAI OAuth 账号，`plan_type` 为 `pro` 或 `chatgpt_pro`；仍在 Key 的分组中，代理和凭据主体与创建时一致。
- 模型按普通转发规则可用：账号支持该模型，渠道定价和上游渠道不限制它，渠道映射与账号映射都不改名；诊断只发送绑定模型本身。
- 没有账号运行时阻断、模型临时阻断、代理流熔断、运行时熔断（Redis）或调度阈值暂停，满足分组隐私要求；Key 有效、未过期、额度未用尽。私有选号还须取得账号并发槽。

## 业务发送

业务仍用绑定的 Key 作为 `Authorization: Bearer`，并各携带一个 `X-Sub2API-Quality-Grant` 和 `X-Sub2API-Quality-Trial`（每次发送一个新的规范 UUID，同一 trial 不能再次发送）。只接受 HTTP `POST /v1/responses`；正文为最多 1 MiB、无重复键的 JSON，顶层字段限于 `model`、`input`、`reasoning`、`stream`、`store`、`prompt_cache_key`、`client_metadata`。`input` 必须是 SHA-256 与绑定一致的原题字符串，`model` 与 `reasoning.effort` 等于绑定值，`stream=true`、`store=false`。

grant 与 trial header 在 handler 开始时移入私有执行上下文，不转发给上游。错误、过期、已换发或已消费的授权直接失败，不回落到普通选号：准入失败返回 409 `codex_quality_unavailable`，原因只写入服务日志和 Ops 记录；进入转发后的失败（含发送前预留被拒绝）返回 502 `codex_quality_failed`。

- 私有选号只使用运行绑定的账号，仅在该私有判断中跳过手动停调度开关；账号、Key、分组和代理配置不修改。
- 最终出站请求（含 zstd）生成后再核对运行、Key 和账号，并要求最终正文仍携带绑定的模型与强度，否则拒绝发送且不花费预算；随后以持久 CAS 预留一次发送，再开始上游 I/O。传输结果未知的发送保留为 `unknown` 并计入预算，不能重放该 trial；预留次数是保守的发送上限，不能宣称每次预留都得到了上游响应。
- 读取次序固定为网络 → 原始诊断观察 → 模型声明守卫 → 业务响应改写。`created_model`、`terminal_model`、HTTP/事件模型头分别保留，模型头名称不区分大小写，冲突记入 `header_models`。被守卫拦下的已读原始帧仍属于同一次发送，未读取的正文模型保持未知；缺失的 token 数据为 null，不替换为零。
- 首输出前的模型错配不写入下游，已经输出的请求不会自动重放。SSE 单事件上限复用 `Gateway.MaxLineSize`，JSON 复用 `Gateway.UpstreamResponseReadMaxBytes`，不使用无界响应缓冲。
- 读取最终请求时兼容 reasoning recovery 清除 `GetBody` 的不可重放请求：有界读取实际编码字节后原样恢复 `Body`，保持 `GetBody=nil`、编码头和长度；编码体与解码体分别最多 2 MiB 和 1 MiB。读取或关闭失败时阻止发送，不预留预算，不重新启用透明 POST 重放。
- 普通健康、调度恢复、proxy circuit 和自动恢复通知与诊断隔离；实际用量账单及额度快照按真实请求记录。诊断发送与普通流量一样记入账号“Codex 出站指纹”的最近实际出站。
- `completed` 只代表原始上游成功终态观测；同一条记录仍可能因早期模型、模型头冲突或客户端取消而不能作为通过样本。它不能替代模型核验或数学判分。

普通 Codex 的 Responses-Lite 请求保留 `input` 中已有基础提示和工具，不再额外填入顶层默认 `instructions`。OAuth STATE 只在同一账号主体、会话和显式逻辑 turn 内保留首个已提交值；Messages 只有完整响应成功写入下游后才保存。缺少 turn 证据不复用旧值，API Key 会话续接契约保持。

## 账本

账本存于 `sub2api_plugin_state`：plugin key `codexrip.codex-runtime`，namespace `codex-routing-private`，key `quality-run.<run_id>`。新记录显式保存 `model`、`reasoning_effort` 和凭据主体 `owner_identity`；账本不保存 grant、Key、题面、Cookie 或 STATE 值。

路由资格诊断时期写入的旧记录（无 `model`）按 `gpt-6-astra`/`high` 显示且只读：GET 和 close 仍可用，同一 `run_id` 不能再创建、恢复或发送（409，需换用新 run id）。旧记录的 `scope`、`qualification`、`route_generation`、`route_runtime_generation` 及旧 attempt 字段在重写（如 close）时原样保留；视图只对旧记录只读显示 `route_generation`、`route_expires_at` 和 `connection_fingerprint`，旧 attempt 可能带 `acquire`/`verify` 阶段。关闭旧记录不再清理其遗留的私有 Cookie 状态。

## 运维执行器

项目运维目录的 `scripts/invoke-codex-quality-acceptance.py` 驱动上述接口，用法见该目录 `scripts/README.md`。新运行必须使用新账本并显式给出 `--model` 与 `--reasoning-effort`，二者随账本绑定；`artifacts/evidence/codex-quality-20260923.json` 是路由资格诊断时期的账本，只读，仍可 `status`、`local-status`、`review`。

`baseline`（6 次）与 `verification`（12 次）为真实生成操作，必须传发布者核实的 `--version` 和 `--source-sha`；前两次只观察，修后计分序列从首个计分完成响应起至少跨 6 分钟。生成期间由单一执行者持有本地账本锁，发布者不得同时替换容器。

`review` 只对已有 trial 登记数学结论，不调用模型。通过条件包括完整响应、固定账号、绑定的模型与强度、原始模型和全部已提供模型头一致，以及答案和证明均正确；不能用正则找到“21”便自动通过。题目基准为 9 圆＋12 星，且需证明 20 不足。

`reconcile` 不发送模型请求。只有收到应用明确的发送前拒绝、服务器账本也无该 trial，才允许保留原记录并安排新 trial；网络超时或代理错误不能当作“没发送”。诊断在第一次业务发送前失败时，可在仅修诊断的新版本上线并排空旧进程后执行 `restart-baseline --version <version> --source-sha <sha> --reason <reason>`；它不发模型请求、不重置服务器账本。

修前基线与修后序列须在同一账号、模型和强度下采集；不能先应用修复再把结果命名为修前基线。
