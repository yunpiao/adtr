# F49B：终态健康任务的可恢复归档

关联 Issue #15、AD-F-193；未知提交的逐项目标核对见 Issue #200。
这是本地工程切片，不表示来源兼容或产品验收完成。
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
页面应重新读取目标当前状态，不应把回执当作当前状态。

## 与原任务入口的关系

普通列表默认 active，可显式查询 archived/all；维护视图和归档详情要求维护权限。
归档详情必须明确请求 archived 可见性。归档后的同键提交仍返回原任务并标记
archived，不创建另一任务。失败任务必须先还原，再通过既有 recovery API 创建
带父任务 ID 的新任务；还原本身不执行或恢复任务。

页面用“归档 / 还原”，呈现 UTC 截止时间、所选数量、理由和可恢复说明。
未知 terminalAt 不猜测年龄。网络结果不确定时保留原操作键并重新读取状态，
不得静默生成替代键或把原回执当成当前可见性。

## 未知提交后的原目标逐项核对

已尝试提交的归档或还原确认页提供“选择待核对目标”和
“读取所选目标当前状态”。选择器只包含原操作完整的 1–100 个目标及其
原预期版本，不接受自由输入任务 ID，不通过列表第一页推断目标是否存在。
初次进入、选择目标、刷新或重回页面均不自动读取目标。

每次手动读取只核对一个目标：先使用现有 `GET /api/auth/me` 核对当前会话，
再执行 `GET /api/tasks/detail?taskUUID=<原目标>&visibility=all`，最后再次核对
`GET /api/auth/me`。最多三个 GET，不轮询、自动重试、批量扇出或发出 POST。
选择/读取按钮不提交表单，也不消费密码或 TOTP。

页面须先等待原任务工作区的菜单和操作权限检查通过；读取另外要求现有的
任务详情权限与 `canArchiveView`。服务端对 `visibility=all` 要求
`tasks.readable`、`tasks.writeable` 和 `task_archive.readable`，并对每个 GET
重查当前身份和权限。观察不新增权限，也不把可读归档候选当成可读全部详情的授权。
归档/还原写操作仍额外要求 `task_archive.writeable` 及原有新鲜 proof、Origin/CSRF。

前后身份读取必须与当前挂载的账户、角色/权限和会话标记一致；强制改密、过期、
401 或已确认的会话变化先清除本次观察，再进入现有会话失效流程。
会话标记仅在内存中比较，不存储、记录或散列。该前后检查是围绕详情读取的
会话一致性观察，并非原子事务或任务响应内的服务端身份见证，也不承诺未来权限。

成功结果仅显示原目标 UUID、原预期可见性版本、当前“已归档/默认可见”、
当前可见性版本和浏览器观察完成时间（UTC）。该时间不是服务端提交时间。
页面始终说明：

“这里只核对该目标当前状态，不确认原操作是否提交。确认原操作仍须使用原幂等键、
相同内容和新的验证码。”

服务端没有只读回执 GET。当前版本即使恰好等于原预期版本加一，也可能由另一项
合法操作产生，不能据此认定原操作成功。只有既有的同 actor、原幂等键、相同完整
内容且带新鲜 proof 的显式 POST 重放，才会返回原不可变历史回执。
观察全部目标成功也不清除 pending、不生成新键、不修改原目标及版本。
原有经过验证的 mutation 成功分支仍负责清除 pending 和跳转首项任务。

读取期间绑定完整原意图，包括 action、key、before 是否存在及其值、reason、
所有原目标和版本。任一内容、目标选择、身份/会话、当前权限或 mutation 状态变化，
以及离开页面、取消或卸载，均使本次观察失效。中止传输不能阻止服务端已经处理的
请求，晚到结果即使忽略 AbortSignal 也不能显示。观察结果及时间不写入持久存储；
原待确认记录继续由既有按账户隔离的维护意图存储管理。

单次观察设 30 秒总截止时间，覆盖三阶段读取，用于避免无限等待；这是客户端
保护界限而非服务端 SLA。删除该界限会让挂起请求无限等待，副作用是慢但有效的
读取可能需要稍后再次手动尝试。不调整共享读取 hook 或其其他消费者的重试策略。

- 403：当前读取权限被拒绝，原操作结果仍未知。
- 404：当前不能读取该目标，不区分不存在、不可见或权限隐藏。
- 422 `result_too_large`：现有完整详情限制不允许读取，不截断或改读列表假装成功。
- 网络、5xx、畸形响应或超时：本次观察未确认，保留原操作并允许稍后手动重试。
- 错误 UUID、非健康任务/平台范围或前后身份核对失败：不展示任务派生字段。

错误提示不输出原始异常、请求 URL/headers/body、Cookie、CSRF 或 proof。

## 浏览器证据的精确边界

`web/e2e/maintenance.spec.ts` 在保留原周期计划、真实 Worker 健康结果、归档→还原、
历史幂等重放和只读角色拒绝断言的基础上，增加两个真实健康任务的未知归档核对。
其中原 producer 仍使用 10/10 次敏感操作；bootstrap 原有 6 次加真实健康提交、
两目标归档和同键重放各一次，共 9/10 次。新 context 不等于新额度，不重置计数、
跳过自然 TOTP 窗口或增加角色授权；保留测试 480 秒与 runner 540 秒界限。

故障夹具仅在 bootstrap 页的一次精确归档 POST 上，将原请求原样转发一次，关闭
转发重试和重定向。真实 API 返回 200 后，先校验有界完整 JSON 回执的原目标集合和
下一版本，才丢弃浏览器响应。它不伪造数据、改写 proof 或另行提交，也不声称浏览器
原生收到了该 200。未验证提交结果时停止后续写操作，不以未知结果授权重放。
故障 handler 在观察和显式重放之前移除，两者走真实未拦截的 UI 请求。

丢回复后通过真实存储刷新/重开原意图，分别读取原目标版本 2→3、0→1；始终保留
相同原键及完整输入。证据散列覆盖固定字段顺序的 action、idempotencyKey、before、
reason 和按 UUID 排序的完整原目标数组，另记录完整目标数组散列和数量。
排序只作用于证据副本，不改写原请求。散列不包含 proof/会话字段，不等于后端
`request_hash` 或提交证明。显式同键新 TOTP 重放须返回同一 operationUUID 和完整
历史回执，当前版本仍为 3/1，并核对原任务结果、事件和已知 schedule occurrence 未变。

安全证据为现有 test-results 下的封闭字段 JSON 及两个观察区域 PNG。PNG 在
1440×900 和 390×844 viewport 中截图，分别记录 viewport 和实际图片尺寸，
截图前核实 proof 输入为空且截图区域不包含 proof 表单。JSON 只含安全 UUID、
版本、完整意图/目标/任务/事件散列、固定类别请求计数、图片散列及实际执行 checkout
四个改动文件的 SHA-256，不包含原始响应、proof、URL/headers 或任意异常。

JSON 状态 `browser_passed_cleanup_not_attested` 仅表示浏览器用例断言完成。
它不能自证进程/容器清理、源码发布来源或整个数据库控制事件数量；仍须独立核对
实际精确 head CI、产物及两幅像素、完整来源 catalog 和 runner 清理日志。
并行分支失败先停止未来写操作、等待已启动分支/路由结束，再清理 context；
主要失败和独立清理失败均保留为失败，不把异常净化解释为通过。

此处记录实现与待运行断言，不宣称新增测试已经执行或通过；实际运行、像素、
清理和独立审查结果须随精确提交证据另行记录。

## 验证与仍未完成的范围

`internal/taskarchive/validation_test.go` 覆盖严格 UTC/微秒界限、输入、稳定目标顺序、
严格终态年龄与租约资格。带 integration 标签的独立 PostgreSQL 测试覆盖批量回滚、
并发版本胜者、归档/还原/旧操作重放、控制证据不可变、历史未知时间、
提交幂等与 schedule occurrence 保留，以及恢复 ancestry。
`internal/auth/archive*_test.go` 覆盖 HTTP 边界、双权限、新鲜 proof、事务回滚和 schema gate。

本地单元/编译检查与实际 PostgreSQL/浏览器运行分别记录；缺 PostgreSQL 时不能记为通过。
消息来源、投递与确认状态、重放窗口、分类保留期、合规/审计约束、物理删除、
容量目标和源系统真实链路仍待冻结，AD-F-193 和 Issue #15 不能据此关闭。
