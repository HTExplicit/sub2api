# Sub2API 开发指南

每次任务先读 [AGENTS.md](AGENTS.md)；当前下游能力与发布边界见 [DOWNSTREAM.md](DOWNSTREAM.md)。本页只保留开发入口，不维护生产状态或重复测试清单。

## 环境与依赖

- 后端为 Go、Gin、Ent，数据服务为 PostgreSQL 与 Redis；Go 版本以 `backend/go.mod` 为准，CI、容器和工具链保持一致。
- 前端为 Vue 3，使用 pnpm；依赖及覆盖以 `frontend/package.json` 和 `pnpm-lock.yaml` 为准。修改依赖时同步锁文件，CI 使用 `--frozen-lockfile`。
- 本地数据库、密码和路径按开发环境配置，不使用文档中的共享凭据，不把真实凭据写进文档或产物。
- 修改 `backend/ent/schema` 后按需执行 `go generate ./ent`，提交必要生成结果。接口新增方法时只补受影响的 stub/mock。

## 开发入口

```bash
# 前端开发服务器
pnpm --dir frontend dev

# 后端开发服务器；先准备本地配置
cd backend
go run ./cmd/server/
```

根与后端 `Makefile` 提供构建入口。构建会运行其明确依赖的检查；没有构建风险时，不为文档整理触发构建。

## 最小必要验证

先将每项检查对应到本次改动的具体风险，再选择用例、包或文件。以下仅为调用形式，须替换成实际受影响对象：

```bash
# 后端：匹配具体用例，不扩大到全部包
go -C backend test -tags unit ./internal/<package> -run '^Test<ChangedBehavior>$'

# 前端：指定测试文件
pnpm --dir frontend exec vitest run src/<affected-test>.spec.ts
```

文档只做编码、链接、锚点和路径静态检查；同提交或相同代码树的通过结果直接复用。失败修正后只重跑该失败及直接影响；未改前端、并发或数据层时不主动增加相应专项。保留平台必需检查，具体路由见 `.github/workflows`；本地不重复已交给正常 PR CI 的业务测试。真实模型、支付、失败登录、全量或压力测试仅在任务确有对应风险时考虑。

CI 风险映射集中在 [ci_plan.py](.github/scripts/ci_plan.py)，检查归属与命令在 [ci_run.py](.github/scripts/ci_run.py)。无法归类的执行文件会阻断，须随改动补充具体风险及最小用例，不回退全套或判为无影响。结果摘要区分执行、复用和不适用；[复用凭据](.github/scripts/ci_receipts.py)须核对完整代码树、目标集合、验证定义及环境。

## 文件与数据边界

- 保持稳定源码和脚本路径；第三方文档、许可证、迁移 SQL 及 checksum 不为整理而改写。
- 删除前确认无引用、可再生或已交付；保留未提交、未合并、用途不明资料和诊断账本，Git worktree 用 Git 移除。
- 账号映射、网络、生产配置与数据只在明确授权的任务范围内调整。迁移不可逆边界见 [数据与回滚契约](.downstream/native-domains.md#release-and-rollback-boundary)。
- PowerShell 调 SQL 时避免 `$` 被插值；优先保存参数化 SQL 文件并用客户端读取。Windows 本地连接需要 IPv4 时显式使用 `127.0.0.1`。
