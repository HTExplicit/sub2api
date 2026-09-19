# v0.2.7 实施状态

本分支尚未发布。用户要求所有明确问题、下游插件化和全项目审查完成后统一交付。

## 固定基线

- 生产和 origin/main：`65a82d9a8f079f389f08e092d318a663123095e9`。
- 官方：`v0.2.7` / `aea725f2ea644d5592d0bbb1d63b607efa7e200a`。
- 隔离工作目录：`work/sub2api-v027-plugins`，分支 `feat/v027-plugin-architecture`。
- 官方合并提交：`5a4b26a1d`。七处文本冲突已解决；保留账号重试/释放、空 context 保护、对话框栈及路由测试依赖，同时引入官方 Seedance 路由与移动端模型广场入口。

## 用户已经确定的边界

- 开发等价 292 插件，同时尽量迁移全部自建能力；采用通用宿主扩展层和功能域插件，通用正确性修复、认证、事务、计费提交仍在核心。
- 插件域：Codex 运行扩展、Cindy Provider、账号工具、提示词/技能、模型目录/策略、图像工具、界面/观测。插件独立模块，仅引用公开 SDK。
- 保留日常操作入口，启用时才显示插件贡献的功能；已启用但故障时保留入口并显示不可用原因。后端同步检查。没有旧实现的隐式回退。
- 同仓构建签名 `.s2plugin`，统一版本镜像携带锁定插件；迁移继承既有配置、任务、租约和启停状态。
- 292 保持 OAuth/Setup Token 非影子账号范围、停用/错误账号支持、手动首次、严格292、3600秒TTL、到期前60秒续期/失败后到期后60秒重试一次、停止状态不自动恢复。
- 费用按账号 A 口径，配对同一窗口的费用/使用率增量，显示完整窗口总额和剩余额度价值。无有效配对显示待估算。5h/7d/30d使用真实周期。
- 推理强度覆盖单个和逐项批量连接测试，复用模型能力，不改纯额度查询和自动采样，不默认真实模型验收。
- 代理编辑区：标签在上，下拉框和按钮底部对齐，窄屏换行。
- 审查整个项目与运维。保留资源、网络、手动路由和映射，CPA停机，图像关闭，公开管理退役。
- 按既有固定digest直发规则统一交付，不恢复取消的发布保护；最小定向测试复用同树结果。

## 当前实现与未完成项

- 合并后的定向后端命令已完成：`go test ./internal/handler ./internal/service -run '^(TestGrokMedia.*Slot|TestSeedance.*|TestOpenAIGateway.*DeepSeek.*)$' -count=1`。handler 匹配0例（仅完成编译），service匹配用例通过。
- 正在实现统一额度窗口：新增 `account_quota_windows.go`，`Account.QuotaState` 接入 `IsSchedulable` 和两种管理DTO；用量API增加 `quota_windows` 并按实际时长取费用。前端改读窗口列表、移除累计费用除累计比例的旧公式、添加额度阻止状态。
- 额度增量估值第一版已加入 `quota_estimate.go`、`account_quota_estimate.go`、迁移247。仅复用现有 `/wham/usage` 查询；查询前后须账号并发为0、计费池已完成且费用记录指纹与队列计数稳定。持久化同一账号身份/倍率、周期的基线和最近观测；UI按配对增量显示。`/wham`绝对reset_at已保留。仍需真实PostgreSQL集成测试、collector并发/迟到用例，以及审查跨实例/异步提交间隙保证，不能只凭公式单元测试宣布整个估值完成。
- 当前额度改动还需：规范响应头/错误体/查询写入和恢复源、短/长期策略复用、处理原始字段重置时刻、配对估值、前端回归测试更新、倒计时刷新与数据层定向测试。
- 新额度窗口单元测试已通过；类型命名冲突已修正为 `UpstreamQuotaState`。管理DTO精简投影映射已修正并编译通过；前端新增字段类型漏接已修正，`pnpm typecheck` 已通过。
- 代理布局已在现组件修改，最终需随 Codex 插件UI迁移并验收。
- 新增公开 `backend/pkg/extensionapi/v1`：版本化领域调用、宿主操作、状态CAS、租约、调度请求、界面贡献；基于官方进程握手并增补独立gRPC服务。SDK的RPC/取消测试通过。插件manifest/schema增加能力、依赖、UI槽声明，manager已开始多插件重构；声明启用来自持久bindings而非健康状态。新 `246_plugin_extension_state.sql` 和对应PostgreSQL CAS/带generation租约实现尚未运行真实数据库验收。
- 单个/逐项批量连接测试已接入 `reasoning_effort`，共享模型元数据解析与校验，Responses/Chat/Grok出站序列化、SSE和任务结果字段已接入；前端选择器已加。需要实际HTTP载荷测试、批量幂等/重试测试、补齐其他已支持平台的模型元数据，以及最终账号工具插件迁移。
- 各域真正业务迁移、构建签名锁定、配置/数据迁移、费用配对持久化、全项目覆盖清单与运维修正均未完成。不存在可发布的完整结果，禁止部署。
- `plugins/codex-runtime` 已创建独立Go模块及可编译入口，包含票据状态机、准入/注入、手动采集/续期/停止操作的第一版；模块测试通过。它尚未接入宿主业务链路，也没有签名包、锁清单或正式UI。代理证书信任/诊断、持久自动任务调度、存量状态迁移、真实离线端到端采集测试仍需补齐，现有宿主292逻辑尚未切除。
- SDK Host新增受manifest范围约束的账号目录及凭据准备接口，宿主通过现有OAuth凭据准备上下文支持Setup Token与停用账号；该接口最新修改待编译复验。
- 后端Wire已从源provider重新生成；初次go generate缺工具依赖sum，改用 `go run -mod=mod github.com/google/wire/cmd/wire ./cmd/server` 成功，新增锁定工具间接依赖属于此次生成所需。
- `plugins/codex-runtime` 已 `go mod tidy`，独立模块基础测试通过；最近修正自动续期不能因旧票仍有效而skip、operation ID去重、过期运行中阶段不自动重放、ticket.inject能力。新的采集网络逻辑尚未用假上游完整验证。
- 插件通用后台操作已接入已有 AccountJobRuntime（`extension_operation`、PluginJobExecutor），复用持久任务、取消、失败重试与元数据约束；新增管理贡献/操作/任务端点，提交前和执行时都核验已启用能力/声明动作/账号范围。宿主状态表增加 `next_at` 及due查询，Codex模块可从持久ready/retry阶段续期，5个共享槽；尚未切换旧292执行器。
- 新增真实压缩出站测试证明所选ultra经过模型映射/zstd后仍在最终请求内，Compact无效选择在发请求前拒绝；新增采样器用例验证查询期间的迟到费用和在途请求会丢弃样本，均已通过。
- 最近一次后端定向命令覆盖 service/admin/repository/server 编译及新业务用例通过；repository/server当次匹配0例，仅完成编译，真实数据库验证未执行。最新前端typecheck通过。SDK和独立Codex模块仍需多进程/假上游集成测试后才能切换。
- 前端五个定向文件66例通过（AccountUsageCell/AccountStatusIndicator/CodexTicketProxyEditor/单个测试/批量测试）。包含旧响应适配，但旧响应不再计算任何金额。新的强度交互及最新状态分支还需要对应新用例。
- 不得把插件空壳、仅加开关、仍调用原业务实现当作迁移完成；每域转换后须删除旧业务执行路径。

## 后续实现注意

- 官方 `PluginManager` 实际仅支持一个启用的OAuth transport，不能仅新增manifest就声称多域插件可用。需要按能力注册/调度，并分离期望启用状态与运行健康。
- 官方 HostService 只有KV/账号目录/身份解析，没有CAS、租约、业务数据访问、调度准入、后台任务、管理操作与UI扩展槽。新协议使用独立命名空间，兼容官方v1。
- 现有 `account_usage_service` 的 `/usage` 自动探针会发送模型请求；线上调查只读 `/admin/accounts/:id`，不要为了验收随手请求 `/usage`。
- 线上已读事实：16376 `codex_7d_used_percent=100`、明确未来reset，但 `rate_limit_reset_at=null`；16377 primary周期43200分钟。未改线上。
- `usage_logs.CreatedAt` 当前在记录用量时设置为结束时间，Quota响应头在更早阶段采样。增量费用配对必须处理这个时序与并发/迟到提交，不能简单把当前累计费用绑到较早快照。
- 新429分类器已改为真实窗口并删除猜测5h/7d期限；未知恢复时间的硬额度错误记录 `openai_quota_exhausted`，额度状态可在新鲜恢复证据后解除。该处最新改动仍需复验；有些原测试标有 `//go:build unit`，运行时须加 `-tags unit`。当前正在运行相关unit命令，记录结果后继续。
- 调度缓存精简投影已补齐原始周期/重置字段和硬额度状态，避免30天周期在投影后重新被当作7天。
- 新前端窗口结构使旧 `/usage` fixture及“12/40%=30”的错误估值断言过时，须改成验证服务端配对估值，不得删除仍需要的状态断言。
- 已发现旧 `deploy-session-converter.ps1`、`deploy-2fauth.ps1` 等私钥路径/任意SSH封装，与现行入口规则不一致；仅读过，未修改或运行。
- 主 checkout 有用户遗留未跟踪 `.worktrees/`、`WS`、`fc_`，不得清理。根目录不是Git仓库。
