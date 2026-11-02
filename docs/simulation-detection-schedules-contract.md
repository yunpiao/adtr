# SIM-SCHEDULE-001：有界合成定时检测

关联已有 Issue #41（AD-F-079/080）及 #15（AD-F-190）。本契约的未冻结语义按用户“缺少依据先模拟一个依据、继续可开发线”的授权明确选定；不是原产品行为或真实 AD 证据。产品验收保持 **0/209**。

## 范围及独立授权

只在现有已持久化的合成域、合成数据集和已编译的合成检测模板上计算。一个计划对应一个域/模板/数据集；多个计划分别显示结果，不能把局部成功视为整批成功。无 AD、网络扫描、凭据、部署或远程执行。

独立 `simulation.detection.scheduled` kind，独立 `simulation_detection_schedules` 权限默认拒绝，同时要求已有 `simulation_detection` 权限和当前域授权。管理员、健康计划权限或手动检测写权限不自动授予定时检测能力。新增开关默认关闭，不改变生产环境或授予角色权限。

通用任务注册器禁止可调度 kind 使用最终结果校验，所以该新 kind **保留 unschedulable**，不修改这一护栏。模块使用原 `SubmitTx` 与自己的 occurrence-intent 同事务提交，唯一键 `(tenant_id,schedule_id,scheduled_at)` 跨修订保持唯一；没有伪造通用 `task_schedule_occurrences` 记录。执行和最终结果发布必须匹配此 intent 的租户、域、actor、计划、修订、时间、task ID 与规范化 payload hash；直接手动提交新 kind 无有效意图将失败。

## 创建、控制及恢复

服务端产生 schedule ID 和 UTC 秒精度 anchor。定义为 label（1–80字符，无控制字符）、cycle（0每日、1每周、2每月）、templateId、templateVersion、datasetId。首次周期从 anchor 的下一期开始，创建后暂停。

编辑需要 expectedControlVersion 和 operationId，产生新的不可变 revision、重设 anchor/下一期并暂停；不会自动启用。启用、暂停、逻辑归档均 CAS，操作回执/事件/审计与状态同事务。归档不是永久删除。回执恢复返回不可变历史回执及当前状态，不能把旧启用回执当作现在仍已启用。相同幂等键不同请求返回冲突。

暂停/归档只停止未来准入，不声称已经取消正在运行的任务；本页面不新增取消 API。授权撤回使旧计划永久 authorization_blocked，即使恢复权限也不会自动重新启用；需要新计划。模板不再有效使 configuration_blocked，可通过显式编辑产生新修订，再显式启用。

## 周期、错过及互斥

所有运算使用 UTC，不提供本轮需求外的固定执行时刻/时区配置。每日/每周保持 UTC 时分秒；每月基于**原 anchor 日**取目标月最后有效日期，例如 1月31→2月28/29→3月31，不累计漂移。年份限定1–9999，溢出阻断并记录原因。

错过周期记录 skipped_misfire 范围，不回补队列。只有最近一期处于 **60秒 lateness** 窗口内才可准入；这是容忍轮询抖动的必要边界，不是业务超时/吞吐参数，删除它会允许旧任务在任意久后启动。启用时过去的暂停周期记 skipped_paused，等待下一期。

同一稳定计划所有修订中存在 queued/running/retry_wait/cancel_requested 任务时，记录 skipped_overlap 并推进游标，不积压。两个 scheduler 竞争由身份锁和计划行锁串行化，任务、意图、事件与游标原子提交。事务失败回滚后可安全重试；业务任务 MaxAttempts=1（零次业务自动重试），下一周期是新 occurrence。

kind 的 lease30秒、heartbeat5秒、timeout10秒与现有手动合成检测一致；不扩大任何旧任务预算或冻结验证边界。

## 版本、授权及发布

创建/编辑由服务器钉住模板 ID/version/hash、数据集 ID/fixture hash、mapping version。准入、执行、最终发布复核当前模板未归档且版本/hash与钉住值一致；不会静默升级模板，也不会把过期模板继续视为有效。模板更新导致旧计划 configuration_blocked，排队任务明确失败。

API、scheduler、任务执行/最终发布使用当前权限和 actor epoch。数据库或上下文临时错误不能被永久标记为撤权。最终发布仍使用原任务引擎 fencing、租约、取消与结果验证。既有任务引擎在撤权终结时标记 failed/authorization_revoked，数据库内部可能保留计算结果，并不承诺擦除；OwnerScoped 读取拒绝旧 epoch 结果，计划历史不携带结果，不能计为成功。模板过期的最终校验分支会阻止成功并清空待发布结果。

## 限额与未覆盖项

每 actor/域最多100个计划（包括归档，防止归档规避上限）；最多1024修订，分页1–50、页码至10000；每次 tick 最多32候选，每个准入操作的 context 为5秒；回滚和连接关闭各有独立2秒清理 context，因此5秒不是整个 Tick 或关闭的总墙钟上限。它们是本合成切片的明确保护，不是原产品容量承诺；达到上限返回显式错误，不进行永久历史清理。历史保留无隐式删除。

原 Rate 是每秒包速率，纯内存合成计算没有发送网络包，因此不接受或显示一个无效 Rate 控件，也不声称兑现速率需求。真实 AD每日/每周/每月、数据包限速、多域批次、Windows矩阵、真实算法及目标容量仍未产品验收。

## 迁移及验证

schema27 新增独立计划/修订/操作/事件/意图表，不扩展健康计划的 kind/domain/payload CHECK 或手动检测 receipt guard。不自动启动 DDL，不插入权限授予。迁移版本与共享接线由集成人员管理。

新增单元测试覆盖日/周/月/月末/闰年、溢出、定义和payload输入、伪造意图、最终重授权。真实 PostgreSQL 测试定义覆盖 CRUD/CAS竞争、回执当前真相、审计回滚、双scheduler唯一准入、实际 Worker合成结果、错过周期、跨修订重叠、撤权、模板归档、伪造任务意图。缺少 ADTR_TEST_DATABASE_URL 会失败而非跳过。仅编译测试不等于已运行 PostgreSQL；发布记录必须给出实际精确提交 CI和合并后验证。
