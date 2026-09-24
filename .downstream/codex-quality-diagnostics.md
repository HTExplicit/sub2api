# Codex 固定账号质量诊断

此入口用于管理员在真实 `/v1/responses` 链路上运行有界对照。路由材料、完整响应、上游模型声明和回答质量是四种不同证据；采集成功不代表回答质量恢复。

## 接口与范围

所有管理操作位于 `/api/v1/admin/accounts/:id/codex-quality-runs`，要求真实管理员身份。运行绑定该管理员已有的业务 Key、其分组、目标 Pro OAuth 账号主体、代理、`gpt-6-astra`、显式 `high` 和题面 SHA-256。

| 操作 | 请求 | 行为 |
| --- | --- | --- |
| `POST` 基础路径 | `run_id`、`api_key_id`、`prompt_sha256`、`max_sends`、`ttl_seconds` | 创建或恢复同一运行，换发短时 grant；最多 60 次发送、最长 7200 秒 |
| `GET /:run_id` | 无正文 | 读取预算、原始上游观测和私有路由资格 |
| `POST /:run_id/renew-route` | `operation_id` | 显式采集与业务出口复验，均计入同一预算 |
| `POST /:run_id/close` | 空对象 | 关闭 grant，释放该运行自身连接，擦除私有 Cookie 值，保留账本 |

`run_id`、`operation_id` 与 trial 均使用规范化 UUID。同一 run 恢复时不能改变绑定范围或增加预算，换发 grant 不恢复已花费次数；closed 运行不能重开。

业务仍用原 Key 作为 `Authorization: Bearer`，额外携带 `X-Sub2API-Quality-Grant` 和 `X-Sub2API-Quality-Trial`。正文的 `input` 必须为绑定的原题字符串，`model=gpt-6-astra`、`reasoning.effort=high`、`stream=true`、`store=false`。客户端会话元数据与 prompt cache key 可保留；不支持把此入口扩展为任意模型或任意账号转发。

grant 与 trial header 在 handler 开始时移入私有执行上下文，不转发给上游。错误、过期或已消费的授权直接失败，不回落到普通选号。

## 执行约束

- 只在私有选号判断中跳过账号手动停调度开关；账号、Key、分组和代理配置不修改。当前 Key 状态、到期、额度、渠道模型限制、账号健康、并发及路由资格仍检查。
- acquire、verify、business 共用持久 CAS 预算，在本地准备与检查结束、上游 I/O 开始前预留。传输结果未知保留为 unknown，不能重放该 trial；预留次数是保守的发送上限，不能宣称每次预留均已得到上游响应。
- 诊断材料使用独立 bundle、clock 与连接，不能覆盖普通资格，不登记自动续获。GET 不抢占或恢复普通 half-open probe。
- 普通健康、调度恢复、proxy circuit 和自动恢复通知与诊断隔离；实际用量账单及额度快照按真实请求记录。
- 记录来源是网关改写前的原始响应。`created_model`、`terminal_model`、HTTP/事件模型头分别保留；模型头冲突保存在 `header_models`。缺失 token 数据为 null，不替换为零。
- `completed` 仅代表成功终态观测，不能替代模型核验或数学判分。当前仍保留修前探针形态，探针未显式给出的 effort 如实记录为空。

## 运维执行器

项目运维目录使用 `scripts/invoke-codex-quality-acceptance.py`。它从既有配置读取管理员凭据，仅在内存保存访问令牌、业务 Key 和 grant。固定账本为 `artifacts/evidence/codex-quality-20260923.json`；跨日、客户端中断、换版本都不能换 run ID 或清空预算。

```powershell
python scripts/invoke-codex-quality-acceptance.py preflight
python scripts/invoke-codex-quality-acceptance.py local-status
python scripts/invoke-codex-quality-acceptance.py status
```

`baseline` 与 `verification` 为真实生成操作，必须传发布者核实的 `--version` 和 `--source-sha`。前者 6 次，后者 12 次；前两次只观察，修后计分序列跨至少 6 分钟与两次实际资格续获。所有采集、复验、失败和对照都受同一 60 次上限约束。生成期间由单一执行者持有本地账本锁，发布者不得同时替换容器。

`review` 只对已有 trial 登记数学结论，不调用模型。通过条件包括完整响应、固定账号与最终 high、最终原始模型匹配、全部已提供模型头一致，以及答案和证明均正确；不能用正则找到“21”便自动通过。题目基准为 9 圆＋12 星，且需证明 20 不足。

`reconcile` 不发送模型请求。只有收到应用明确的发送前拒绝、服务器账本也无该 trial，才允许保留原记录并安排新 trial；网络超时或代理错误不能当作“没发送”。

诊断读取最终请求时兼容 reasoning recovery 清除 `GetBody` 的不可重放请求：有界读取实际编码字节后原样恢复 `Body`，保持 `GetBody=nil`、编码头和长度；编码体与解码体分别最多 2 MiB 和 1 MiB。读取或关闭失败时阻止发送，不预留生成预算，不重新启用透明 POST 重放。

如果诊断本身在第一次业务发送前失败，发布者可在仅修诊断的新版本上线并完成旧进程排空后，显式执行 `restart-baseline --version <version> --source-sha <sha> --reason <reason>`。入口要求旧阶段无业务回执、无未结束操作，完整保留旧阶段及未确认结果，为旧未确认 trial 保守占用预算，再建立新 trial/turn 命名空间。它不发模型请求、不重置服务器账本；迟到的旧回执会阻止后续生成。普通 `baseline` 和 `verification` 不能隐式改变已有阶段的源码版本。

## 发布次序

诊断基础设施先独立发布并采集修前基线。协议与账号身份修复随后发布，在共同宿主基线、同一账号和同一预算下完成修后序列。不能先应用修复再把结果命名为修前基线。

后续协议整合必须保持读取次序：网络 → 原始诊断观察 → 模型声明检查 → 业务响应改写。诊断业务流的读取限界需与普通业务一致，轻量采集探针保持自身预算。
