# 固定合成资产分类报告 v1

结论：本模块把既有 `assetassessment.Evaluate` 接入有界、不可变、owner 隔离的同步报告存储；不是 AD 采集、后台 Worker、旧系统状态映射或完整产品验收。关联 Issue #30、AD-F-038/039/040/042/052 的有限分类切片。产品验收仍 0/209。

## 来源与计算

`SeedFixtureTx` 仅供受信任安装/隔离测试代码调用，参数只有 tenant/domain。代码固定 `synthetic-assets-v1` 的 11 个对象、独立 nullable flags、登录断言、组属性、观察时间及三个允许场景：

- `baseline`: 2026-11-01T12:00:00Z。
- `freshness-boundary`: 基线 +24h，仍新鲜。
- `stale`: 基线 +24h+1ns，适用维度为 unknown/stale_observation。

观察明确 `complete=false`，理由 `fixed_synthetic_subset_not_directory_inventory`。汇总仅表示这 11 个已观察合成对象，不是目录总体。来源 hash 绑定全部证据、完整性及场景；读回须与固定生产者逐项一致，重算 hash 也不能伪造来源。不得把真实目录对象改标 synthetic，也没有接受任意证据/结果/时间的 HTTP 导入路径。

每行调用未修改的真实 Evaluate，版本 `simulated-asset-v1`。disabled 优先；disabled 未知且 locked=true 仍 unknown；enabled 只表示两个明确 false 标记，**不是旧系统 active0**。登录年龄 >=30天 inactive；缺少时间不能推断 never，必须显式 never 断言。组仅分类 security/distribution 和 global/domain_local/universal。未知、不适用、过期原因分别保留。阈值不是请求参数，更改语义需新版本。没有风险、密码到期、信任、有效 ACL、身份设备关联等伪零指标。

## 调用与授权边界

公开 DTO/API 位于 `internal/simulationassetassessment/types.go`。调用者持有已认证事务，**先**核验当前有效会话、tenant、domain 资源和独立 read/manage 授权，并持有授权 epoch 锁，才读取证据。`Scope.Epoch` 是当前授权代，不是客户端参数；后端要求正数并与历史报告/回执比较。撤销后重新授予不得复活旧代报告。历史列表只列当前代，旧代详情/回执返回 not found（404），不暴露历史对象；缺少当前 read/manage 授权仍返回 denied（403）。不同 owner/tenant/domain 的对象返回 not found。Scope 自身不是认证器。

`CreateTx` 接受且只接受：
- `id`：报告 owner 内唯一标识，必需，1–128 ASCII `[A-Za-z0-9_.-]`。复用其他操作已创建 ID 冲突。
- `operationId`：owner 内幂等键，必需。同一规范请求重试返回同一回执且不重复审计；不同请求冲突。旧代回执读取为404。旧代 operationId 永久保留；重授后以同一键提交相同或不同报告均返回 conflict（409），不新增报告/回执/审计；旧报告 ID 也不能复用。
- `observationId`：固定来源引用，必需；不存在拒绝。
- `expectedSourceHash`：必需 SHA256；防止基于不同观察创建报告，不匹配冲突。
- `scenarioId`：必需服务器固定场景，未知值拒绝；客户端不能提供 asOf。

这些参数均不授予权限。删除任何必需参数会拒绝，固定场景的代价是不能任意评估用户选择的时间。`AuditFunc` 必需，与报告和回执同事务；嵌套 savepoint 保证回调失败即使外层误提交也不留下报告/回执。调用者仍须在任意错误回滚外层事务。

## 不可变、预算与列表

三张独立表 `simulation_asset_observations/reports/receipts` 具有 UPDATE/DELETE/TRUNCATE 禁止触发器，不提供删除/覆盖 API。报告 pin 来源 hash、算法版、场景、asOf、owner、epoch、创建时间、结果和完整汇总。所有读回校验 provenance/hash，并重新执行固定来源分类比较；篡改后重算 hash 仍无法更改统计/行。

每来源最多64对象；每 owner/tenant/domain **跨 epoch 总计**64报告，不能通过撤销/重授绕过容量。每操作一回执，一份真实报告；重放不占新配额。owner 域事务锁串行化容量、报告 ID 及幂等键。所有存储 JSONB 上限262144字节，预计算 JSONB 分隔空格开销；响应上限524288字节。超限整体拒绝，不返回截断的“完整结果”。固定11对象远低于限额，但限制仍是必需安全边界。

`RowsTx` 的 kind/accountStatus/activity/groupType/groupScope 是枚举精确过滤，offset 0–64，limit 1–20，非法值拒绝。matched 是全部匹配行数；Summary 始终对应全报告11行，不随当前页/筛选变化。Reasons 按四个分类维度计数，故总和为4×Total，不是对象去重数。历史 offset 0–64、limit 1–20，按报告 ID 稳定排序，total 为当前代报告数；空页用空数组。详情返回完整但有界报告，不执行新采集。

## 验证与开放项

单元测试使用独立手写11行分类和汇总预期，覆盖 disabled/locked 九格真值表、30天±1ns、24h±1ns、never/unknown/矛盾时间、组范围、重算hash篡改、JSONB预算、分页参数和旧代拒绝。`go test -race ./internal/simulationassetassessment` 与 `go vet ./internal/simulationassetassessment` 本地通过。

实际 PostgreSQL 命名门禁：
- TestPostgresAssetAssessmentPersistenceReplayAndPaging
- TestPostgresAssetAssessmentOwnerEpochAndSource
- TestPostgresAssetAssessmentAtomicRollbackAndImmutable
- TestPostgresAssetAssessmentTamperAndQuota
- TestPostgresAssetAssessmentConcurrentIdempotence

本机缺 `ADTR_TEST_DATABASE_URL`，上述门禁本地因缺DSN明确失败（未执行数据库断言），不能记为通过。HTTP 当前权限/CSRF/事务 epoch、schema32集成、实际 PostgreSQL 与浏览器、独立复审、精确 PR head CI、main合并后验证由整合流程独立记录。本文件不以源码/模拟单测替代这些门禁，更不代表真实AD/Windows、完整旧字段映射、容量或产品验收。
