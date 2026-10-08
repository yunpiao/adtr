# G03 首个工程切片验证记录

关联 #4，基于 #5 的架构契约分支；不关闭 #2/#3/#4。

## 基线和范围

初始 main 只有 README，无测试。此切片新增 Go API/Worker 进程入口、
配置校验、无敏感信息的健康探针、显式 schema 元数据迁移、开发编排与 CI。
数据库驱动 pgx v5.11.0，Go 1.27.1；依赖以实际 go.mod/go.sum 为准。
Go 工具链下载 SHA-256：`63d339f0da5ab53635a56f2490a7984dfe12dfcff22ad749f63edaf590168445`，
来源 https://go.dev/dl/?mode=json 与 https://go.dev/dl/go1.27.1.linux-amd64.tar.gz 。

## 验证命令

```sh
make check
python3 scripts/test_integration.py
python3 scripts/test_lifecycle.py
git diff --check
```

执行结果及最新 CI head 以草稿 PR 为准；本文不自动将运行计划记为通过。
CI 每次执行上述构建/集成/生命周期检查，不包含真实 AD 测试。
没有前端源代码，当前类型检查只覆盖 Go；TypeScript 工具链将在前端 Issue 引入。

## 已识别限制

- Worker 尚无业务任务循环；任务持久化、权限/审计模型、业务状态机在后续 P0 Issue 实现。
- 迁移只含 schema 元数据；目前开发角色共享，生产最小权限分离尚未实现。
- 健康检查按请求建立数据库连接，后续容量目标确定后评估池化与探针频率。
- Windows 72 场景仅规划分类，实验机、工具链和逐场景夹具仍阻塞；没有任何 Windows 通过声明。
- 没有真实 AD 夹具、规则算法、前端或 209 项业务功能；当前产品验收仍为 0/209。
- 云环境最初容器在线依赖下载因 CA 不可信失败；保留 TLS 校验，改为宿主校验依赖后容器离线构建。
- 外部独立审查待执行；自审实际 diff 不等同于独立审查。


## PR #6 review corrections on dot cloud computer (2026-10-07)

- Source baseline: remote fda2ba092ad708c13f98b1d11ef43ac45cb37f29, read through the authorized GitHub connector.
- Official Go 1.27.1 installed and archive SHA-256 verified before execution.
- Exact remote baseline `make check`: PASS (format, vet, race tests, build).
- Updated `make check`: PASS, including four Python lifecycle-runner regression tests.
- TLS regression coverage: ssl alias in both orders, duplicate/encoded parameters, host/service overrides,
  socket/non-verifying endpoints, retained primary and multi-host fallback configurations.
- Restart regression: record IDs before restart, poll the same containers directly, fail on replacement;
  explicit recreation is tested separately. Polling contract covers transient failure, timeout and ID changes.
- Independent read-only diff review found no blocking finding in these fixes; one parser-order comment corrected.
- This dot cloud computer currently has no Docker CLI/Engine. Real PostgreSQL migration and container lifecycle
  checks were NOT run here. GitHub CI for the new head must be checked separately; earlier head success does
  not verify these corrections. No AD/Windows or product acceptance is claimed.
