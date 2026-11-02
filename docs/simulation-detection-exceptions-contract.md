# SIMULATED 检测例外持久化与结果解释契约

关联 Issue #43（AD-F-103–110）与 #253 的离线核心；本切片不关闭完整产品需求。
依据是用户授权建立的合成契约，不是源系统兼容结论。真实 AD/Windows、regex、
跨域全局、自动抑制、学习、风险/告警级联仍未实现，产品验收仍 0/209。

## 实际业务边界

新模块调用既有 detectionexceptions Add/Edit/Archive/Import/Export/Evaluate/
PreviewFromEvent。保存同一租户/域内规则级或 global 例外、不可变配置修订、幂等回执、
显式评估记录和事务审计。global 只表示同域全部规则，不是平台或跨域共享。
规则条件只支持既有手动检测 mapping v1 的五个字段：event.category、event.outcome、
actor.name、target.name、source.ip，以及 eq/in。未知字段、event.code、object.kind、
regex 等明确拒绝，不保存为永不命中的空壳配置。

配置读写是显式的新模块授权。条件与备注是用户确认提交的域内共享配置；提交后同域
例外读取者可以查看，包括通过事件预览后明确提交的条件。配置中只保存来源任务 ID、
执行规则 ID/摘要，不嵌入源任务 payload/result、事件列表或命中列表。预览本身不写配置。

原始任务与结果仍为原手动检测模块所有，新模块没有新 Worker、任务类型或执行器，
不改旧任务注册、ValidateSuccess、调度、租约、预算或结果。例外只在新的显式评估中
添加注释；完整原 matches 保持数量、顺序与内容。不是自动告警抑制或降低风险分。

## 真实来源绑定

预览与评估仅接受 sourceTaskId，不接受调用者上传 matches、事件字段、digest、
授权 epoch 或成功标志。既有 ReadRunTx/Engine.DetailTx 在当前事务检查真实身份、
租户/域、本人拥有的任务、当前授权 epoch、可见性与 schema；任务必须 succeeded。
同时要求当前 manual detection read、events read 及对应开关有效。

模块重新验证不可变模板版本/摘要、任务 payload/result 的全部 pin、数据集的
fixture hash/数量/合成来源，并按固定 mapping v1 重新编译、评估，逐项核对原结果。
规则绑定采用实际执行摘要，不采用规则目录发布摘要：目录 COPY 会将私有副本的
Enabled/Version 改为可执行模板值，两个摘要本来就可能不同。新版本不自动继承旧绑定。
只有在该来源任务实际命中的 RuleID 才能创建规则级绑定。从事件预览还必须精确匹配
该任务的 EventID/RuleID，输出禁用 Declaration；随后需要显式提交。

评估明细、评估列表及包含评估的回执重新校验当前源读取权限/开关/owner/epoch，
撤权或重新授权后的旧 epoch 不得从历史记录旁路访问原任务内容。已归档而不再由
原 ReadRunTx 可见的任务也不能通过本模块恢复读取。显式 404/403/400 映射为本模块
对应错误；数据库、上下文超时等基础设施错误不伪装为撤权。

## 操作与幂等

- create/import 只接受禁用、未归档的 Declaration，scope、摘要、状态和操作者由服务器决定。
- edit 创建新配置修订，并将该条目置为禁用；需显式 enable 才再次生效。
- disable/archive 是保留所有证据的安全操作；归档不可恢复，不进行物理删除。
- import 原子接受 Declaration 数组；未知字段、重复 ID、跨域/伪造源或任意非法项使整批失败。
- export 返回当前 Record 证据对象，不能直接作为导入格式；不会把历史、启用态或身份当可信输入。
- 所有写入绑定 expectedRevision 和完整规范化 Intent；同 actor/domain/operationId 同请求返回原回执，
  不同请求冲突。审计失败必须回滚全部业务写入。事务由调用方持有和提交，任意错误必须回滚。
- 配置历史按单个 revision 读取。初始空快照 revision1 可为虚拟；首次成功写入或评估才持久化。
- 评估使用服务器 evaluatedAt，效力区间为 [notBefore,expiresAt)。过期/编辑/停用/归档不改旧评估。
  读取旧评估以当时修订、时间重新核验注释，保留当时事实，不伪装为当前效力。

## 有界存储

同域最多 32 条配置，包含已归档条目；配置最多 256 修订；同域最多 64 条评估。
普通写入必须保留每个未归档条目一个 archive 修订，已启用条目另保留一个 disable 修订。
这些预算防止配置耗尽后无法安全停用/归档；重复 disable 不消耗名额。安全控制只能消费
保留预算，不能用于重新启用或编辑。删除配额会失去有界存储保证；低配额的副作用是
历史积累后拒绝新写入，需要另行授权设计生命周期，当前不会自动清理历史。

Record/评估上限 256 KiB，完整回执 512 KiB。序列化写前同时计算紧凑 JSON 与保守
JSONB 标点间距开销，整对象预检后再写业务/审计，禁止截断记录或只保留部分 matches。
读取也检查上限与内容摘要。配置快照有界全量返回；评估列表至多 64 条摘要，无无限分页。

## API 接线约定

默认关闭 ADTR_SIMULATION_DETECTION_EXCEPTIONS_ENABLED；独立默认拒绝
simulation_detection_exceptions 标记，平台管理员不获得默认业务绕过。
开关用于隔离这条合成功能，启用是访问路由的必要但不充分条件；还需当前域/租户授权。
关闭后路由不提供本功能；开关不授予任何权限，也不改变旧检测模块行为。

GET snapshot/history(revision)/export/receipt(operationId)/evaluation(id)/evaluations；
POST create/edit/enable/disable/archive/import/preview/evaluate。
路径前缀 /api/simulation/detection-exceptions。DTO 字段以新包 types.go 为准。
History 单修订；export 是 Record 对象；preview 返回 Declaration；写操作返回 Receipt。
所有返回明确 simulation 与 contract（Declaration 只是未保存输入，不冒充执行结果）。

## 本地证据状态

基线 detectionexceptions/simulationdetection 单测通过。新增后端单元 race、vet 和
integration-tag 编译通过。实际 PostgreSQL 尝试因缺 ADTR_TEST_DATABASE_URL 明确失败，
没有 skip，也不能由编译替代真实运行。后端四个具名 PostgreSQL 测试覆盖真实 Worker
非空原命中、历史注释与撤权、事务/CAS/配额、修订安全控制、源结果伪造及超界零写入。
最终独立复审、专用真实 PG/API/浏览器、当前 head CI 与合并后验证仍为独立门禁。
