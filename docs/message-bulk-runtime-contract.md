# F40 有界批量个人已读运行时

关联 #112、#50 / AD-F-158 / DTL-036。仅实际 infrastructure.health 来源消息；不是完整 F40 或产品验收。保留 messagecore、单条 read/receipt 和固定查询快照语义。

## 输入与恢复

POST `/api/messages/bulk-read` 接受 resultId、operationId、selection（selected/all）；selected 必须有非空、唯一且最多1000个 messageIds，all 禁止 messageIds。所有成员必须来自当前有效固定结果的列表成员（含阅读筛选），不能对全库执行 all。冻结时保持结果顺序；快照失效整次拒绝，无部分接收。

operationId 在租户/用户内绑定规范请求（resultId、模式和排序后的选择ID），重放返回原始接受收据及当前进度；同ID不同请求409。接受收据固定 acceptedAt/target，不把重放变成新操作。接受后目标与逐项原收据持久化，无快照外键；10分钟快照到期/清理、logout、新会话不丢已接受目标或结果。用户必须先恢复有效身份和当前 tasks.readable 才可查询或重放。新会话不得用旧快照创建新操作。

GET `/api/messages/bulk-read-operation?operationId=…` 返回当前进度。POST `/api/messages/bulk-read-cancel` 仅接受 operationId；事务串行取消尚未提交余项，保留已完成首次时间与原逐项收据；取消重放原终态，完成后取消仍为 completed。

所有 API 使用服务端身份与当前权限；只有 POST 要求精确Origin/JSON/CSRF与已有32KiB请求保护，不接受客户端租户/授权版本/时间。单个operation的目标详情有界，当前接口只返回进度而不返回整批消息正文。

## 状态和事务

状态 queued（已接受尚未处理）、running（至少一批已提交且仍有余项）、completed、cancelled、authorization_blocked。终态不可自动恢复。Target=Processed+Remaining；Processed=NewlyRead+AlreadyRead；Failed=0，因为DB错误使整批回滚，未完成仍是Remaining，不伪装逐项业务失败。cancelled/authorization_blocked 可以有Remaining，Remaining表示永不由该操作继续处理的冻结余项，不表示后台仍排队。

每批先锁 schema，再实时锁租户/actor，复核账户有效性、tasks.readable与冻结authorization_version，最后锁operation。后台复用真实任务Read授权器，不存cookie/密码/MFA，不要求会话永久存活。任何epoch变化即终态authorization_blocked；临时SQL故障回滚后重试，不当成撤权。锁持续至提交，撤权不能插入已核验批次与其提交之间。

每批最多50条，首次已读insert-on-conflict、不可变逐项收据与进度一起提交。崩溃/取消连接提交前全部回滚，提交后数据库进度恢复，无外部副作用和租约外工作。并发进程及cancel使用相同operation行锁，单条read并发使用message_reads唯一键保留首个提交时间。

## 必需保护与副作用

- 冻结目标最多1000，继承固定结果候选限制；selected同时受32KiB请求体字节保护，1000项条数上限不承诺任意128字节ID均能一次提交；超限整次拒绝，all不重新查询
- 活动操作每用户4、租户32、全局256，准入全局事务锁与部分索引有界计数；超限429，需要等待活动操作终止
- 每次Worker轮询先在独立事务持久推进队列游标（按递增queue_id循环，有活动部分索引），提交后才锁身份与operation并处理至多50条；处理失败/超时不回滚调度游标，因此下一轮不会反复卡住同一项，1秒轮询、5秒DB事务预算；副作用是排队等待，不能据此承诺容量/P95
- 历史operation、targets、receipts不自动清理；保留未知提交恢复，长期存储保留策略须另行冻结，活动预算不代表历史存储容量上限

这些固定保护在启用时必需。删除它们会使每次请求、事务写入、活动目标或Worker循环无界；扩大值须重测。无需新增配置参数、真实凭据、安全访问或部署。

## 验证门禁

需分别记录实际健康Worker→outbox投影→冻结→批处理、跨页all、非法目标、并发重放、单条并发、cancel竞态、逐批撤权、快照清理、新会话恢复、真实PG断连/重启、原首次时间/逐项收据、计数守恒、租户隔离、预算及迁移17→18。单元/编译不代替真实PG；UI后续独立切片，#50保持开放、产品验收0/209。

## 本地检查点（2026-10-11）

关联工程 [#112](https://github.com/yunpiao/adtr/issues/112)。本地基线及新增模块 Go race、HTTP解析、全仓 integration 标签编译通过；完整 make check 通过1470项前端测试、Go race/vet/build、104项Python契约及依赖审计0漏洞。真实PG尝试因缺少ADTR_TEST_DATABASE_URL在初始化失败，不算运行通过；精确head CI仍待执行。

独立设计审查要求避免坏项/长锁饿死及持续新增高ID阻止回绕，已改为独立提交cursor、固定upper轮转；业务批次事务失败不回滚调度位置，崩溃跳过的本轮候选在下一轮恢复。独立实际diff审查补充非空17消息历史升级证据，测试已用真实health→投影→查询→单条原收据构建历史再迁18完整比对。

真实数据库测试代码分别覆盖：pg_terminate_backend中批事务回滚；重建Store及RunBulk循环恢复；另在生命周期脚本以直接SQL明确标记的51目标合成夹具，观察真实Worker提交前50、下一批进入PG睡眠后SIGKILL PostgreSQL和Worker容器，重启后核对已提交回执和首次时间不变、未提交第51回滚，再由重启Worker完成。合成生命周期夹具不声称通过HTTP冻结，也不代替Go实际health/API链路。以上PG、容器故障场景尚待CI实际运行后逐项记录。
