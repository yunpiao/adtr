# 数据与任务契约（G02）

关联 #3；设计状态，尚无持久化调度实现。

任务记录包含 task_id、tenant_id、domain_id、kind、payload_version、payload、
actor_id、authorization_version、idempotency_key、state、attempt、max_attempts、
lease_owner、lease_until、fencing_token、cursor、result_version、created_at、updated_at。
业务时间一律 UTC，API 使用 RFC3339，持续时间显式带单位；工作簿的展示时区和字段仍待 G01。
敏感凭据只存引用，不入 payload；任务数据的字段白名单按 kind/version 冻结。

## 提交与幂等

事务创建任务、审计与 outbox。幂等键作用域为 tenant/domain/kind；
相同键和相同规范化 payload 返回既有任务，相同键不同 payload 返回冲突。
幂等记录保留时间必须覆盖重投递窗口，实际期限由容量/保留期要求确定，未知不得擅自清理。
调度器竞争唯一 schedule occurrence，队列通知只是唤醒，数据库状态为准。

## 状态与租约

合法路径：queued → running → succeeded；running → retry_wait → queued；
running → partial_failed/failed/dead_letter；queued/retry_wait → cancelled；
running → cancel_requested → cancelled（确认子进程停止后），或 completed-with-cancel-race 事件后记录真实终态。
`completed-with-cancel-race` 是审计事件，不是额外成功状态；取消不能撤销已发生的 AD 副作用。
partial_failed 保留逐对象成功/失败/不确定结果，只允许明确选择失败对象重新提交新任务。
dead_letter 恢复创建有父任务 ID 的新任务并重授权，不重置旧任务历史。

领取在数据库事务中使用行锁与条件更新，递增 fencing_token；续租和所有结果/游标更新
要求同一 owner/token、未过期租约及预期状态/版本。旧 Worker 的回传必须被拒绝。
租约时长、心跳、外部超时和资源上限由任务种类配置，必须为正数且有上下界。
取消以持久化标记传播；执行器定期检查并取消 context，受管子进程先终止再强制杀进程组，
记录终止结果。执行器失联不直接宣称取消成功，进入恢复核对流程。

## 重投递与业务重试

消息重投递不消耗业务 attempt；只有已持久化进入一次实际执行才增加 attempt。
`max_attempts` 含首次执行（1 表示零次重试，2 表示一次重试，5 表示总共五次）。
不可将参考实现中 retry=1 与 attempts=1 混为一谈。

| 种类/错误 | 当前决策 |
| --- | --- |
| 凭据/证书/授权/输入错误 | 不自动重试；终态 failed，修复后显式新提交 |
| AD 写、阻断、恢复、远程执行 | 默认禁止；获得后续授权后仍不自动重试不确定副作用，先核对 |
| 只读同步、查询、规则执行、报告、通知 | 逐业务 Issue 冻结；未登记 max_attempts 的类型拒绝注册，不猜统一次数 |
| transient network/database | 仅任务类型明确允许且副作用幂等才可指数退避；次数/间隔/抖动上限待该类型契约 |

每个任务种类注册表必须记录输入 schema、授权动作、最大 attempts、可重试错误、
超时、取消语义、幂等副作用策略、恢复策略、契约测试位置。尚无已冻结业务种类。
分页游标与本批结果原子提交；重复批次按源事件标识去重，不能用接收时间假冒源事件身份。

## 集成测试计划

用真实 PostgreSQL 测试竞争领取、唯一调度、提交审计原子性、进程崩溃恢复与租约 fencing。
分别测试重复唤醒不增加 attempt、业务重试确实增加 attempt、超限进入 dead_letter。
覆盖取消前/执行中/副作用后、部分失败重放、撤权后 Worker 拒绝执行、旧结果覆盖新状态失败。
测试分层：单元状态机；数据库契约；本地进程生命周期；授权 AD 实验室。每层单独报告，
mock 与本地数据库通过不能代替 Windows/AD 端到端验收。

## 执行结束后的受限确认（B2）

只有单次、不可调度的任务类型能注册 `OnQuiesced`。引擎仅在 `Execute` 返回、panic 恢复与所有 defer 完成后构造不可变 `QuiescedAttempt`；先保留确认，再执行仍有效的普通 Finish，随后用独立、精确版本门禁事务调用确认钩子。钩子不能延长租约、复活任务或重复执行任务。取消请求、超时、终态和回收本身都不是结束证明。

确认失败保留在进程内，每个 owner 最多一个待确认；活动和待确认 owner 合计最多32个。相同 owner 并发执行被拒绝，重试轮换起点避免某个慢确认饿死后续确认。确认成功前该 owner 不能领取下一项；提交响应丢失按相同不可变身份幂等重试。格式化和 JSON 均隐藏内部 owner/fence。

Worker 停止领取后等待全部执行器返回，再以10秒总预算确认剩余记录；不会靠超时把尚未结束的执行器认作已停止。进程退出仍未确认的使用保留数据库依赖；重启不能重建此前见证。F01 的已登记账户消费者另有独立的“未打开且已终态”预留清理，运行于 Claim/RecoverExpired 事务之外，不能释放 opened 使用。
