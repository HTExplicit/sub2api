# Sub2API 插件开发

本页描述当前 `v0.2.7` 下游分支的实现，尚不代表生产已升级。迁移与交付状态见[实施状态](../.downstream/implementation-v0.2.7.md)。插件以独立模块、独立进程和签名资源包运行；宿主保留鉴权、凭据刷新、事务、金额提交及通用请求生命周期。

## 从现有模块开始

使用与 `backend/go.mod` 一致的 Go 工具链。插件只导入公开的 `backend/pkg/extensionapi/v1` 或旧传输 `backend/pkg/pluginapi/v1`，不能导入宿主 `internal` 包。

| 模块 | 已迁入的主要职责 |
|---|---|
| `plugins/codex-runtime` | 292 采集、校验与注入、有限续期、代理诊断 |
| `plugins/model-policy` | 静态容量参考、精确别名与供应商范围 |
| `plugins/prompt-skills` | 提示词范围、完整性、协议载体和回显策略 |
| `plugins/account-tools` | 分类策略、逐账号测试计划和推理选项 |
| `plugins/cindy-provider` | 目录、价格参考、协议能力及健康错误分类 |
| `plugins/image-tools` | 图像生成/编辑计划、模型投影和图像功能配置 |
| `plugins/admin-observability` | 流量规则、指标展示和可停用主题资源 |

这些模块可作为实现示例；剩余混合职责以实施状态中的未完成项为准。`plugins/bundle.source.json` 是镜像内置集合及首次迁移配置的唯一清单。

每个模块维护自己的 `go.mod`、`cmd/plugin/main.go`、业务包、`manifest.source.json` 和 `ui/`。入口调用 `extensionv1.Serve`，业务模块实现 `Invoke`、`ValidateConfig`、`ApplyConfig` 和 `Status`。需要宿主数据的模块实现 `SetHost(*extensionv1.Client)`。

## 声明能力与生命周期

manifest 的身份、版本、协议版本和能力必须与程序 `GetInfo` 一致。当前扩展 API 为 1；JSON 请求通过有界 gRPC 信封传送，单次输入和输出上限均为 4 MiB。

| 能力 | 边界 |
|---|---|
| `extensions.provider.v1` | 供应商策略，按平台和账号类型限定 |
| `extensions.catalog.v1` | 模型目录参考 |
| `extensions.request.v1` | 请求计划和前置条件，不自动授予凭据权限 |
| `extensions.scheduling.v1` | 调度约束及持久摘要 |
| `extensions.jobs.v1` | 宿主持久任务 |
| `extensions.admin.v1` | 已声明、经管理员授权的操作 |
| `extensions.observability.v1` | 有范围限制的账号计数器，不包含凭据和请求正文 |
| `extensions.credentials.v1` | 显式声明范围内的敏感出站身份解析 |
| `extensions.ui.v1` | 界面贡献；公开主题使用通配平台和账号类型绑定 |

命名操作通过 manifest 的 `operations` 按能力声明。作用范围重叠的两个已启用插件不能拥有同名操作。宿主在调用前检查实际绑定、依赖和健康状态；结果 `plugin_id` 由宿主写入。旧 `openai.oauth.outbound_transport.v1` 仍支持原有流式契约，参考 `backend/pkg/pluginapi/docs/`；其 `request_sent` 必须准确，防止重复上游请求。

不要把完整客户历史、图片或大文件复制进 RPC。传送有界事实与计划，由宿主处理原始载荷。价格和观测规则等需要跨异步边界的结果，在请求开始时冻结，新请求重新检查插件状态。

`ValidateConfig` 拒绝未知字段和错误类型，返回完整规范化对象。宿主加密保存配置，历史环境变量仅用于首次迁移。停用撤下入口并阻止新调用；启用但故障保留入口和不可用原因。已有历史、已停止续期状态、账本和结果保留。

宿主任务可绑定插件操作上下文，在停用、替换或实际配置变化时取消 IO。保存相同配置不会取消现有任务。已经开始计数或产生费用的请求使用冻结快照收尾。缓存必须有界，按配置版本隔离，使用前仍检查启停和健康；不可用时不得恢复旧宿主业务作为隐式回退。

## 页面和公开主题

配置页面位于 `ui/`，在受限 iframe 中运行。打包器自动加入 `backend/pkg/extensionapi/ui/bridge.js`，页面使用 `Sub2APIPluginBridge`。

复杂页面位于插件自己的 `ui/src`，只导入 Vue 等公开依赖和 `@sub2api/plugin-ui`。公共 UI SDK 提供对话框、图标、错误处理、基础样式和渲染工具；宿主旧组件路径仅作转出适配，插件不引用宿主内部组件。`node backend/pkg/extensionapi/ui/build.mjs prompt-skills` 编译所选域，省略域名编译所有已声明源码入口。构建产物进入 `ui/compiled`，不提交；打包跳过源码和测试目录。贡献的入口必须实际包含在签名包里。

| 方法 | 用途 |
|---|---|
| `context()` | 挂载模式、语言、主题与已验证账号上下文 |
| `config()` / `save(config)` | 配置读取、校验和保存 |
| `status()` | 被动状态，不执行模型测试 |
| `invoke(action, payload)` | 贡献中声明的管理操作 |
| `submit(action, items, key)` | 带稳定幂等键的宿主持久任务 |
| `resource(name, input)` | 具名宿主数据操作；`params`、`query`、`body` 或 `form` 提供原接口的参数 |
| `resize(height)` / `dispose()` | 尺寸和卸载清理 |

账号操作、详情、测试字段、设置页和宿主表面通过 manifest 贡献接入。`config_flag` 引用一个布尔配置字段，关闭时撤下入口，读取失败时显示不可用。持续任务的进度、取消和历史由宿主管理。

数据资源通过 manifest 的 `resources` 显式声明名称、所需能力和用户角色。HTTP 方法与路径由宿主注册，前端桥接只接受名称，不能传任意 URL 或请求头。实际操作仍经过原接口的认证、审计、合规与二次验证链；资源守卫再次检查持久启停状态、包摘要及运行周期。兼容 API 调用同样不能绕过停用状态。

页面会话按贡献选择签名入口，并限定管理员或普通用户角色；普通用户不能选择管理页面。模块脚本使用隔离来源所需的 CORS 资源响应，iframe 保持 `sandbox="allow-scripts"`，不开放同源访问。主题、语言及可用状态通过桥接更新，故障时保留输入并禁用动作，升级后的旧页面需重新打开。

普通用户通过 `/api/v1/settings/plugins` 获得公开表面元数据，不获得管理员动作、iframe 入口或配置字段。公开主题声明 `slot: "theme"`、`permission: "public"`、CSS `entrypoint` 和最多 32 个 WOFF2 `assets`。宿主仅公开这些已声明且哈希匹配的样式与字体，不公开脚本或其他包文件。资源 URL 绑定资源清单摘要；停用或故障后移除样式，宿主基本样式及可访问性规则继续工作。字体许可证随包分发。

## 构建与签名

只编辑 `manifest.source.json`。打包器生成运行时路径、文件 SHA-256、已测试宿主版本和签名。签名后不再改写 manifest；私钥不进入源码、镜像或插件包。

以下 PowerShell 示例从仓库根目录构建现有模型目录模块，生成的密钥仅供本地开发验证：

```powershell
$pluginBuild = Join-Path ([IO.Path]::GetTempPath()) ('sub2api-plugin-' + [guid]::NewGuid().ToString('N'))
New-Item -ItemType Directory -Path $pluginBuild | Out-Null
go -C plugins/model-policy build -trimpath -o (Join-Path $pluginBuild 'model-policy.exe') ./cmd/plugin
go -C backend run ./cmd/package-plugin -generate-key (Join-Path $pluginBuild 'publisher.key')
go -C backend run ./cmd/package-plugin `
  -source ../plugins/model-policy `
  -binary (Join-Path $pluginBuild 'model-policy.exe') `
  -platform windows-amd64 -tested-host-version 0.2.7 `
  -output (Join-Path $pluginBuild 'model-policy.s2plugin') `
  -signing-key-file (Join-Path $pluginBuild 'publisher.key') `
  -key-id local-model-policy-test
```

第三方开发包在 `plugins.trusted_publishers` 中配置自己的 `key_id` 和公钥。正式 Codexrip 公钥唯一保存在 `backend/pkg/extensionapi/v1/publisher.json`，宿主内置该信任身份，配置不能覆盖；私钥位于 GitHub Secret `SUB2API_PLUGIN_SIGNING_KEY`。`go run .github/scripts/initialize_plugin_publisher.go` 只检查当前配置，显式 `-initialize` 也拒绝替换已经存在的密钥。

集合打包使用 `-bundle-source`、`-binary-dir` 和新的空输出目录；`-plugin-domain` 只选择清单中的一个域。`-build-binaries` 从各 manifest 读取独立版本，并注入程序版本；`-verify-bundle` 复用生产签名、文件和目标平台校验，不执行程序。`-source-revision` 把完整源码提交写入签名清单。构建程序时不传签名密钥，签名步骤只运行已构建的打包器。

Downstream Release 先生成 `deploy/plugin-bundle/current`，正式 Docker 阶段校验后原样复制这些包；同一份包与锁清单作为 Release 附件发布。默认候选镜像使用合成开发签名，正式 Codexrip 宿主拒绝开发发布者。开发目录中的旧二进制不用于发布。

Independent Plugin Release 接受不可变 `plugins/<域名>/v<版本>` 标签和已发布宿主标签，只构建所选域。它要求已通过主分支检查的宿主实现与所选宿主 Release 相同（测试源码除外），才能复用其验证结果；需要新宿主接口时先发布宿主。发布不覆盖同名 Release，安装也拒绝同版本不同包。

## 更新单个插件

插件管理页的“更新插件包”保持配置、逐能力启停和灰度，接受同 ID 的更高版本签名包。先验证签名、版本、依赖和保存的配置，再提交持久更新记录；候选校验不获得宿主存储、凭据或任务权限。相同包重复提交不重复执行，同版本不同内容拒绝。

| 接口 | 请求 | 结果 |
|---|---|---|
| `POST /api/v1/admin/plugins/:id/update` | multipart：`plugin`、`expected_revision`、`expected_package_sha256` | 返回安装记录；`updating` 表示等待旧进程排空 |
| `POST /api/v1/admin/plugins/:id/follow-bundled` | JSON：`expected_revision`、`expected_package_sha256` | 明确恢复跟随当前镜像内置包 |

两个接口沿用管理员授权和现有二次认证。安装记录增加 `revision`、`package_sha256` 和 `update_policy`。独立更新设置为 `pinned`，宿主升级保留该版本；`bundled` 才跟随内置包。恢复跟随可能选择较旧的内置版本，但不会回滚账号或业务数据。

每个运行进程持有独立 PostgreSQL 会话锁，不占用业务查询池；只有全部旧进程退出后才能提交新的执行代次。更新中的版本与意图保存在数据库，重启继续处理；期间仍可明确停用，新能力默认关闭。旧代次的状态写入和租约领取被拒绝。更新失败不切回旧宿主业务，候选校验失败不改变当前版本。

配置先校验并按安装修订号提交，成功后再应用到运行进程；冲突不取消现有工作。插件资源会话绑定整包摘要，升级后的旧会话返回 410，重新打开界面取得当前版本资源。

## 按实际改动验证

独立模块验证自身策略；宿主验证范围、取消、事务和真实进程通信；签名包验证清单、哈希及资源边界。选择与改动风险对应的用例，复用相同代码的已有结果，保留正常 PR 必需检查。

本地模型目录进程契约可使用上面构建的程序：

```powershell
$env:SUB2API_MODEL_POLICY_TEST_BINARY = Join-Path $pluginBuild 'model-policy.exe'
go -C backend test ./internal/service -run '^TestCatalogExtensionRuntimeUsesIndependentProcess$' -count=1
```

这些契约使用合成数据，不需要真实模型请求。主题离线浏览器脚本为 `plugins/admin-observability/scripts/check-theme.py`，票据界面脚本为 `plugins/codex-runtime/scripts/check-ui.py`；已验证范围及未完成项见实施状态。
