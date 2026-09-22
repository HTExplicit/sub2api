# 下游归属与审查覆盖

当前清单尚未完成审查，不能用文件已分类代替功能迁移完成。生产仍按根项目手册运行；发布完成状态只记录在[实施状态](implementation-v0.2.7.md)。

## 范围

当前清单以官方精确提交 `aea725f2ea644d5592d0bbb1d63b607efa7e200a` 对比工作树，并纳入未跟踪源码。最新数量、候选归属、逐文件角色、内容摘要和待审查状态仅维护在[机器可读清单](coverage-v0.2.7.json)，由 `node .downstream/refresh-coverage.mjs` 更新；只有字节一致的已有审查结论才会保留。

清单分别标明源码、测试/固定用例、内置技能来源文件、文档、生成代码、依赖锁和二进制素材。候选归属只提供审查入口，混合文件仍按实际调用链判定。审查记录须包含决议、归属、原因与证据，并绑定当前字节指纹；删除项绑定被删除的上游 blob，不能因没有文件摘要而永久丢失审查结论。

仅清单 JSON 自身采用派生结构校验，不以其自引用内容计算摘要；其他文档仍须静态审查。`node .downstream/refresh-coverage.mjs --check` 检查清单与当前源码一致，`--check --require-complete` 另要求全部记录有有效结论。总完成状态由记录计算，不能手改布尔值代替审查。

已完成的外部审查可用 `node .downstream/refresh-coverage.mjs --import-review <审查JSON路径>` 导入，可重复指定该选项。输入须声明相同 `upstream_base` 和逐文件 `entries`；每条已审记录的角色、内容摘要、指纹、决议、原因和证据都必须有效。待审记录不导入，重复路径、改变的源码与清单自身声明会使整次导入失败且不写入；`--check` 不允许同时导入。来源证明与业务语义审查分别在原因和证据中表述，不能互相替代。

## 已建立证据的决议

| 证据入口 | 根因或归属 | 实现路径 |
|---|---|---|
| `TestQuotaWindowObservationsKeepIndependentDeadlinesAndClearRemovedSlots`、`TestQuotaSnapshotOrderingAndLegacyWindowClock` | 不同窗口不能共享新的采样起点；旧快照必须在锁内判定时序 | 核心统一窗口表示、窗口独立时钟、数据库单调写入 |
| `TestQuotaRecoveryCannotReactivateOldUnknownErrorOrClearOtherState`、`TestOAuthQuota429UsesRecoverableQuotaSource` | 硬额度与普通 429 需要不同状态来源 | 核心额度读模型，独立恢复；保留管理员状态和普通冷却 |
| `TestQuotaMeasurementUsesShadowWindowAndParentBillingIdentity`、`TestQuotaEstimateRepositoryRecalibratesAfterCostHistoryShrinks` | 费用必须对应同一主体、计费倍率、额度范围和连续费用历史 | 核心配对估值与可归因观测边界 |
| `TestAccountTestReasoningOpenCodeGoSerializesNativeProtocol`、`TestBatchTestReasoningPersistsPerAccountForRetry` | 选项必须写入实际出站协议，批量选择按账号身份持久化 | 核心协议校验和序列化；增强界面的最终插件归属仍待完成 |
| `TestCodexTicketFacadeUsesPluginWithoutLegacyFallback`、`TestRestartRecoversAbandonedFirstAttemptWithoutSendingAnotherRequest` | 292 是领域业务，不能保留两个采集/续期执行器 | `plugins/codex-runtime` 独立执行；宿主只留协议、凭据刷新、任务及一次数据迁移 |
| `TestSettingsCodexTicketConfigurationHasOnlyPluginWritePath` | 全局设置与插件配置不能同时写同一业务开关 | 旧写入口返回迁移提示，插件配置唯一写入 |
| `TestCatalogProcessContractPreservesProductScope`、`TestPluginCatalogCacheHonorsActivationHealthAndConfiguration` | 固定目录与供应商匹配是可独立策略；观测/自定义值及金额提交仍属核心 | `plugins/model-policy` 独立目录，宿主通用能力接口及有界缓存 |
| `TestFirstPartyBundleContainsMatchingSignedDomainPackages` | 每个插件及界面资源必须对应锁定签名包 | 七域签名与进程验证通过，源码与开发构建差异见实施状态；仍未正式发布 |
| `TestCindyNewTurnRefreshesPricingWithoutChangingPendingBill` | 配置切换不能改变已完成请求的账单，长连接的新回合必须重新取当前配置 | 宿主保存请求价格快照，异步任务按账号身份传播；价格参考来自独立 Cindy 模块 |
| `TestImageStudioDisableCancelsHostExecutionAndKeepsSavedInputs` | 只隐藏 UI 不足以停止已排队或正在执行的领域任务 | 宿主 IO 绑定插件上下文，停用/配置更改取消执行；输入和历史数据保留 |
| `TestPublicPluginSurfaceRetainsFailureAndHidesDisabledConfiguration` | 停用与故障必须采用不同展示语义，公开元数据不能携带管理操作 | manifest 条件贡献，角色过滤，故障保留入口并阻止新请求 |
| `TestTrafficObservationFinishesWithCapturedPolicyAfterPluginDisable` | 停用不能把已开始观测的成功请求误记成失败 | 插件提供有界分类表，宿主冻结规则并完成一次收尾 |
| `TestPublicThemeAssetsRequireCurrentActivationDeclarationAndDigest`、`check-theme.py` | 主题资源须可撤销，宿主仍有基本可用样式 | 按声明公开 CSS/字体，校验资源摘要，深浅色和窄屏启停的计算样式验证通过 |
| `plugins/codex-runtime/scripts/check-ui.py` | 协议标签与控件不能一起居中对齐按钮 | 插件界面采用底部对齐与自然换行，桌面/窄屏及深浅色 DOM 检查通过 |
| `TestPromptDomainUsesActiveScopeWithoutBroadeningRequestBindings` | 全局管理查询不能把请求作用范围中的通配符当作实际账号类型 | 唯一领域提供者管理共享材料，逐账号注入仍执行绑定范围 |
| `TestRemoteSkillJobExpiryPreservesActiveOwnersAndRejectsLateCompletion` | 进程退出后不应永久显示运行中，也不能允许旧任务迟到提交 | 固定任务期限、数据库实时时钟校验；过期失败、不重放、不改活跃任务 |

## 继续审查

尚未决定保留或迁移的路径保持待审查。七域已建立，接下来核查剩余混合职责及局部主题声明；范围与验证状态以实施状态为准。

根项目运维、独立服务和 Worker 不属于此 Sub2API Git 清单，修正、证据和未完成项统一记录在项目根 `artifacts/evidence/v027-root-review.json`。历史 worktree、在线数据、凭据和账本均保留。
