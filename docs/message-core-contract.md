# F40 消息纯领域切片契约

关联 [Issue #50](https://github.com/yunpiao/adtr/issues/50) 的 AD-F-157/158/159。
以[主开发确认后的修订契约](https://github.com/yunpiao/adtr/issues/50#issuecomment-6051076113)
为准，覆盖[最初提案](https://github.com/yunpiao/adtr/issues/50#issuecomment-6050794296)中的不同默认语义。
基线为 PR #72 `e437919967074bc544cf378d72eab51c37f351cc`。
交付仅包含 `internal/messagecore/**` 与本文档。

这是有限、标准库依赖的纯函数切片，不是整项 F40 验收。没有数据库、HTTP、页面、通知发送、
真实消息来源、共享鉴权、迁移、App 接线或持久化批处理。不会调用 `time.Now`、访问外部服务、
操作真实 AD 或使用真实凭据。旧数字枚举映射、timeline 分组、group count 和 appName 语义仍未定义。

## 输入与显式身份

入口 `NewResult(Request) (*Result, error)` 接收：

- `Scope{TenantID, UserID, DomainIDs, IncludePlatform}`：非空租户与用户；域 ID 非空且不重复。
- 调用方已脱敏的 `Messages []Message`：`ID, TenantID, ScopeKind, DomainID, Type, SubType,
  Title, Description, OccurredAt, IngestedAt`。
- `Catalog []TypeSubType`：显式列举合法的 `{Type, SubType}` 对。
- `ReadState{TenantID, UserID, FirstReadAt map[string]time.Time}`：必须匹配 Scope 的两项身份。
- `Query`、显式非零 `ConfirmedAt` 与全部显式正数 `Limits`。

ID、类型名和子类型名按原始 Go 字符串精确比较；不隐式 trim、大小写折叠、数字转换或 Unicode
归一化。必填标识不得为空字符串。标题与描述可为空；本核心不能证明调用方已完成脱敏。
输入消息 ID 在整个输入批次内唯一（包括不同租户消息），每条消息必须带租户、合法类型/子类型
和两个非零时间。即使消息最终因范围或筛选不入结果，也先完成结构校验；错误不能变成空结果。
OccurredAt 与 IngestedAt 分别保留，不要求入库时间晚于发生时间。

`ScopeKind` 必须显式为 `ScopePlatform`（`platform`）或 `ScopeDomain`（`domain`）：

- platform 消息的 DomainID 必须为空；只有同租户且 IncludePlatform=true 才能参与。
- domain 消息的 DomainID 必须非空且在 Scope.DomainIDs 中；空域列表不匹配任何域消息。
- 字符串 `platform` 用作一个普通域 ID 时，仅匹配被显式授权的同名 domain 消息，不授予平台消息权限。
- 没有平台管理员、空域或空租户的默认绕过。

ReadState 的 nil/空 map 表示该身份没有已读记录。每条记录的 ID 和首次时间必须非空/非零；
允许保留本次输入或结果之外的 ID，因为当前状态可以覆盖该用户的其他消息。
核心只能检查传入身份的一致性，不能验证会话、来源真实性、数据权限或数据库状态是否最新。

## 类型目录与筛选

目录必须非空；每一对的 Type/SubType 均非空；重复对无效。相同 SubType 可以属于不同 Type。
这里的目录是语义允许表，不声称某个生产者已经实现。没有单独的无归属子类型字符串。

`Query.Types` 为空表示目录内所有类型，否则每个类型须在目录内且不重复。
`Query.SubTypes` 是完整的 TypeSubType 对；为空时不进一步限制，否则消息必须匹配其中一对。
未知对、重复对、或其 Type 不在非空 Types 选择中，均明确拒绝。多个类型的选择与子类型对选择
分别是 OR，两类筛选之间是 AND。不猜测 Leak/Baseline/Scan 等历史映射。

`Keyword` 仅在 Title 上做区分大小写的字面子串匹配；空串匹配所有标题。
正则元字符无特殊含义，不搜索 Description，不做 Unicode 归一化。

`ReadFilter` 必须为 `ReadAll` / `ReadUnread` / `ReadRead`，对应 `all` / `unread` / `read`。
`Sort` 必须为 `SortAscending` / `SortDescending`，对应 `asc` / `desc`。
两者的零值及其他值均拒绝，不猜测有效默认值。

## 时间冻结与稳定排序

所有保留的时间都转换为同一瞬间的 UTC，并去除进程内 monotonic 时钟数据。
所有查询均按 OccurredAt 的半开区间 `[Start, End)` 筛选：

1. 两端均为零值：冻结成 `[ConfirmedAt - 168h, ConfirmedAt)`；这是精确 168 小时，不是本地日历七天。
2. 两端均非零：转换为 UTC，必须 Start < End。
3. 只提供一端：明确拒绝。

ConfirmedAt 即使在显式双边界查询中也必须提供，用于记录结果的确认时刻；它不证明结果仍有效。
所有默认时间仅由调用方传入时刻计算，不读取系统时钟。
按 OccurredAt 升/降序；同一瞬间一律按 ID 字节序升序破平局，降序时也不反转该次序。

## 固定结果、计数与分页

Result 的字段不导出，必须通过 NewResult 构造。零值或 nil Result 的 List/Page/Locate/MarkRead
明确返回 invalid_result；其他读取访问器要求使用成功构造的 Result。
所有保留数据均脱离输入切片/map；所有导出的切片/map 都是独立副本，时间也不保留调用方 Location。
调用期间调用方仍不得并发写入其正在传入的 map/slice。成功构造后可并发读取和进行纯 MarkRead。

分页之前固定保存两组有序数据：

- `Base()`：租户/平台/域/类型/子类型/时间/关键词筛选后的全集，尚未应用阅读状态筛选。
- `Members()` / `IDs()`：在 Base 上应用查询时 ReadFilter 后的固定成员，供列表、定位与全选使用。

每个 Entry 包含 Message、查询时 Read 与 FirstReadAt。未读为 Read=false、FirstReadAt=零值。
`ReadSnapshot()` 返回查询时同身份已读状态的独立副本。
`Counts()` 的 All/Unread/Read 来自 Base，恒满足 All=Unread+Read。
`Total()` 和 Page.Total 为 Members 的分页前数量，不是当前页长度；它可以不同于 Counts.All。

`List()` 返回 Query.Offset/Limit 指定的初始页；`Page(offset, limit)` 只重新切片，不重新筛选。
`Locate(id, pageSize)` 只查同一 Members（包括初始页之外的成员），返回零基 Index、一基 Page。
未命中是 Found=false、Index=-1、Page=0；空定位 ID 是错误，不伪造位置。
定位页大小同样必须满足分页策略。Result.Query() 仍返回最初确认的查询与已解析 UTC 边界；
翻页不会改变它。空结果、空页、Base、Members、IDs 与导出 Query/Scope 切片均非 nil。

`Limit > 0`、`Offset >= 0`；`Limits.MaxPageSize` 必须为 1..200，Limit/pageSize 不得超过它。
无 -1 全量语义。大于结果长度的非负 Offset 是合法空页，计算不做可能溢出的 Offset+Limit。
20/50/100/200 等产品页面选项由适配层决定。

## 显式资源预算

Limits 的三个独立正数预算没有默认值，超限整次返回 budget_exceeded，绝不静默截断：

- MaxInputMessages：构造时所有输入消息数量，包含最终不入结果的消息。
- MaxInputBytes：构造请求的确定性逻辑字节预算；包含消息、作用域、目录、筛选与已读状态。
- MaxMarkTargets：单次 MarkRead 目标数量，独立于页长、输入消息数量和固定结果成员数。

MaxInputBytes 不是 JSON 长度、Go heap 实测值或产品容量。为了使输入数量与内容都有边界，按以下
架构无关公式逐项扣减，使用先比较后减法避免整数溢出：

- 每个构造请求固定 256 字节，覆盖时间、分页、布尔和策略等固定开销。
- 加 Scope 的 TenantID/UserID、Query 的 Keyword/Sort/ReadFilter 的原始字符串字节数。
- 每个 DomainIDs 元素：64 + ID 字节数。
- 每个 Catalog 或 Query.SubTypes 元素：64 + Type/SubType 字节数。
- 每个 Query.Types 元素：64 + Type 字节数。
- 每个输入 Message：192 + 全部八个字符串字段（含 ScopeKind）的字节数。
- ReadState：TenantID/UserID 字节数；每条 FirstReadAt 记录再加 64 + ID 字节数。

所有字符串使用 `len(string)`（原始字节数，不是 rune 数）。固定开销使大量空字符串元素也消耗预算。
先检查策略和预算，再建立目录索引或结果副本；即使预算足够，也仍须通过全部结构校验。

MarkRead 使用同一个 MaxInputBytes 对当前调用重新计量：固定 256 + 当前 ReadState 的上述费用
+ 每个目标的 64 + ID 字节数。当前状态可能比查询快照大，因此不能省略此次检查。
即使 ID 已读或目标为空，也不绕过身份、时间和策略约束。目标集合过大时调用失败；服务端不能
把静默截断后的某一批宣传成用户冻结目标已完成。跨批次调度与进度需要后续独立契约。

这些只是显式内存保护。没有总容量、P95、吞吐、保留期或跨页持久化容量承诺。
未来 SQL 分页需要另行审查等价方案，不能以本实现为由要求整租户无界加载到内存。

## 已读纯转换与重放

`result.MarkRead(current ReadState, targets []string, at time.Time) (MarkResult, error)`：

1. 绑定 Result.Scope 的租户/用户，必须传入调用方当前同身份状态与显式非零时间。
   不能用查询时快照替代数据库最新状态；核心也不能自行证明 current 最新。
2. 先验证目标数量/字节预算、当前身份/记录、时间以及全部目标。
   空 ID、重复 ID、结果外 ID（包括只存在于 Base、未通过 ReadFilter 的消息）均整次拒绝。
   错误返回零 MarkResult，不输出部分新状态或将拒绝伪装成逐条执行失败。
3. 验证成功后复制 current，保留所有非本次目标的记录。已存在 ID 保留其首次时间；
   新 ID 写入 at 的 UTC 时间。同一个 Result 可以使用更新后的 current 重放，首次时间不被覆盖。
4. 成功返回独立状态与 Progress；原 current、targets、Result 与查询快照均不修改。
   空目标在其他输入有效时是零计数无操作，仍返回独立状态。

成功时恒有 Target=Processed=NewlyRead+AlreadyRead，Failed=Remaining=0。
重放的状态幂等，重放计数按该次 current 计算，因此 NewlyRead 可以变成 0、AlreadyRead 增加。
若传入 current 缺少查询快照中原有记录，核心以 current 为准，不将旧快照记录重新合入。
持久化层必须防止陈旧状态覆盖，选择事务锁、乐观版本或等价一致性机制。

旧固定结果继续显示查询时已读快照；MarkRead 不删除 unread 成员、不重新排序或编号。
重新查询才会得到新的 Counts/Total/Members。

## 错误与后续集成责任

错误是 `*messagecore.Error`，可用 errors.As 读取 Code。Code 包括：
invalid_scope、invalid_message、invalid_filter、invalid_pagination、invalid_catalog、invalid_policy、
budget_exceeded、invalid_read_state、invalid_target、target_not_in_result、invalid_result。
Field 仅是代码中的静态字段名，错误不保留或回显输入 ID、关键词、标题、描述或其他提交值。

集成层仍须负责：

- 真实消息来源、数据投影与敏感证据脱敏；当前会话、平台许可、租户和域权限校验。
- 固定结果有效期/授权版本、读取/定位/标记前实时复核；失效时要求重查，不静默替换结果。
- 最新已读状态的原子持久化、冲突处理、幂等操作身份、跨页冻结目标、审计与事务。
- 持久化部分成功、失败、未知提交、取消剩余与恢复；本 Progress 不能标成已提交进度。
- SQL 等价筛选/排序/计数/分页、页面接线、生产目录映射、timeline/group count/appName。
- 真实 PostgreSQL、浏览器→API→存储链路、性能、授权 AD/Windows 与整项 F40 验收。

纯单元测试及 race/vet 结果应由 PR 按实际命令、工具链与 exact-head CI 单独记录。
代码、文档、独立审查、CI、合并、部署与产品验收是不同阶段，不相互替代。
