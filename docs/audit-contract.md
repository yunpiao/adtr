# F39 平台操作审计：本地实现契约

关联 Issue #13，AD-F-153–156，FLD-2478–2537，DTL-022，EXP-014。本契约冻结当前实现；不把参考源码的线索或阈值称为原系统 1:1 验收。审计表由 schema 6 引入；运行服务精确校验共享的当前应用版本；显式 migrator 执行迁移，API/worker 不在运行中补建表。

## 身份与记录来源

所有路由使用现有 Cookie 身份；写请求还需要同源 Origin、JSON、CSRF、当前密码和未使用的六位 TOTP。actor、tenant、角色、资源范围与授权 epoch 均从数据库确认，客户端不能指定。平台管理员也不能绕过域授权。查询/计数/分页/类型字典/快照/隐藏目标解析都在 SQL 内先应用 tenant 与当前 F47 域范围。

稳定 ID 是 auth.N、resource.N、task.N、audit.N、domain.N，N 为正 bigint。来源分别为 auth_audit、resource_audit、task_events 与新增的 audit_events 控制账本；task_outbox 不重复计入。当前资源管理同时产生 auth/resource 事件，保留各自真实身份，不猜测去重。源表及控制账本继续由 UPDATE/DELETE/TRUNCATE 拒绝触发器保护。

新增事件在同一事务内，通过服务端设置的 transaction-local GUC 捕获用户名、直接网络对端 IP、匹配的静态请求路径、服务端随机 request ID。忽略转发 IP 头；不记录原始请求体、Cookie、密码、TOTP、令牌、密钥或任意查询串。eventArgs 只包含允许的目标、状态、原因与这些请求元数据。role_create/update/delete/permissions_update 的历史 :ID 形式规范化为稳定事件名，目标保留为 roleId。

迁移前未记录的 loginUser/sourceIp/path/requestId 保留 NULL，availability 明确表示缺失；不联结当前用户名伪造历史。已登记 auth/resource 成功动作标记 SUCCESS，*_denied 标记 FAIL；未知动作标记 NONE 且 availability.eventResult=false。任务结果同时依据已登记事件动作与真实状态：succeeded=SUCCESS，failed/partial_failed/dead_letter=FAIL，其余合法状态=NONE，状态保留在参数内。

## 路由与字段

- GET /api/audit：返回 {page:{pageIdx,pageSize,total,totalPage},List:[],exhausted}
- GET /api/audit/types：返回 {events:[],List:[{logType,logTypeName,eventNameList:[]}]}。九类固定类型名称，事件候选来自当前授权可见记录；超过 1000 个类型/事件对返回 422 metadata_too_large
- GET /api/audit/columns：返回 {columns:[{prop,label}],defaultColumns:[],maxColumns:8}
- POST /api/audit/delete 与 /restore：{id:[...],reason,actorPassword,totpCode}；成功返回 {result:"success",changed,visibilityRevision}
- POST /api/audit/exports：筛选字段、selectColumn、idempotencyKey、actorPassword、totpCode；返回 {taskUUID,task,replayed}
- GET /api/audit/exports/detail?taskUUID=...：返回 {task,downloadReady,rowCount?,snapshotAt?,downloadPath?}
- GET /api/audit/exports/download?taskUUID=...：真实 XLSX 附件；固定服务端文件名、正确 MIME、no-store、nosniff，不返回内部路径

List 行包含 ID、loginUser、sourceIp、event、eventArgs（安全 JSON 文本）、eventResult、CreateTm（UTC RFC3339）、logType、logTypeName、userId。附加 source、domainId、availability、deleted、deletable、visibilityVersion；未知用户/IP 为 JSON null。audit.* 与 domain.* 控制账本 deletable=false。

未知路由 404，方法不符 405，未知字段/重复标量/重复 JSON key/null/非法 UTF-8/控制字符及越界输入 400。无认证 401，无权限 403；不同租户、创建者或无权域目标为 404。数据库错误不变成空结果或成功。不可用/不兼容 schema 在认证之前返回 503，且不尝试不兼容审计写入。

## 查询语义

pageIdx 缺省 1，范围 1–1000000；pageSize 缺省 20，范围 1–100。-1 只允许 pageIdx=1 且匹配总量不超过 1000，否则 422 result_too_large。显式 0 不表示缺省。createSort 缺省 -1，只允许 1/-1；按 occurred_at、source 的 C 排序、数值 source_id 同方向排序，计数及页面来自同一 SQL 快照，JSON 聚合显式排序。

startTm/endTm 使用带时区 RFC3339，规范化 UTC；开始包含、结束不包含，两者同时提供必须 start<end。未强加没有证据的最长时间窗。keyword 最多 50 Unicode 字符，对历史 loginUser/sourceIp 作不区分大小写的字面子串匹配，%/_/反斜线不具有通配意义，缺失元数据不补值。

filterEvent/logTypeList 通过重复 URL query key 表达，数组内部 OR，不同字段之间 AND。事件最多 100 个不同字符串，每个 1–128 Unicode 字符且无控制字符；不存在的合法事件返回空。类型仅 1–9 且不得重复。visibility 为 visible（缺省）、hidden、all；hidden/all 额外需要 audit.writeable。

查询/types/columns 需要 audit.readable；隐藏/恢复需要 audit.writeable。导出提交需要 audit.readable、audit_exports.writeable、tasks.writeable；导出详情/下载需要 audit.readable+audit_exports.readable，且只能当前创建者读取。工作线程每个动作重检 tasks.writeable、audit.readable、audit_exports.writeable 与冻结的 epoch。角色撤权后再授予不能恢复旧任务或旧下载。

## 可恢复清理

每次 1–100 个不同的合法源 ID，原因去除首尾空白后非空、最多 500 字符且无控制字符。所有目标先按授权解析；任一不存在或无权目标使整个请求 404 且不修改其余目标。源数据不变，只更新单独可见性状态；状态、逐目标控制账本和 TOTP 消耗同事务提交。全部目标已处于请求状态返回 409 visibility_unchanged。控制账本不能隐藏，返回 409 protected_audit_event。任何账本写失败都回滚状态和证明消耗。每次实际变动推进租户 visibility revision。

此处 delete 指可恢复隐藏，不是物理删除或保留期清除；没有静默清理历史证据/快照/产物的后台任务。

## 异步导出与发布

明确八列，默认全选，提交必须选 1–8 个不重复列，按选择顺序输出：userId、loginUser、sourceIp、logTypeName、event、eventArgs、eventResult、CreateTm。空列/未知列/重复列/>8 返回 400 invalid_columns；未提供必填列字段返回 invalid_input。空结果是有表头、零数据行的有效工作簿，与没有选择列分开。所有单元格以 inline string 编码，公式开头也是文本，不包含宏、外部链接或用户提供路径。

导出只允许 visible 记录。请求允许 startTm/endTm/keyword/filterEvent/createSort/logTypeList，拒绝 visibility/pageIdx/pageSize 等额外字段；隐藏视图必须先切换到可见记录才能导出。幂等键作用域为 tenant/platform/audit.export；相同创建者、授权 epoch、规范化筛选与列复用任务，冲突返回 409。

注册任务 audit.export v1，30 秒租约、5 秒心跳、30 分钟总超时，最多 3 次 attempt。只有明确可安全重试的数据库错误及受管 worker 中断可重试；输入、权限、容量、内容失败不重试。重新提交使用新幂等键。通用任务接口可显示脱敏控制状态并取消，通用 submit/recover 不创建此专用任务；只有专用 /exports 创建并记录受保护提交账本。

第一次成功的 fenced execution 在一个数据库语句内冻结已授权、已过滤、已稳定排序的安全行与原始域来源，而不是在提交时读取最终数据。读取 100001 条作为超限哨兵，超过 100000 使整个快照回滚，不截断、不发布；快照跨重试不可变。每批最多读取 10000 行。基于此快照流式写真实 XLSX，最多 1 MiB 一个数据库 bytea chunk，总产物硬上限 128 MiB；超限返回 artifact_too_large。单元格不能超出 XLSX 的 32767 UTF-16 单位，无法编码的数据使任务明确失败。

所有 worker 数据库操作通过 tasks.Execution.WithTx：schema→当前身份/epoch→owner/token/租约/state/resultVersion 检查及操作在同一事务，回调前后均检验，5 秒操作上下文覆盖每次查询。产物以 (task_id,fencing_token,chunk_no) 存储，重试比对内容而不覆盖；manifest 按 task+token 固定字节数、行数、SHA-256。只在任务 succeeded 且 result 中的 artifactToken 精确匹配完整 manifest 时下载；旧 worker、取消或撤权不能发布。

每个 snapshot 域在导出操作前后、详情及下载时按当前真实时钟再次授权；许可证随时间失效，即使没有 epoch 写入，也不能继续读取。下载另外核验当前创建者/epoch/权限、visibility revision、每块序号/摘要和完整摘要；先验证后发送，128 MiB 上限约束下载内存。隐藏/恢复改变 snapshot 的 visibility revision 后返回 409 export_snapshot_changed，要求重新导出。失败、取消或尚未完成返回 409 export_not_ready；已标记成功但缺失快照/manifest 或元数据不一致返回 500 artifact_invalid。

## 验证与原始证据边界

单元测试：严格字段/重复值/空列/非法排序、字面关键词、Unicode 边界、列顺序、稳定 ID、许可分离、受保护取消策略。真实 PostgreSQL 集成测试：历史 NULL、请求元数据安全性、跨租户/域筛选、混合目标原子性、账本不可变与证明回滚、10001 行真实 XLSX 首末行和批次、空结果、100001 超限回滚、创建者和授权恢复边界、可见性变化、无 epoch 变动的时间到期、重试保持原快照、旧 fencing token 拒绝回调、完整 manifest 在 Finish 前被取消也不能发布。任务引擎另测 stale lease/version、回调回滚及取消后发布阻断。浏览器场景位于 web/e2e/audit.spec.ts。

本地环境没有 PostgreSQL/Docker 时，只能报告单元/race/编译通过；真实 PostgreSQL、浏览器/worker 完整链路及 CI 必须另记实际运行结果，不算跳过通过。100000/10000 来自参考阈值；10001 分批链路不是 100000 规模容量验收。128 MiB 是本实现安全预算，不是性能承诺。

尚未宣称复刻的来源歧义：FLD-2478 token、2479 ip、2480 path、2481 args 改为可信服务端输入，不接受浏览器断言；2482 枚举与 2504 文本结果采用本地明确映射；2506 字段名 literally string、整数含义不明，未发明输出。旧时间格式/时区/边界、pageSize=-1 容量未获源系统完整证据。九项字典但八项可选列的第九项未确认；旧空列空工作簿行为不视为正确结果。FLD-2522 路径替换为受鉴权下载 URL，2524 fileName 由服务端固定，2527–2529 userInfo 来自身份服务，2530–2534 内部别名由规范筛选取代。原物理删除/留存语义未确认，本实现只做可恢复隐藏。未进行真实 AD 或 Windows 联动验收，没有外部通知或生产部署。

## F01 域连接审计来源

schema 9 在域连接表创建后安装 domain_audit 投影。配置变更及诊断提交记录同事务元数据；worker观察缺失的请求字段保留NULL。所有非平台域记录均先检查当前精确域范围，再计算计数、类型、分页和导出；导出下载按全部域来源重新检查自然到期，不依赖epoch变化。domain_test_result 的 observationCode 仅表示已保存观察，eventResult 在关联任务终态之前为NONE；成功只在任务succeeded后显示。控制记录不可隐藏。

## F03 操作账户来源

schema10 增加 operation_account.N 控制记录；只记录服务器身份、域/账户ID、版本与安全结果码，不记录标签、用户名、密码或密文。该来源不可隐藏，并在查询/计数/类型/导出和自然到期下载时重新验证当前域范围。SUCCESS 表示本地登记事务成功，不表示远端账户已验证。
