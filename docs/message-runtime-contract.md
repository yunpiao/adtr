# F40 health 消息运行时首片

Refs [#101](https://github.com/yunpiao/adtr/issues/101)、[#50](https://github.com/yunpiao/adtr/issues/50)，AD-F-157/158/159。基线 main `f9b8ed1254c0ac0fd9183c0f0625995bec975218`；迁移17。`messagecore` 保持原样，作为查询与已读转换 oracle。

## 真实来源与边界

仅投影真实 `infrastructure.health` 的 canonical 终态 outbox/event/task 联接。接收者取原始 `tasks.actor_id`；操作事件的 actor（例如取消者）不改变收件人。仅 `finished`、`cancel_requested`、`authorization_rejected`、`attempts_exhausted`、`lease_recovered`、`kind_rejected` 的真实终态参与，排除 `completed-with-cancel-race` 的额外记录。source event 唯一，且 tenant/task/result_version/recipient 唯一；重放或并发不能制造重复消息。

类型为 `task_result`，子类型为 `succeeded` / `failed` / `partial_failed` / `dead_letter` / `cancelled`。平台范围显式标识，消息 domainId 为空。正文只用静态安全模板，不复制 task payload/result/cursor/error_code 或敏感证据。发生时间来自事件；入库时间是首次投影时间。任务归档不隐藏或删除消息。

不修改、删除 outbox。投影采用每次至多200条原始候选的循环扫描，再对候选做来源筛选与 anti-join 去重；每轮固定当时可见的最大outbox ID作为上界，扫描至该上界后回绕。持续新增的高ID不会无限延长当前轮，让晚提交低ID有下一轮被补投影的机会。游标不是永久 high-water mark，晚提交的低ID会在下一轮被发现。游标与消息同事务提交，失败不推进。历史量大时回绕延迟会增长；该机制不构成吞吐、P95或容量承诺。

## API 与实时权限

- POST `/api/messages/query`：确认查询；JSON `types`、`subTypes:[{type,subType}]`、`keyword`、`start`、`end`、`readFilter`、`sort`、`offset`、`limit`。最后四项必须显式提供；时间可同时省略，冻结为确认前168小时的半开区间
- GET `/api/messages/page?resultId=…&offset=…&limit=…`：固定结果分页
- GET `/api/messages/locate?resultId=…&messageId=…&limit=…`：同一固定成员定位
- POST `/api/messages/read`：JSON只接受 `resultId`、`messageId`、`operationId` 三个标识，原子标记单条本人消息
- GET `/api/messages/read-operation?operationId=…`：查本人历史成功收据

所有接口要求当前有效身份与 `tasks.readable`，本人已读不要求 tasks.writeable、密码证明或MFA。两种POST必须精确Origin、JSON与会话CSRF。无客户端租户/用户/权限版本/时间覆盖，未知字段、重复JSON键、重复参数、批量/all、数组标记、非法分页均拒绝。任务自身写权限不变。

时间筛选保留请求原始RFC3339精度。PostgreSQL事件时间为微秒精度；SQL候选边界仅向上取整到微秒，避免驱动向下截断纳秒End而漏掉应包含的事件，核心仍使用原半开边界校验。

查询结果绑定租户、用户、会话token的摘要（不存原cookie）、authorization_version与10分钟期限。分页、定位及新标记前实时鉴权并核对绑定；撤权再授予仍使旧epoch结果失效。新会话不能继续旧结果。查询时完整基础候选、阅读快照、成员顺序与计数持久化；翻页不重新筛选，标记已读不修改旧结果的成员、序号与计数。刷新查询才显示新阅读状态。

## 幂等收据与未知提交

已读首次server时间与收据在同一事务提交。相同 operationId 与同一目标原样返回第一次收据和首次已读时间，不能将首次 NewlyRead=1 改为 AlreadyRead=1；同ID不同目标409拒绝。不同operationId再次读同一消息保留首次时间并报告 AlreadyRead=1。

提交超时/连接断开时，客户端先查询同一operationId；若无收据再使用原ID与原目标重放。已提交收据查询/重放在当前身份与权限核验后返回，不受原快照到期、清理或旧session绑定阻挡；同用户新有效会话可以确认历史结果。旧cookie认证失败。不同租户/用户不可读取他人收据或结果。取消在提交前回滚，提交后不能撤销已完成标记；本片无批量取消调度。

## 保护预算及副作用

下列值为首片固定正数保护策略，必须由服务端应用；删除这些保护会失去单请求内存、查询扫描、活动派生记录或后台循环的界限：

- 页长1..200；offset非负，超界offset返回空页；不支持-1
- 时间范围内本人来源至多1000候选，读取1001用于超限检测；先按来源与时间索引限候选，再做类型/标题/阅读筛选。超限整次 `query_scan_budget_exceeded`，需要缩小时间范围，不能用稀有关键词绕过候选预算
- 核心逻辑输入预算2MiB，超限整次拒绝；这是确定性逻辑字节计量，不是JSON大小或堆峰值。持久快照JSON另限4MiB，防止序列化膨胀与损坏记录的无界读取；核心预算通过也不能绕过此存储预算
- POST请求体最多32KiB，GET查询串最多1KiB，单个结果/消息/操作标识最多128字节且只接受ASCII字母、数字、连字符或下划线；超过这些解析保护值明确拒绝。删除保护会允许无界输入或含控制字符的标识，副作用是超长查询必须缩短
- 固定结果10分钟；每用户活动8个、每租户128个、全局1024个，事务级配额串行核验。超限拒绝新查询；并非静默替换旧结果
- 投影每批200候选、轮询1秒、每次DB上下文5秒；普通失败后继续下轮，不提交半批数据
- 到期派生结果每次至多清理200个，使用到期索引与锁；清理不删除消息、首次已读或历史收据

这些值在对应功能启用时必需；副作用是大范围查询明确被拒绝、频繁查询需要等待结果过期、长历史晚提交事件可能延迟到回绕时显示。未来扩大值须实测与审查，不表示本片最大产品容量。

## 验证与剩余范围

验证逐项记录于工程Issue/PR：基线、格式/静态/race/构建、SQL与纯模块oracle、撤权/跨用户/租户/会话、固定阅读快照/首次时间/原收据、敏感哨兵、真实health Worker→outbox→投影→HTTP→PostgreSQL、迁移升级与精确head CI。未运行不得算通过。

后端首片不代表UI已交付或完整F40验收。完整时间线/分组、旧类型映射/appName、其他真实生产者、批量跨页进度/取消、完整浏览器交互与容量仍开放。#50继续开放，产品验收0/209，无生产部署。

## 本地审查记录（2026-10-11）

独立实际diff复核确认了会话/权限/原收据恢复边界。审查以实际pgx codec复现纳秒End截断遗漏，已修SQL候选边界ceil微秒并补纯单元/SQL回归；另修循环扫描持续新增可能饿死晚提交低ID的缺口，加入每轮固定上界与持续追加回归。补齐同operationId16并发原结果、receipt INSERT失败全事务回滚、提交后响应写失败恢复、后台循环普通失败自动重试。自审与独立审查分别执行；最终精确提交CI仍须实际完成。
