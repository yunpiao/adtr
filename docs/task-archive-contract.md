# F49B：终态健康任务的可恢复归档

关联 Issue #15、AD-F-193；这是本地工程切片，不表示来源兼容或产品验收完成。
原始需求未冻结保留期、消息定义、容量与删除规则。本实现仅将明确选中的
`infrastructure.health` 终态任务从默认列表归档，并允许还原。
没有自动到期、物理删除、消息清理、存储空间回收或磁盘阈值触发行为。
`audit.export`、AD 业务执行器与未知任务种类均不具备归档资格。

## 数据边界

- `adtr.tasks.terminal_at` 由服务端在首次终态时记录；归档、还原不改变该时间
- 历史记录仅在终态事件证据明确时回填；无法确认时保持 NULL，显示为未知且不能归档
- `adtr.task_visibility` 独立保存当前可见性；不存在记录表示 active、visibilityVersion=0
- 每次成功归档或还原使 visibilityVersion 加一，不执行任务、不恢复失败执行器
- payload、result、cursor、state、created/updated 时间、任务身份与父任务关系保持原样
- 提交幂等键、schedule occurrence、task_events、task_outbox 均保留，且不会被当作可丢弃消息
- `task_archive_operations` 保存不可变回执；`task_archive_events` 保存逐任务控制证据
- 控制证据有 actor、tenant、authorization epoch、前后版本、理由与请求元数据，禁止更新、删除、TRUNCATE

归档成功另在当前事务追加 `task.archive:<operationUUID>` 控制审计；还原为
`task.restore:<operationUUID>`。幂等重放不会重复追加控制审计。
控制审计不会伪造 executor 成功事件或改变既有任务/审计快照。

## 授权与事务

候选查询要求当前 `tasks.readable` 和显式 `task_archive.readable`。
归档、还原同时要求两个权限的 readable/writeable。已有自定义角色不自动获得新权限。
平台管理角色遵循现有显式内置权限策略。权限调整推进服务器授权 epoch。

健康任务沿用既有租户级管理范围，允许同租户的已授权操作者处理他人创建的健康任务；
没有跨租户绕过，也不把 audit-export 私有任务继承为健康任务权限。
任务 ID 不存在或属于另一租户，返回相同的 404。

每个请求先获得 schema 8 兼容性锁，再锁租户、当前 actor，最后按 taskUUID
字典序锁任务与其可见性。直接事务 API 缺少此前的 schema gate 会失败关闭。
所有身份、会话与当前权限均在事务中重查；写操作沿用密码、新鲜 TOTP、Origin、
CSRF、JSON 类型与速率限制。proof 消费、全部可见性变更、精确回执和控制审计
共同提交，任一失败回滚，HTTP 失败审计和滥用速率记录沿用既有安全机制。

## API

### 候选预览

`GET /api/tasks/archive-candidates?before=2026-10-01T00:00:00Z&pageIdx=1&pageSize=20`

- before 必填，为 UTC RFC3339 `Z` 时间，最多六位小数，不能晚于数据库时钟
- pageIdx 默认 1，范围 1–1,000,000；pageSize 默认 20，范围 1–100
- 拒绝重复、空、未知参数和非规范整数
- 仅包含未归档健康任务：终态、有已知 terminalAt 且严格早于 before、无有效租约
- 终态仅为 succeeded、failed、partial_failed、dead_letter、cancelled
- 按 terminalAt、taskUUID 稳定排序；授权和资格筛选先于 count 与分页
- 返回 before、page、tasks、exhausted；每项包含 taskUUID、taskName、domainId、state、terminalAt、visibilityVersion

预览只作选择依据。最终写操作会在锁定后重新检查全部条件。

### 归档与还原

`POST /api/tasks/archive` 接受完整且仅包含以下字段的 JSON：

```json
{
  "targets": [{"taskUUID": "server-task-id", "visibilityVersion": 0}],
  "before": "2026-10-01T00:00:00Z",
  "reason": "已核对健康检查结果",
  "idempotencyKey": "review-2026-10-01",
  "actorPassword": "current-password",
  "totpCode": "123456"
}
```

`POST /api/tasks/restore` 使用相同字段但必须省略 before。
targets 为 1–100 个不同 taskUUID；visibilityVersion 必须是非负整数。
reason 必须已去除首尾空白，1–500 Unicode 字符，不允许控制字符。
idempotencyKey 使用 1–128 位字母、数字、下划线、点或连字符。
拒绝缺少字段、null、未知字段、重复 JSON 键（含嵌套对象）、小数版本、
身份/租户/授权 epoch/执行状态/重试或租约断言。

整批成功或整批失败。任何目标版本过期返回 `409 visibility_conflict`；
正在执行、已归档、终态时间未知、截止时间未满足或种类不符返回
`409 task_not_archivable`。还原要求目标已归档且为无有效租约的健康终态任务。
错误响应不提供外国租户的目标信息或哪个目标先失败的细节。

成功返回：

```json
{
  "receipt": {
    "operationUUID": "server-operation-id",
    "action": "archive",
    "before": "2026-10-01T00:00:00Z",
    "reason": "已核对健康检查结果",
    "targets": [{"taskUUID": "server-task-id", "archived": true, "visibilityVersion": 1}],
    "occurredAt": "2026-10-07T08:00:00Z"
  },
  "replayed": false
}
```

还原回执不含 before，targets.archived=false。回执目标按 taskUUID 排序。
操作键以 tenant + actor 为作用域，跨归档/还原共享；同键不同规范化请求
返回 `409 idempotency_conflict`，目标顺序不同不构成不同请求。
完全相同的重试需要新的密码/TOTP 验证并返回原回执、replayed=true。
后续已还原时重放旧归档，仍返回旧回执，不再次归档。回执表示该次历史结果，
页面应重新读取当前列表，不应把回执当作当前状态。

## 与原任务入口的关系

普通列表默认 active，可显式查询 archived/all；维护视图和归档详情要求维护权限。
归档详情必须明确请求 archived 可见性。归档后的同键提交仍返回原任务并标记
archived，不创建另一任务。失败任务必须先还原，再通过既有 recovery API 创建
带父任务 ID 的新任务；还原本身不执行或恢复任务。

页面用“归档 / 还原”，呈现 UTC 截止时间、所选数量、理由和可恢复说明。
未知 terminalAt 不猜测年龄。网络结果不确定时保留原操作键并重新读取状态，
不得静默生成替代键或把原回执当成当前可见性。

## 验证与仍未完成的范围

`internal/taskarchive/validation_test.go` 覆盖严格 UTC/微秒界限、输入、稳定目标顺序、
严格终态年龄与租约资格。带 integration 标签的独立 PostgreSQL 测试覆盖批量回滚、
并发版本胜者、归档/还原/旧操作重放、控制证据不可变、历史未知时间、
提交幂等与 schedule occurrence 保留，以及恢复 ancestry。
`internal/auth/archive*_test.go` 覆盖 HTTP 边界、双权限、新鲜 proof、事务回滚和 schema gate。

本地单元/编译检查与实际 PostgreSQL/浏览器运行分别记录；缺 PostgreSQL 时不能记为通过。
消息来源、投递与确认状态、重放窗口、分类保留期、合规/审计约束、物理删除、
容量目标和源系统真实链路仍待冻结，AD-F-193 和 Issue #15 不能据此关闭。
