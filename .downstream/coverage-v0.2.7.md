# 下游归属与审查覆盖

当前清单尚未完成审查，不能用文件已分类代替功能迁移完成。生产仍按根项目手册运行；发布完成状态只记录在[实施状态](implementation-v0.2.7.md)。

## 范围

基础清单以官方精确提交 `aea725f2ea644d5592d0bbb1d63b607efa7e200a` 对比当时的代码树，共 2061 个差异路径，其中 533 个生成文件、固定素材及依赖锁文件，1528 个手写代码、测试和文档路径列在[机器可读清单](coverage-v0.2.7.json)。新增的提示词、账号工具、Cindy、图像模块及配套改动尚待刷新纳入完整清单；这些数字不是最新代码树的覆盖率。候选归属只用于找到审查入口，混合文件必须按实际调用链判定，不能整文件判作已迁移。

| 候选归属 | 路径数 |
|---|---:|
| operations | 34 |
| cindy-provider | 158 |
| documentation-and-history | 9 |
| mixed-requires-review | 1009 |
| core-extension-and-job-host | 72 |
| model-policy | 19 |
| core-data-and-migrations | 21 |
| image-tools | 27 |
| account-tools | 40 |
| codex-runtime | 34 |
| prompt-skills | 67 |
| core-quota-and-protocol | 22 |
| admin-observability | 16 |

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
| `TestFirstPartyBundleContainsMatchingSignedDomainPackages` | 每个插件及界面资源必须对应锁定签名包 | 已验证六域签名/独立进程，最新源码与构建差异见实施状态；最终插件集合尚未完成 |
| `TestCindyNewTurnRefreshesPricingWithoutChangingPendingBill` | 配置切换不能改变已完成请求的账单，长连接的新回合必须重新取当前配置 | 宿主保存请求价格快照，异步任务按账号身份传播；价格参考来自独立 Cindy 模块 |
| `TestImageStudioDisableCancelsHostExecutionAndKeepsSavedInputs` | 只隐藏 UI 不足以停止已排队或正在执行的领域任务 | 宿主 IO 绑定插件上下文，停用/配置更改取消执行；输入和历史数据保留 |
| `TestPublicPluginSurfaceRetainsFailureAndHidesDisabledConfiguration` | 停用与故障必须采用不同展示语义，公开元数据不能携带管理操作 | manifest 条件贡献，角色过滤，故障保留入口并阻止新请求 |
| `plugins/codex-runtime/scripts/check-ui.py` | 协议标签与控件不能一起居中对齐按钮 | 插件界面采用底部对齐与自然换行，桌面/窄屏及深浅色 DOM 检查通过 |

## 继续审查

尚未决定保留或迁移的路径保持待审查。下一步建立界面/观测域，并继续核查六个已有域的混合调用；范围与验证状态仅以实施状态为准。

根项目运维、独立服务和 Worker 不属于此 Sub2API Git 清单，必须单独检查。当前已确认 `deploy-session-converter.ps1`、`deploy-2fauth.ps1` 使用固定入口；若干 s-ui、备份、清理和旧安装入口仍有私钥路径，尚未统一修正。历史 worktree、在线数据、凭据和账本均保留。
