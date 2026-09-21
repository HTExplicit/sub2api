# Sub2API 插件开发

本页描述当前 `v0.2.7` 下游分支的实现，尚不代表生产已升级。迁移与交付状态见[实施状态](../.downstream/implementation-v0.2.7.md)。插件以独立模块、独立进程和签名资源包运行；宿主保留鉴权、凭据刷新、事务、金额提交及通用请求生命周期。

## 从现有模块开始

使用与 `backend/go.mod` 一致的 Go 工具链。插件只导入公开的 `backend/pkg/extensionapi/v1` 或旧传输 `backend/pkg/pluginapi/v1`，不能导入宿主 `internal` 包。

| 模块 | 已迁入的主要职责 |
|---|---|
| `plugins/codex-runtime` | 292采集、校验/注入、有限续期、身份/传输、推理恢复/回放规则及诊断界面 |
| `plugins/model-policy` | 静态容量参考、精确别名与供应商范围 |
| `plugins/prompt-skills` | 提示词范围、协议载体、回显、发布/回滚、技能来源/存储计划及管理界面 |
| `plugins/account-tools` | 分类策略、逐账号测试计划和推理选项 |
| `plugins/cindy-provider` | 目录、价格参考、协议能力及健康错误分类 |
| `plugins/image-tools` | 图像生成/编辑计划、模型投影和图像功能配置 |
| `plugins/admin-observability` | 流量规则、指标展示、提示词审计管理界面和可停用主题资源 |

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
| `extensions.recovery.v1` | 有账号范围的恢复/回放规则及脱敏诊断，不提供原始客户历史 |
| `extensions.credentials.v1` | 显式声明范围内的敏感出站身份解析 |
| `extensions.ui.v1` | 界面贡献；公开主题使用通配平台和账号类型绑定 |

命名操作通过 manifest 的 `operations` 按能力声明。作用范围重叠的两个已启用插件不能拥有同名操作。宿主在调用前检查实际绑定、依赖和健康状态；结果 `plugin_id` 由宿主写入。旧 `openai.oauth.outbound_transport.v1` 仍支持原有流式契约，参考 `backend/pkg/pluginapi/docs/`；其 `request_sent` 必须准确，防止重复上游请求。

声明 `requires.extension_api` 的进程只使用带账号范围和执行代次的具名宿主端口，不能同时使用旧 `HostService` 的 KV 或账号目录绕过这些限制；旧 transport-only 插件未声明扩展 API 时仍保留原协议。具名策略即使命中缓存，也会重新核验当前持久状态、绑定和已应用配置，调用期间的配置变化或取消会使该次结果失效。需要后续宿主出站的流程仍须保留自身操作上下文，不能把一次策略查询当作无限期授权。

安装、绑定启停、卸载、更新提交及内置初始化的最终写入共享 PostgreSQL `SERIALIZABLE` 事务内的依赖图校验。健康故障不擦除期望启用状态；校验失败会回滚本次图修改和同事务任务取消。序列化冲突返回既有状态冲突，不自动重放业务。这只约束采用该协议的新写入路径，不保证旧版本写入器或绕过仓库的直接 SQL 安全。

不要把完整客户历史、图片或大文件复制进 RPC。传送有界事实与计划，由宿主处理原始载荷。价格和观测规则等需要跨异步边界的结果，在请求开始时冻结，新请求重新检查插件状态。

`ValidateConfig` 拒绝未知字段和错误类型，返回完整规范化对象。宿主加密保存配置，历史环境变量仅用于首次迁移。停用撤下入口并阻止新调用；启用但故障保留入口和不可用原因。已有历史、已停止续期状态、账本和结果保留。

宿主任务可绑定插件操作上下文，在停用、替换或实际配置变化时取消 IO。保存相同配置不会取消现有任务。已经开始计数或产生费用的请求使用冻结快照收尾。缓存必须有界，按配置版本隔离，使用前仍检查启停和健康；不可用时不得恢复旧宿主业务作为隐式回退。

具名调用的 `account_id` 由宿主从实际授权目标写入；权限与灰度在缓存和执行前检查。账号任务元数据中的 `plugin_id`、`plugin_generation` 也由宿主写入；创建事务检查当前启停与代次。失败重试保留原插件，显式重新校验当前版本与账号范围，不能通过重试扩大已收窄的绑定。旧代次任务不能在新进程上自动继续。

实际账号目录查询也必须传 `CatalogQuery.account_id`；省略或零值仅保留无账号的共享参考查询兼容性。账号型 broker 在读凭据、指标或写账号投影前读取当前安装范围，不能把 payload 里的账号元数据当作调用准入。已经发出的观测仍可按原归属收尾，不因此开始新业务请求。

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
| UI SDK `resourceAvailability()` | 只读刷新当前会话角色下的具名资源 `{name, available}`；不返回任意 URL 或请求头权限 |
| `event(name, payload)` | 只发送当前贡献在 `events` 声明的界面事件 |
| `openJob(id)` | 宿主重新读取并验证归属后展示通用任务进度 |
| `preference(key)` / `savePreference(key, value)` | 按用户和插件隔离的浏览器偏好，不开放任意存储键 |
| `resize(height)` / `dispose()` | 尺寸和卸载清理 |

账号操作、详情、测试字段、设置页和宿主表面通过 manifest 贡献接入。`config_flag` 引用一个布尔配置字段，关闭时撤下入口，读取失败时显示不可用。持续任务的进度、取消和历史由宿主管理。

配置页面须先调用 `config()` 再 `save()` 或测试。Bridge 仍传原始配置对象；宿主保留该次读取的安装修订号和包摘要，不让页面载荷伪造版本。冲突不自动读取新版覆盖草稿；用户明确重新加载后才采用新快照。配置请求中名为 `code`、`message` 或 `data` 的字段仍是配置，不按普通 API envelope 解包。

全局管理界面可声明 `all_accounts: true`，同时指定管理员角色与所需能力；只有平台/账号类型均为通配、灰度为100%的绑定才提供该入口。对应资源也声明完整范围，描述、提交与执行使用相同条件。分页组件接受宿主提供的选项；具名 `table-page-size` 偏好沿用原有非敏感浏览器分页设置，其他偏好继续按用户/插件隔离。

第一方贡献都显式声明 `capability`；带管理员动作的贡献还必须满足固有Admin权限。例如票据采集同时需要Credentials与Admin，计数器查看同时需要Observability与Admin。宿主的 `account_scope` 是当前有效绑定交集的只读投影，不是插件可提交的授权字段；界面和提交对整组选中账号使用同一作用范围与灰度。缺少任一账号资料时拒绝整批，不静默剔除目标。旧第三方清单缺能力字段的页面兼容规则保留，执行仍要验证原有管理权限。

数据资源通过 manifest 的 `resources` 显式声明名称、所需能力和用户角色。HTTP 方法与路径由宿主注册，前端桥接只接受名称，不能传任意 URL 或请求头。实际操作仍经过原接口的认证、审计、合规与二次验证链；资源守卫再次检查持久启停状态、包摘要及运行周期。兼容 API 调用同样不能绕过停用状态。

页面可以按资源可用性分别禁用具体动作，并在提交前重新查询；查询失败不得启用动作，界面结果也不能替代后端实时门禁。例如全局文件夹/标签删除需要全账号权限，但局部分类赋值不因此升级为全局操作。管理员配置页在插件停用或故障时仍可打开用于修复，它不授予业务执行权限。

账号资源还核验目标账号及灰度范围，筛选批量操作要求覆盖完整账号范围；没有资格的目标不会被静默过滤。事务使用原接口校验，界面上下文传入领域调用时绑定已验证的插件所有者。分类保存面向插件仅返回账号 ID，不返回完整账号或凭据。创建任务使用 `operation_key` 映射到既有幂等键，任务记录和取消仍由宿主负责。

内联界面用 `ExtensionWidget`，操作弹窗用 `ExtensionModal`；原入口可保留轻量参数适配。SDK 对 JSON 参数去除 Vue 响应式代理，保留原生上传文件；取消或关闭页面会终止仍在执行的资源读取。通用字段支持模型选项和受限文本区域，标签、提示与字符上限由插件声明。

只读表格使用 `display_fields` 声明文本、徽标或日期字段；宿主仅渲染挂载处明确给出的标量，不执行表达式或遍历账号对象。插件上传解析可以使用包内内联 Blob Worker，仍无直接网络权限。浏览器本地媒体使用具名 `LOCAL` 资源与 `local_data`，Blob 保留原生类型，实际所有者由宿主登录用户决定；历史与取消等 `Retained` 资源只由宿主登记，插件声明不能自行取得这一特权。

页面会话按贡献选择签名入口，并限定管理员或普通用户角色；普通用户不能选择管理页面。模块脚本使用隔离来源所需的 CORS 资源响应，iframe 使用 `sandbox="allow-scripts allow-forms"` 以支持表单事件和原生校验，保持同源隔离；CSP 的 `form-action 'none'` 与 `connect-src 'none'` 阻止直接网络提交。主题、语言及可用状态通过桥接更新，故障时保留输入并禁用动作，升级后的旧页面需重新打开。

普通用户通过 `/api/v1/settings/plugins` 获得公开表面元数据，不获得管理员动作、iframe 入口或配置字段。公开主题声明 `slot: "theme"`、`permission: "public"`、CSS `entrypoint` 和最多 32 个 WOFF2 `assets`。宿主仅公开这些已声明且哈希匹配的样式与字体，不公开脚本或其他包文件。资源 URL 绑定资源清单摘要；停用或故障后移除样式，宿主基本样式及可访问性规则继续工作。字体许可证随包分发。

Canvas图表通过宿主通用颜色读取器使用 `--theme-chart-*` 参数，主题加载、撤下及深浅色变化会更新已挂载的图表。数据和统计逻辑不属于主题。技能来源使用已注册的网络与公开根，profile不能自行授予新URL；存储计划只选择受支持布局和槽位，候选键须是内容摘要，宿主执行固定根内的完整性检查和原子发布。

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

普通上传只用于首次安装。已有相同插件身份时，无论是否停用，都返回 `409 / PLUGIN_ALREADY_INSTALLED`；不能通过旧上传覆盖包、重置灰度或绕过下列更新契约。明确卸载后的重新安装保留既有迁移/诊断账本，不等于升级回滚。

插件管理页的“更新插件包”保持配置、逐能力启停和灰度，接受同 ID 的更高版本签名包。先验证签名、版本、依赖和保存的配置，再提交持久更新记录；候选校验不获得宿主存储、凭据或任务权限。相同包重复提交不重复执行，同版本不同内容拒绝。

| 接口 | 请求 | 结果 |
|---|---|---|
| `POST /api/v1/admin/plugins/:id/update` | multipart：`plugin`、`expected_revision`、`expected_package_sha256` | 返回安装记录；`updating` 表示等待旧进程排空 |
| `POST /api/v1/admin/plugins/:id/follow-bundled` | JSON：`expected_revision`、`expected_package_sha256` | 明确恢复跟随当前镜像内置包 |

两个接口沿用管理员授权和现有二次认证。安装记录增加 `revision`、`package_sha256` 和 `update_policy`。独立更新设置为 `pinned`，宿主升级保留该版本；`bundled` 才跟随内置包。恢复跟随可能选择较旧的内置版本，但不会回滚账号或业务数据。

每个运行进程和绑定的宿主操作持有独立 PostgreSQL 会话锁，不占用业务查询池。正常切换先排空旧进程、取消并收尾绑定的宿主 IO，再提交新的执行代次；数据库会话异常失联的限制见下段。上游 IO 可以脱离客户端断线，但仍保留插件取消信号，已经产生的计费则独立提交。更新中的版本与意图保存在数据库，重启继续处理；期间仍可明确停用，新能力默认关闭。旧代次的状态写入、任务创建和租约领取被拒绝。更新失败不切回旧宿主业务，候选校验失败不改变当前版本。

专用会话现在由宿主观察器检测失联（1秒探测间隔、1秒探测超时），首次失败即排空/取消并关闭原连接，不自动重连。正常释放不报告故障，取消不提前代替宿主任务清理。数据库可能先释放失联会话的锁、再由本机检测到；这不是跨实例原子撤销，不能撤回已被上游接受的IO。持久任务租约、调用前已花费阶段和事务幂等仍是防重复的独立保障。依赖图事务准入与这种在途 IO 的撤销是两项不同保证，不能互相替代。

配置保存、启用、停用、测试及删除的 HTTP 请求必须同时携带 `X-Sub2API-Plugin-Revision` 和 `X-Sub2API-Plugin-Package`；动作和新任务提交必须携带包摘要。缺失、非法及过期条件分别返回 428、400、409。读取配置从同一持久快照返回 raw JSON 和上述响应头，并禁止缓存；保存回执使用条件写入的 `RETURNING revision`，不通过后续读取借用另一位管理员的新版本。相同配置仍做 CAS，但不增加修订、不重复应用或取消工作。写入 CAS 冲突不改变现有工作；实际配置变更沿用原有取消和应用机制。应用前后若有另一次并发配置变更，旧进程在当前配置/代次门禁下不可执行，等待协调更新，不声称跨宿主 RPC 与数据库写入原子化。

插件资源会话绑定整包摘要，升级后的旧会话返回 410，重新打开界面取得当前版本资源；旧页面的动作/新任务还受上述包版本检查。历史、已完成结果和取消入口不因旧页面版本而被一并封锁。

## 按实际改动验证

独立模块验证自身策略；宿主验证范围、取消、事务和真实进程通信；签名包验证清单、哈希及资源边界。选择与改动风险对应的用例，复用相同代码的已有结果，保留正常 PR 必需检查。

本地模型目录进程契约可使用上面构建的程序：

```powershell
$env:SUB2API_MODEL_POLICY_TEST_BINARY = Join-Path $pluginBuild 'model-policy.exe'
go -C backend test ./internal/service -run '^TestCatalogExtensionRuntimeUsesIndependentProcess$' -count=1
```

这些契约使用合成数据，不需要真实模型请求。主题离线浏览器脚本为 `plugins/admin-observability/scripts/check-theme.py`，票据界面脚本为 `plugins/codex-runtime/scripts/check-ui.py`；已验证范围及未完成项见实施状态。
