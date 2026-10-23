# Computer asset XLSX export v3: bounded EXP-007 slice

Contract freeze 2026-10-14; PR162 merged, exact-revision evidence recorded below.
Original implementation base main `7ed455aecee1352f0261e7064253891af1c31c79`, tree
`bdfc54e0982cb07a4679c5fa022f9324da31832a`. Refs
[F14 #30](https://github.com/yunpiao/adtr/issues/30), AD-F-053 / EXP-007,
[computer list](computer-assets-v3-contract.md) and
[group export lifecycle](group-asset-export-v3-contract.md).
AD-F-053 remains `in_progress`; Issues #30/#151 remain open; acceptance is **0/209**.

## Frozen source and shared query

One explicitly authorized domain and one immutable published dictionary3 observation.
Required domainId, expectedRevision, expectedCredentialRevision and observationId
pin the displayed source. Missing or stale pins never silently select latest.
Validate the complete observation, including excluded users/groups, before filtering.
This exporter performs no LDAP operation or credential use and creates no observation.

The computer list and exporter share their predicate and order helper. Optional
search is literal Go simple-lowercase contains on SAM/SID/DN, at most 50 UTF-16
units and 200 UTF-8 bytes; no trim, normalization, regex or DNS/IP/SPN inference.
Empty search matches all computers. Optional startTm/endTm must appear together
as RFC3339 instants with start before end: whenCreated is in [start,end), and
missing creation time is excluded whenever a time range is applied. This is an
instant range, not an implicit whole-day extension. Optional creatTm=asc|desc
sorts creation time with nulls last in both directions and GUID-ascending ties;
omission sorts by GUID ascending. Export captures ALL matching rows, not the page.
Unsupported pagination, status, overviewType, updateTm, group filters and invented
business fields are rejected rather than silently ignored.

Required screenColumns is an array. Empty array resolves to exactly
`guid,sAMAccountName,sid,dn,whenCreated` in that order. A nonempty array preserves
caller order; absent/null, duplicate or unknown columns fail validation. Headings
are `GUID,SAM,SID,DN,whenCreated`; absent scalar facts become empty cells.
No inferred device name, DNS/IP association, OS, business status or SPN-derived
values are exported. Zero computers or zero matches yields a real header-only
XLSX; unavailable, stale or corrupt source fails instead of becoming empty success.

## Independent authority and lifecycle

Schema23 installs the independent `computer_asset_exports` readable/writeable
capability and epoch invalidation, task kind `computer_assets.v3.export`, and three
separate snapshot/chunk/manifest tables. Existing custom roles gain no grants;
exact permission catalogs include explicit false/false marks. Built-in admin
capability conventions still require explicit domain resource scope. The new
permission is necessary to authorize this producer separately; omitting it fails
closed. Installing code does not enable collection or create credential grants.

Reads require domains.readable, directory_assets.readable,
computer_asset_exports.readable and tasks.readable. Submit/cancel also require
computer_asset_exports.writeable and tasks.writeable, same-origin JSON and fresh
password/TOTP proof. Every API/Worker transaction checks actor, tenant entitlement,
domain/resource scope, exact schema and authorization epoch. Only the creator
can read the receipt, task and artifact, including generic task APIs. An epoch
change permanently invalidates old artifacts. Other export permissions do not
authorize this producer. Generic submit/recover/cancel cannot bypass its checks.

Prefix `/api/computer-assets/v3/exports` follows the group-export HTTP contract:
POST submit, GET `/receipt`, GET `/detail`, POST `/cancel`, GET `/download`.
Canonical request contains domainId, expectedRevision, expectedCredentialRevision,
observationId, search, startTm, endTm, creatTm and screenColumns. Optional strings
canonicalize empty. Receipt/replay preserve the original operation after current
owner/epoch/authority checks; receipt404 never proves a lost submit did not commit.
Cancellation can proceed after source rotation and must not require obsolete pins.
Proof is absent from payloads, fingerprints, logs, browser storage and evidence.

Fixed worksheet `ComputerAssets` and filename `computer-assets-v3-<taskID>.xlsx`
bind the output to this producer. Download verifies the whole artifact and digest
before any bytes, with exact Content-Length, X-ADTR-Artifact-SHA256 and
X-ADTR-Row-Count. Successful replies include no-store/nosniff and X-ADTR-User-ID.
Pending means queued/running/retry_wait/cancel_requested; only succeeded with a
valid current manifest is ready. Failed/dead_letter/partial_failed are failures;
cancelled requires persisted cancellation. source_changed/source_unavailable/
artifact_invalid disable download; database/schema failures remain503. Resolve
uncertain cancellation through detail, not by inventing a successful terminal state.

UI binds responses to actor/domain/revisions/observation/query generation, states
all-matching scope and column order, and invalidates late results. Unknown submit
retains the original idempotency key; receipt lookup never automatically resubmits.
Session recovery stores only actorID/domainId/key/fixed version-kind with strict
type/size/actor checks. Download revalidates session, actor/kind/task metadata,
bounded bytes and SHA256 before creating a Blob URL; authority loss discards
protected results and URLs are revoked. JavaScript zeroization is not claimed.

## Execution and protective limits

Replay-safe execution follows the group lifecycle: maxAttempts3, lease30seconds,
heartbeat5seconds, timeout5minutes, database_unavailable retry after5/10seconds
(cap60seconds), and engine recovery of interrupted/expired-lease attempts under
the same budget. Authorization, source, corruption and deterministic limit errors
are not retried. These bounds prevent unlimited work; removing them is outside scope.

Capture matching rows and whole-observation SHA256 under fencing. Every persisted
transaction and final success gate rechecks source/digest/authority/time eligibility.
Staged output is not downloadable; only fenced succeeded publication is eligible.
Cancel/revocation wins over success. Chunks are at most1MiB with ordinal/digest;
manifest follows ZIP close. Download verifies contiguous chunks, individual/whole
digests, length and exact task result agreement. Failed/cancelled/dead-letter
cleanup removes generated snapshot/chunks/manifest without requiring a live
executor; original receipts/events remain. Successful artifacts have no invented
retention period. Old attempts are removed under fencing before retry.

Cells are inline text, preserving formula-like prefixes, XML characters, CR/tab/LF,
Unicode and literal OOXML escapes; no formulas, links, macros or implicit numeric
or time conversion. Invalid selected XML text fails without truncation or echoing.
Protective limits: source10000 objects/16MiB encoded; export10000 rows/five columns;
cell32767 UTF-16 units; XLSX32MiB. Overflow fails instead of silently truncating.
Selection and validated download use bounded memory; these are not demonstrated
legacy capacity, peak-memory or end-to-end streaming guarantees.

## Verification and acceptance boundary

Required tests cover list/export query equivalence, time boundaries/offsets/missing
time, null-last ties, all pages, reordered/default/invalid columns, header-only,
whole-source corruption, stale pins, actor/tenant/kind/epoch isolation, receipt
ambiguity, retries/fencing/final source rotation, cleanup and actual ZIP integrity.
Three dedicated browser jobs cover normal/all-matching and empty output, invalid
selected text followed by safe-column recovery, and queued cancellation with
owned-Worker quiescence, resumed activity and durable zero-artifact evidence.
Actual XLSX/PNG evidence must bind database/source/checkout/run provenance.

The original candidate preserved all65 baseline checks and added3 for68; no old failure or requirement is
removed. Local validation is static/gofmt and explicitly guarded pure Python;
local Go/Node/PostgreSQL/browser/build execution is not claimed. Each later candidate requires its own CI, independent actual-diff/artifact review,
current-main compatibility and separate post-merge main-push checks. The recorded
PR162 success below is baseline evidence only for later revisions.
Running/cancel_requested browser acceptance, full legacy filters/business columns,
DNS/IP exports, real AD/eight Windows versions, target capacity and production
acceptance remain open. No deployment or Issue closure is part of this slice.

## Recorded PR162 evidence (2026-10-14)

[PR162](https://github.com/yunpiao/adtr/pull/162) is merged. Final head
`09e1c52f8ceac2a2d85cdc8b4c71fbf515fa078a` and actual main merge
`94b098d6acf5187e1ecf9aa659f5abed00122ce7` have tree
`0c669bd750c2ec137311aad08b6a05744ddff907`.
[PR CI38550970449](https://github.com/yunpiao/adtr/actions/runs/38550970449)
finished68/68 on attempt3; independent
[main-push CI38561279766](https://github.com/yunpiao/adtr/actions/runs/38561279766)
finished68/68 on attempt1. These results bind those exact revisions, not the new
user running-cancel candidate or future documentation/code changes.

Fresh main artifacts11893251763,11893351368 and11892781839 independently passed
source/provenance and actual-file inspection:940 source objects,216 XLSX cells and
six PNGs across normal/empty, failure/recovery and queued-cancel scenarios. This
establishes the bounded synthetic browser/Worker/PostgreSQL/API slice, with queued
cancellation only. It does not establish running cancellation, real AD or product
capacity/compatibility acceptance.

Historical failures are retained:
- Initial head74af592/tree791bfd, run38545054964 attempt1:54 successful jobs and14
  failed (eight sync.WaitComputer compile failures, four stale membership-permission
  catalog expectations, one sorted-access expectation and aggregate verify)
- Corrected head09e1, run38550970449 attempt1:66 successful and two failed
  (reader socket-hangup and verify). The precise socket failure cause is unknown
- Attempt2's actual reader and verify jobs passed, but the latest rollup still had
  queued duplicate metadata. A rerun of current eligible attempt jobs and metadata
  convergence produced final attempt3's68/68. A rejected old-attempt job rerun403
  remains a failed operation; it is not a test pass or evidence that the old failure
  never occurred

AD-F-053 stays in_progress, product_accepted=false; Issues30/151 remain open and
product acceptance remains0/209. No production deployment is implied.


## Candidate: snapshot-stage running cancellation (2026-10-14)

The test-only `computer-asset-export-v3-running-cancel` suite extends PR163 main
`ec0da2018cc5914b8d0ca345c1c2c4cd5078d177` (tree
`ec801e6ffc0bce09cd89714bb1be3586d2781fad`). Together the group and computer
suites add exactly two isolated stable-body jobs to the prior69, yielding71.
The new reversible contract restores all prior runner/workflow bytes. The old
terminal suites remain separate, with no double configuration or validation path.
Exact-head runtime verification and independent artifact inspection are pending.

The fixed runner-selected kind binds the owned Worker guard to the computer export
identity, snapshot tables, and original source. The guard's default remains user;
the HTTP checkpoint accepts only taskID/workerID, never a caller-selected kind.
`running` here requires a durable snapshot and progress10 for an executing attempt;
`cancel_requested` requires a persisted request while that Worker remains paused;
`cancelled` requires its genuine resumed terminal transition and cleanup. A short
activity-row lock exposes this checkpoint; it must not block the API task-row write.
More than20seconds live lease is required before STOP, with CONT within10seconds
and owned-process identity rechecked. These test-only guards are necessary to keep
the observation within the same live execution; removing them can test completion
or lease recovery instead. Failure cleanup resumes the Worker and releases locks.
Production lease/heartbeat/timeout values and security limits remain unchanged.

A successful case must prove the original receipt, durable transitions/events,
no downloadable file, zero snapshot/manifest/chunk rows, and subsequent activity
by the same Worker. A freshly completed computer source and kind-specific sensitive
operation budgets are required. Evidence formatVersion1/suite runningCancel is
bound to actual checkout/tree/run/source hashes. Only the exact provenance JSON,
evidence JSON, running-checkpoint.png, cancel-requested.png and cancelled-terminal.png
are staged after validation; raw logs, proof inputs, XLSX and traces are excluded.
CI screenshots must be inspected independently, not inferred from validator success.

Local checks are static or explicitly allowlisted offline Python with subprocess,
network and signals rejected; local Go/Node/PG/browser/build are not run. This
candidate does not establish chunk-stage cancellation, fencing races, legacy field
parity, capacity or real AD/Windows acceptance. AD-F-053 stays in_progress;
Issue30/151 and product acceptance0/209 remain open.


## Task-ID original result read (AD-F-152 / AD-F-151, Issue #26)

This bounded follow-on to AD-F-053 / EXP-007 exposes an independent read-only
`ComputerAssetExportTaskResult` only for exact `computer_assets.v3.export` task
records. It does not extend the five permitted columns or the computer search
predicate, and does not add a producer, API, permission, database change or LDAP use.
User and group result behavior is unchanged. Generic task viewing remains governed
by its original permissions; denied dedicated detail disables only this entrance.

The dedicated gate probes only the existing detail/download read paths, and requires
readable domains, directory_assets, computer_asset_exports and tasks. Download is
independently disabled if its path is denied. The access/check POST is a permission
probe, not an export mutation; export traffic is GET only. Current actor headers,
server tenant/domain ownership and authorization epoch checks remain mandatory.
First detail-by-task validates the exact task ID, domain and kind plus the complete
canonical request/artifact. That first request is locked for subsequent reads,
including close/reopen; refresh cannot substitute a new observation or revision.
The panel displays original source pins, canonical filters/sort/ordered columns,
status and artifact. source_changed keeps the original source visible and disables
download rather than adopting the latest observation. Pending, failed, cancelled,
source_unavailable and artifact_invalid are also non-downloadable.

The original download implementation is unchanged: fresh detail, strict headers,
bounded complete bytes and SHA256, then another fresh detail binding the same
request and manifest before a browser file may be created. Close, task/domain,
account/session change, unmount or parent generic-task refresh abort and invalidate
outstanding metadata/body/postcheck work. Authority failure clears private detail;
401/session loss requests session refresh. Permission recheck is manual; there is
no polling or automatic retry. This read path does not read or write recovery
storage, generate an idempotency key, submit, cancel or replace unknown intent.
Original creation/recovery remains in the computer asset export panel; cancellation
also has the separately gated original-task entrance described below. This result
component itself remains GET-only.

Named DOM/API tests exercise first binding, changed sources, independent permissions,
all non-ready statuses, late metadata, actual streamed-body/postcheck interruption,
byte/manifest corruption and postcheck revocation. These synthetic tests are not
real source evidence. The existing computer export browser suite retains its
three real default/allMatched/empty producer flows, source pins, database/sensitive
operation budgets, XLSX validations and original ready-status/task-ID/download
viewport assertions. After removing the persisted recovery locator, a new tab
opens each actual task, reads its original result and downloads identical bytes;
its export traffic must be exactly detail/detail/download/detail GETs with no
receipt lookup or export POST. The original XLSX artifact remains independently
validated, and the task-view copy must be byte-for-byte equal.

The existing validator and upload allowlist admit only default-ready.png,
allMatched-ready.png and empty-ready.png. Those three 1440 x 900 evidence slots
now show the task original-result panel, while retaining the original flow/layout
assertions before navigation. They do not establish new pixel acceptance for the
old ready panel or mobile/narrow layouts. Evidence must be generated and reviewed
from the exact submitted CI head; local runtime was not executed for this change.
Issue #26 and Issue #30 remain open; this is not full product acceptance.


## AD-F-151 / AD-F-189：无本标签页 locator 的原计算机导出取消

[工程 Issue208](https://github.com/yunpiao/adtr/issues/208) 是既有取消能力的可达性与
未知结果保护集成，不增加 producer、任务种类、服务端状态或权限。来源为
[Issue26](https://github.com/yunpiao/adtr/issues/26) 的任务状态与文件工作流及
[Issue12](https://github.com/yunpiao/adtr/issues/12) 的原任务状态；精确取消语义仍是
本文既有专用 `/cancel` 契约。基线 main
`5ebaf5aa6090686de17131d60a058c3af4c0177b`。本节是实现与验收约束，实际运行、
PNG和清理结论须由最终精确head的CI证据建立；产品验收仍为0/209。

### 入口、原任务绑定与权限

通用任务详情仅对精确 `computer_assets.v3.export` 增加独立折叠区“任务原计算机导出取消”。
初始不显示proof，不读专用业务详情，不创建locator。用户点“核对原计算机导出取消”
后才核验专用读取与取消能力，按原taskID读取并固定完整规范请求。原
ComputerAssetExportTaskResult仍只有读取/下载GET；通用任务cancel/recover继续禁止此kind。
原任务ID不是任意任务取消授权：服务端专用TaskTx先调用Engine.DetailTx，校验原
actor/tenant、owner-scoped授权epoch、kind、域和非归档；CancelTx后再DetailTx并提交。
仅有readable不能取消，仍需tasks/computer_asset_exports writeable、当前域scope、
entitlement、CSRF/Origin及本次密码/未使用TOTP。取消不重新验证来源pins；不得替换
为当前列表的新revision/observation。没有新增grant、角色、调度或凭据授权。

“读取原计算机导出当前状态”仅显式GET同一原任务。running不能说已停止，
cancel_requested表示请求已登记但Worker尚未确认，只有真实cancelled可显示
“服务器已确认取消，没有可下载文件”；其它终态竞争如实展示，不断言取消获胜。
GET状态不是取消回执，也不能用queued/running/retry_wait证明早先未知POST不会提交。

### 两个取消入口共享的持久未知屏障

旧资产页取消与新任务入口共用localStorage marker和Web Lock。key/lock name固定为
`adtr.computer-export-cancel.v1.<actorID>.<taskID>`，kind固定computer_assets.v3.export；不把
domain或digest放进锁名，防止不同内容变成同一任务的另一把锁。marker精确字段为
version、kind、actorID、taskID、domainId、requestSHA256，固定键顺序、类型和大小，
拒绝额外/重复键、非规范JSON或绑定冲突。requestSHA256按本文完整规范请求的固定
字段顺序计算：domainId、expectedRevision、expectedCredentialRevision、observationId、
search、startTm、endTm、creatTm、screenColumns。没有用户导出的sAMAccountName筛选字段，
保留空字符串与screenColumns顺序；它只用于一致性，不是授权、服务端状态或回执。
不保存原filter/request正文、username、幂等键、proof、CSRF、token或响应。hash不使
筛选条件匿名，只持久化必要的标识符和不透明摘要。

显式提交先清proof输入，再取得exclusive/ifAvailable Web Lock，不排队保留旧proof，
不steal。锁内再次核当前生命周期与marker、重新授权并GET原detail；若已终态或
cancel_requested则0 POST，防另一tab清终态marker后本tab旧running表单再次提交。
无marker且仍允许取消，先写入规范marker并回读完全相同字节，才能调用一次原专用
cancel。持久化/回读失败、缺锁或锁被占均0 POST；不退回内存、sessionStorage或租约。
输入证明只存在瞬时表单/当次请求闭包，不写入共享恢复结果或诊断。所有await后均核
signal/generation；卸载、关闭、绑定的任务/域/身份变化、focus/visibility/storage变化
清私有显示和proof，中止旧请求，迟到响应不能恢复控制或清marker。30秒deadline只
限制UI等待，不代表服务器回滚或停止。

未知POST、超时、断连、畸形响应、撤权或其它401/403/404保留只读恢复屏障，不自动
重发。marker跨重挂载、新标签和重新登录保存；同actor重新进入仍要授权读取原task。
没有自动过期、清空所有记录或“忘掉未知取消”按钮；放弃原创建locator不删除marker。
匹配且当前有权限的专用终态可持锁精确删除并确认缺席；cancel_requested保留至终态。
删除/读回不确定时当前生命周期仍只读，不能声称已remove后marker必然仍存在。
崩溃发生在写marker后、实际发出请求前也可能留下保守屏障；以后GET非终态不能证明
当次没有发出，没有凭空制造的取消回执API可以解锁。

### 精确proof拒绝的安全纠正

只有当次专用cancel的完整、最多4096字节UTF-8/无重复键JSON
`{error:"invalid_credentials"}`，精确HTTP401及application/json、no-store、nosniff，
才是闭集负向证据。后端requireAccessProof在cancel dispatch前返回该错误并rollback，
所以该次请求没有取消效果，但仍可能有速率桶/失败审计效果。错误响应不要求不存在的
actor身份头；若意外出现且不匹配则拒绝。body读取失败、超限、UTF-8/JSON/schema或
metadata错误仍按401失效私有UI，不能以status0绕过失效，也不能清屏障。

API私有brand绑定当次signal、actor、task和完整原请求，不是任意error.code即可仿造。
同一锁callback从dispatch持续到可信拒绝后的auth/me、menu/check、原detail、最终
auth/me，核当前身份字段/CSRF、生命周期、专用读取响应身份头与完整原绑定。成功后
仅可删除本invocation刚写入的精确marker并确认缺席，才提示重新显式输入fresh proof。
不能释放再抢锁凭相同字节删除，避免ABA清掉另一attempt；任何授权/绑定/读回失败只读，
其他未知401不例外。若fresh状态已cancel_requested或terminal，不重新打开取消表单。
没有自动二次POST，也不清除更早未知请求或其他任务的marker。

Web Locks/localStorage是本客户端两入口跨标签保护的能力依赖，不是后端授权或幂等
机制的必要条件。支持这些能力的浏览器保留正常旧取消；不支持/不可用时只禁这两个
取消入口并解释原因，读取、下载、创建及其它业务不受影响。不修改浏览器/系统权限。
保证只覆盖同新版、同origin与存储分区的协作入口，不覆盖仍开着的旧版页、另一设备、
站点数据被手动清除或隐私窗口结束；不承诺服务器exactly-once。

### 旧资产入口的只读刷新兼容

旧资产页普通1.5秒只读轮询保留：正常pending创建可以自动读取到终态，缺少锁/存储
本身不停止轮询。普通同身份、同原任务的receipt/detail刷新，包括queued→running，
不是失效事件，必须保留已经填写的MFA和原proof fieldset；不得因fresh DTO重挂载而
清掉证明。真实focus/storage/session/身份失效及显式提交仍清proof。锁内的当次授权
和原detail复核不省略。旧running套件先准备proof再checkpoint/receipt，流程不改。

确认cancel_requested后，旧“取消计算机导出任务”按钮仍存在且native disabled；可
移到已销毁proof表单外作为不可提交按钮。它表示请求已登记，不是允许再次取消。
未知屏障或实际事件失效要求显式恢复，不能与普通已授权GET刷新混为一谈。新任务
入口只保留原状态查询；旧running的2秒取消响应/click、1.5秒disabled和10秒暂停上限
不扩大。

### 有界验证与证据

新增DOM/API契约验证原owner/session/task/request、默认折叠GET-only、严格proof拒绝
纠正、未知跨remount/session/两入口屏障、存储/锁失败0POST、原请求每字段摘要、ABA、
迟到/撤权清空和固定安全错误。不支持锁/存储时其它操作不退化；正常旧取消继续覆盖。
合成DOM状态不替代真实浏览器/Web Locks/Worker证明。

既有queued-cancel真实套件保留TLS fixture采集、原创建、专用Worker暂停、queued
attempt0与零snapshot/manifest/chunk。另开无opener、无sessionStorage locator的同会话
新标签，从真实任务列表进入原详情；先验证默认没有proof/通用取消，再显式进入专用
控制并一次取消。原通用非终态轮询继续运行；真实状态转换可使专用准备失效，测试
等待通用页真实cancelled后再显式GET原详情，不把POST回包当新读取回执。窄native
fetch前置只读观察器只比较完整预期marker bytes，保存布尔
值和计数，不读proof/body，不改原fetch参数、缓存或响应，不route/伪造/重放请求。
本次原任务读取及两次旧面板恢复证据绑定动作开始后新发出的原Request对象，
只接纳相同对象的
Response；旧poll回包不能满足新checkpoint。观察器有界、settle/join并卸载监听器与
timer，不把transport错误原文交给报告。旧tab原locator逐字不变；新tab不得创建/
receipt/download/通用cancel/recover。

原资产面板继续通过真实receipt/detail恢复，保留取消状态、无下载、原ID与新建按钮
可视断言；owned Worker恢复后两轮队列仍无执行，原数据库及attempt0不变。proof
计数仍由owner7/producer9起始，create+cancel后owner9/producer9/exportTasks1，10上限
不变；900秒测试上限、120秒初始暂停和所有owned-worker deadlines不扩大。
新增等待分支settle/join后才收尾，新tab关闭与owned Worker恢复均有finally保障；失败
只输出固定checkpoint，原始异常、Cookie、proof或cause不进入报告。进程/容器清理仍
由实际runner证据独立证明，不能用取消终态或零artifact推断。

现三文件allowlist与严格证据schema不变：provenance JSON、cancel evidence JSON、
cancelled-terminal.png。PNG在恢复及不可变数据库验证后真实截取新任务取消入口，
保留1440×900、原taskID与确认终态、无proof。截图前直接确认新section可见、原UUID
和确认终态在视口内，并分别检查新section与整个页面没有密码/actorPassword/TOTP
输入；旧helper定位不到资产面板所得的空集合不能代替该检查。这是同一suite截图
主体的明确改变，旧资产面板保留DOM/可视检查，不声称同时保留其新的像素证据。validator继续验证PNG
SHA/头部与当前checkout/run/source，独立review仍须实际看图，不能用PNG头替代像素。
原running-cancel真实套件保留为旧入口/后端回归；新入口running/retry_wait/未知响应
仅DOM/API覆盖，不声称新增running入口真实checkpoint或丢回复artifact。真实AD、
Windows、容量及完整AD-F/EXP验收继续开放。

### 隔离合成多块 XLSX 完整性与资源表征

新增独立 `computer-asset-export-v3-multichunk` 验收套件，不替换既有小样本三场景。
它通过原 LDAP、目录任务、普通 Worker 和 PostgreSQL 产生一个 1,024 对象的合成观测，
其中 1,020 台计算机，另外各两用户和组；四页，每页 256 对象。固定五列与
`computer-` 关键词、2026-01-01（含）到 2026-01-02（不含）、创建时间升序使用现有 API。
每条长 DN 仍在既有 4,096 字节限制内；生产 10,000 行、32 MiB 文件、16 MiB 观测
及 1 MiB 分块上限均不改变。固定夹具应产生的压缩大小不是实测结论。

真实门禁要求：一次创建、实际文件大于 1 MiB、至少两条已提交数据库 chunk，
每个非尾块恰为 1 MiB。任务、快照、manifest、所有块的原 task/actor/source/payload/fence
必须一致；提交和租用事件的 resultVersion 可为 0，所有事件版本非递减，最终任务版本为正。
完整原始观测由数据库分段读取，不重新序列化；独立从这些字段导出全体 1,020×5 单元格、
表头、首末行和矩阵摘要。真实浏览器下载文件与只从数据库块组装的文件逐字节相等，
还须通过各块及整文件摘要。不能复制浏览器文件冒充数据库文件。

浏览器对每项动作先锁定本次 host 观察窗口的第一个原 Request，再验证身份、请求、
响应元数据和正文；首个坏响应或 JSON 失败不能由下一条成功替代。原 native JSON
读取观察器保持不变，不重放请求、不替换响应、不改缓存。文件来自明确点击后的实际
Download 事件，不使用另一次 HTTP 请求或 CDP body 冒充下载。成功 ready.png 使用真实
1440×900 页面，原任务、完成状态、行数、下载控件可见且密码/MFA 输入为空集合。
图片结构检查不替代独立像素复审，也不声称 Windows Excel 已真实打开。

新安全 validator 严格验证七个原名文件：source-observation.json、database-export.xlsx、
download.xlsx、ready.png、evidence.json、provenance.json、resources.json。除了单元格，
还检查 ZIP local/central/descriptor/EOCD 全字节覆盖、五个静态 XML 模板、完整规范
worksheet，拒绝 XML 额外属性/注释/PI、ZIP 注释/extra/尾载荷。PNG 仅接纳当前真实
RGB8 截图的 IHDR/连续 IDAT/IEND，逐 CRC、单完整有界 zlib 流及 scanline 检查，拒绝
text/profile 等任何附加块。双方文件和全部摘要重算后的 canary 仍须被结构门禁拒绝。
不通过清洗、改写或重编码来使证据过关；旧 validator 不变。这不是通用凭据扫描或
对像素/压缩隐写的无秘密证明，合成数据边界、proof-free DOM 和独立像素复核仍必要。

资源数据只由 runner 读取自己启动的 API/Worker 进程，窗口覆盖完整浏览器阶段，
包括登录、MFA 等待、目录采集、导出和取证。CPU 为计数差；RSS 是近似端点值，
HWM 是进程生命周期高水位，并非导出专属峰值，也不包含浏览器、数据库和整机资源。
没有新增产品 SLO 或资源合格阈值。进程身份不符、不可读取或清理证据不合格均拒绝
成功上传，不请求新权限。原 runner finally 保留；退出零且未强杀等证据不独立证明
Docker 容器已不存在。

套件使用独立一次性夹具，实际 proof 账本须 owner 7→8、producer 9→9，
观测/use 各 1，导出任务 0→1。900 秒 spec、930 秒 runner、30 秒 validator、
原 30 秒 source/ready 等待、单 worker、零重试及 20 分钟 job 不放宽。
七文件仅在真实成功、严格验证、原清理结束后保存，14 天留存并绑定实际 tested commit、
完整来源摘要和 run/attempt。未通过不得上传部分产物或自行增大夹具/重发任务。

本小节描述待执行的工程验收门禁，不记录尚未发生的多块容量或资源测试结果。
最终精确提交 CI、真实产物与独立审查完成前，不能宣称该套件通过。即使通过，
它只覆盖一个既有上限内的合成多块数据点；真实 AD/Windows、目标规模压测、最大容量、
超量行为和完整 AD-F-053 验收仍开放，产品账本保持 0/209。
