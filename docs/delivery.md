# 交付台账与工作约定

### 2026-10-17 running-cancel 全会话冻结前就绪预检候选

基线PR167 head `abddb252`（完整发布SHA由远程提交记录核对），本地同树提交
`5ad803998fce88d638199543620f5e305fb84ab9`、tree
`db5458b3481e7670cb7c4cb599945c5a2470542e`。
[run38997385830](https://github.com/yunpiao/adtr/actions/runs/38997385830) 的computer
job `117293165648` 记录other/idle-in-transaction/Client/unsafe_relation；安全artifact
`12088272241`。本轮user/group通过，说明该捕获问题并非user专有；具体事务来源仍未知。

候选修复已确认的捕获门槛缺口：运行期先完整检查所有owned会话/锁的结构、类型、
PID唯一性、blocker归属及无环，防止首条busy语句遮住后续非法样本。
未知锁类型、外部blocker、重复PID、结构/身份/观察异常立即失败。
目标进入首个snapshot边界后，先调用原checkpoint验证，再以原validate_sessions
对完整原样样本预检。仅固定的严格冻结谓词拒绝可视为尚未就绪，继续原捕获循环；
other和unsafe_relation不会被认定安全，也不会作为成功冻结证据。

1.5秒deadline从原resume点计算，绝不重置或延长；mutex内SIGSTOP前再次检查deadline。
超时保留最后闭集lastNotReady诊断并失败，不输出SQL/参数/ID或异常原值。
仅在完整样本已满足原冻结规则时发出一次STOP；全线程停止后重新观察并执行原严格
checkpoint/session验证。停后任何失败立即CONT清理，不重新解冻捕获。
SQL/关系/锁白名单、租约门槛、冻结后严格验证、barrier释放及清理保持。

新增预检用于避免已明确观察到其他事务仍忙时立即冻结；删除会恢复原捕获竞态。
副作用是就绪可能更晚，繁忙样本可能耗尽原截止时间而失败；观察至STOP的剩余竞态
仍通过停后严格复核拒绝，不能声称消除全部不稳定性。

离线66项契约通过（guard49项），含三kind忙到安全只STOP一次、持续忙超时无STOP、
发信号前到期、首个busy遮挡后续坏样本、目标身份/租约错误、预检安全但停后变坏立即
清理，以及诊断隐私和未知异常传播。原validate_sessions/validate_checkpoint逐字节
保持。Node/Go/PostgreSQL/browser未本地运行，独立审及精确head CI仍待完成。

### 2026-10-17 running-cancel 持锁类别补充诊断候选

PR167 head `b7c6e1703179637874c70c096dcdf5e67721123f` 的
[run38995521988](https://github.com/yunpiao/adtr/actions/runs/38995521988)
user job `117287169944` 再现严格事务拒绝；安全 artifact `12087746411` 记录
`other` / `idle in transaction` / `Client` / `locksPresent=true`。
这表示观察时事务尚未结束、未执行查询、正在等待客户端且有锁；非active状态的query
是最近语句。具体事务路径与锁类别尚未确认，不能由other推断为任何已知安全SQL。
状态语义见[PostgreSQL文档](https://www.postgresql.org/docs/18/monitoring-stats.html)。

静态检查及合成样本已证实捕获门槛缺口：现有capture只要求一个活动记录waiter及目标
snapshot边界即SIGSTOP，然后才验证全会话。拥有相同application_name的worker进程
还运行多槽claim、recovery、scheduler、maintenance及message循环；可能冻结另一个
尚未结束的事务。这是与证据一致的机制假设，不是此次失败具体来源的证明。

本候选只在拒绝事务事件增加 `lockSafetyCategory`：none、allowlisted_shapes、
unsafe_relation、unsafe_advisory、unsafe_virtualxid、unsafe_transactionid、
unsafe_tuple、unknown_lock。固定优先级汇总首类违规，不依赖锁枚举顺序。
allowlisted_shapes仅说明锁形状符合原非activity规则，事务query仍被拒绝。
字段不含锁名称、对象、ID、SQL或参数；删去只失去诊断信息，不改变准入。
捕获、1.5秒窗口、租约、SQL白名单、冻结后复核、CONT及清理完全不改。

离线Python契约59项通过（guard42项），包含516组锁形状与原非activity锁规则的
一致性矩阵、未知值/字段注入隐私检查及清理后单次输出。所有原有函数逐字节比对，
仅transaction_query_failure增加诊断字段，其余保持；diff检查通过。真实运行与
精确head CI待完成，不能将本地静态结果当作浏览器或数据库验证。

未来候选可以在冻结前要求全会话满足原严格冻结条件，运行窗口内不满足条件时继续
观察，但需独立明确观察/身份异常与未就绪的边界，固定截止时间，并保留冻结后
严格复核；观察到STOP间的竞态仍需验证。此次不采纳该行为改动，也不放行other。

### 2026-10-17 running-cancel 事务拒绝诊断候选

基线真实 main `73ef65bf9860e695ce2f1a0b2b39c264f458c43d`，内容树
`bf65237e8d323a69f94f5bd1955704b0a907d460`；独立源码副本的 Git tree
已逐内容重建核对一致。本地快照提交仅用于审查，不冒充真实 main 提交。

[main-push run 38993821990](https://github.com/yunpiao/adtr/actions/runs/38993821990)
的 user running-cancel job `117281858696` 在 02:27:31 UTC 拒绝了未匹配
严格 SQL 白名单的事务，输出 `unsafe_transaction_query` / `session_validation`。
安全诊断 artifact `12086807691` 已保留。现有证据未记录具体 SQL 类别和会话状态，
根因尚未确定；历史同代码重跑通过不能证明本次失败安全，也不能支持扩大白名单。

候选仅增加闭集失败诊断：`queryCategory` 为完整匹配的 activity/schema/schemaGate、
begin/commit/rollback 或 other；`stateCategory`、`waitTypeCategory` 为固定类别，
未知值归 other，空等待归 none；`locksPresent` 仅说明是否观察到锁。
诊断不包含原 SQL、参数、连接串、环境、标识符、锁对象或异常原值。
这些类别仅用于定位；BEGIN/COMMIT/ROLLBACK 事务仍被原严格规则拒绝。
删除新增字段只会失去定位信息，不改变安全判定；副作用为失败事件增加四个字段。
SQL 接受列表、冻结时序、清理及 CONT 后单次输出顺序保持。

离线验证：原 guard 36 项基线通过；新增后 40 项 guard 契约通过，合并相关 runner/
安全产物契约共 57 项通过，审计钩子禁止真实进程、网络、信号。
首次组合运行因工作目录错误出现10项文件缺失错误；保留记录并在仓库根目录原代码
重跑57项全通过。逆变换诊断新增片段后，guard 与原源码逐字节一致；`git diff --check` 通过。
本地 Node/Go/PostgreSQL/browser 未运行；独立复审、精确远程 head CI 与合并后验证待完成。
这是诊断增强候选，不宣称已解决实际事务拒绝根因，不改变产品验收范围。


总路线图：https://github.com/yunpiao/adtr/issues/1 。全部 209 项均在范围内。
工程文档、代码、PR、最新 head CI、合并、产品验收分别记录；任一阶段不能代表后续阶段完成。

## 当前组合状态（2026-10-11，核对至 main 6f7f44a）

### 2026-10-17 字典3测试凭据去硬编码候选

仅 `directory-v3` 浏览器套件的三个账户密码与未提交证明输入改为必需环境变量：
`ADTR_E2E_DIRECTORY_PASSWORD`（bootstrap 修改后的密码）、
`ADTR_E2E_DIRECTORY_PRODUCER_INITIAL_PASSWORD`（新建 producer 初始密码）、
`ADTR_E2E_DIRECTORY_PRODUCER_PASSWORD`（producer 修改后的密码）、
`ADTR_E2E_DIRECTORY_DRAFT_PASSWORD` / `ADTR_E2E_DIRECTORY_DRAFT_CODE`（导航丢弃测试的未提交输入）。
`scripts/test_auth_e2e.py --suite directory-v3` 每次生成独立临时值，覆盖继承同名值，
仅通过进程环境传给自有测试子进程；不读取或创建 GitHub Secrets，不持久化或打印值。
32字符随机密码满足现有12–64字符规则；draft code 是随机六位输入，不是真实MFA验证码。
这些变量仅该套件必需，直接运行 Playwright 缺失任一项时失败，无固定密码回退；删除生成
接线将阻断该套件。除下述 `group-members-v4` 外，其他套件不注入新值；原 bootstrap/LDAP 随机凭据和真实MFA流程保持。
进程环境仍包含临时敏感输入，不应转储；trace/video/自动截图关闭及DOM快照抑制保持，
人工截图仍只在已有清理检查点采集。新增密码纳入现有公开结果/storage/console泄漏检查。

后续对本次待提交文件扫描补充 `group-members-v4` 的五个固定账户密码，改为
`ADTR_E2E_MEMBERSHIP_PASSWORD`、`ADTR_E2E_MEMBERSHIP_READER_INITIAL_PASSWORD`、
`ADTR_E2E_MEMBERSHIP_READER_PASSWORD`、`ADTR_E2E_MEMBERSHIP_PRODUCER_INITIAL_PASSWORD`、
`ADTR_E2E_MEMBERSHIP_PRODUCER_PASSWORD`，分别用于bootstrap修改后、reader初始/修改后、
producer初始/修改后密码。仅该套件生成并必需，缺失失败且无回退，全部进入泄漏检查列表。
`Tasks.test.tsx` 的fetch mock密码/验证码每项测试临时生成；重放验证码确定不同于首个，
保留两次请求值断言。mock CSRF会话标记及 `actorPassword` 字段名断言不是真实凭据，保持原语义。

原19项离线检查加凭据10项、最新runner契约4项共33项通过，209项需求映射检查通过。
第一次执行25项时24通过、1项旧runner字节保真检查失败，原因是新增接线；随后加入严格
数量/位置校验的恢复器，撤销精确新增片段后仍比对原始哈希，保留首次失败与重跑证据。
沿用 `make check` 的 `*_contract.py` 自动发现和既有CI套件，不增加job或权限。
这些是离线单元/静态检查；TypeScript/格式/DOM/浏览器/API/Worker/PostgreSQL、精确head
CI、发布和合并仍待完成。原冻结patch及原暂存区保持，新增改动单独待审。

### 2026-10-14 AD-F-189 专用任务通用控制隔离候选

Issue-first 记录见 [#12 comment6180459935](https://github.com/yunpiao/adtr/issues/12#issuecomment-6180459935)，
关联 [#26](https://github.com/yunpiao/adtr/issues/26)、[#30](https://github.com/yunpiao/adtr/issues/30)。
基线 main `7140933be2459dbfc4b824aa6d6d6c46a9f4f8da`、tree
`0a770734095ea8561f872c74e1234dba21e619f2`；编辑前已核对全部1084个blob与文件模式。

已确认缺陷：前端专用种类判断只覆盖目录v1/v2，遗漏
`domain.directory_read.v3`、`domain.directory_read.v4`、`user_assets.v3.export`、
`group_assets.v3.export`、`computer_assets.v3.export`。服务端已拒绝这些种类的通用
cancel/recover，但旧任务详情仍显示通用动作/证明表单，并将已脱敏的result/cursor显示为普通结果。
本候选按精确kind隐藏这些入口、密码/MFA表单及私有结果/游标，保留真实状态、进度、错误、
持久事件与刷新；内部TaskAction另设防护。只清理经当前详情确认属于当前actor/当前task的旧通用
recover意图，保留其他actor/task及专用导出恢复记录，不自动重放写入。

目录v3入口为`#directory-v3`；v4的`#group-members-v4`只读已保存结果，采集/取消仍需专用API。
三类资产导出分别指向对应`#*-assets-v3`页面的专用面板，不承诺按任意taskID自动恢复。
健康、审计、域连接既有合法动作及目录v1/v2/日志包限制保持，具体契约见
[任务工作区](soc-task-ui.md#2026-10-14-specialized-task-controls-candidate)。

候选验证目标包括五kind×九state的45组DOM负断言、无通用写入、恢复意图账户/任务隔离、
迟到响应、会话与导航回归，并扩展已有真实任务浏览器套件。沿用71项CI，不新增job；
无后端、schema、权限或策略改动。本地受执行限制，候选DOM/运行时、格式、类型/构建与真实
浏览器/API/Worker/PostgreSQL尚未运行；精确head全矩阵、独立审查、发布、合并及独立main-push
验证均待完成。已写测试和静态检查不算运行通过，任何历史CI均不继承到本候选。
显式白名单离线Python检查已通过19项（组成员证据校验器的合成单元、既有runner与字典3静态契约），
完整209项需求映射校验及`git diff --check`通过；审计钩子禁止子进程、网络和信号。
这些结果不证明新增105项DOM回归或11处真实浏览器检查点已执行。

AD-F-189保留历史较窄切片的`implemented_verified_slice`及原PR72/77/94证据，
本次仅追加candidate记录，不扩大已验证声明。Issue12/26/30保持开放，产品验收仍0/209；
源系统状态/字段、engineRestart、容量/保留期及真实AD/Windows验收仍开放。


### 2026-10-14 AD-F-052 本次观测对象数量候选

基线 main `d24a0cf304631d433b79f75dfd92038f6563826a`、tree
`bce5fdfd382e12a65983da0f64b5ca17f397639c`。Issue30 的
FLD-0586/0588/0590 仅推进同一完整有效字典3观测中的 user/group/computer
数量，按对象 kind 分类，在筛选与分页前计算；不可用为 null，成功空观测为三个0。
页面明确标记“本次观测中的对象数量”，绑定观察编号及来源，沿用当前读取权限与失效清理。
这不是旧系统资产总数口径验收；差异/安全风险概览、图表、真实AD和Windows兼容性仍开放。

沿用71项CI，不增加job，不修改schema/collector/工作流限制。
目前仅候选实现与离线白名单静态检查；精确head运行、独立复审、合并和main-push
证据仍待完成。历史CI不继承到本候选。详见[字典3契约](directory-v3-contract.md)。
AD-F-052 从not_started改为in_progress，Issue30保持open，产品验收仍0/209。

### 2026-10-14 PR164 组/计算机 snapshot 运行中取消合并证据

[PR164](https://github.com/yunpiao/adtr/pull/164)最终head
`a3ab990f39eb1b3d4e695dc497ecdbd84a0a942c`与实际main
`d24a0cf304631d433b79f75dfd92038f6563826a`共用tree
`bce5fdfd382e12a65983da0f64b5ca17f397639c`；main父提交依次为
`ec0da2018cc5914b8d0ca345c1c2c4cd5078d177`及上述head。
[精确head CI38607510864](https://github.com/yunpiao/adtr/actions/runs/38607510864)
attempt1为71/71；独立[main CI38615254322](https://github.com/yunpiao/adtr/actions/runs/38615254322)
attempt2为71个唯一检查全部成功。

历史失败保留：初始head395e9fc的run38599594145为69成功/2失败（计算机checkpoint及verify），
原始checkpoint失败原因尚未查明；后续仅补充诊断的新head通过。main attempt1的auth4
包在600.034秒超时并导致verify失败；同head针对性重跑auth4包398.612秒通过，
目标DirectoryV3测试22.59秒（PR为31.32秒）。源码及限制不变，耗时差异根因尚未查明。

fresh main组产物11921085467（SHA256
`47e9cfdfe6c15300e1924fd2d0c22b36073c9ddd84be2fa5b6759917a33ba2d8`）与计算机产物
11919813345（SHA256
`907afee286add99760ba8d701eeee9da3cc7626605acf0770c7e225357cb4e2a`）均独立复核通过，
覆盖1084仓库blob、各959/962来源hash及共6张PNG。证据限于两个kind的snapshot/progress10
运行中取消；chunk阶段取消、fencing竞争及完整产品验收仍开放。AD-F-053保持in_progress，
Issue30/151保持open，产品验收0/209；上述历史成功不转移到后续候选。

### 2026-10-14 组/计算机 snapshot 阶段运行中取消候选

本候选以已合并 PR163 的 main `ec0da2018cc5914b8d0ca345c1c2c4cd5078d177`、
tree `ec801e6ffc0bce09cd89714bb1be3586d2781fad` 为基线；独立
[main-push CI38588506730](https://github.com/yunpiao/adtr/actions/runs/38588506730)
为69/69。该历史成功不转移到本候选。仅增加 group/computer 两项独立 stable-body
running-cancel 浏览器切片，预期71项；可逆契约逐字节还原旧69项 runner/workflow。

目标为各自真实导出 snapshot/progress10 已提交后的 running → cancel_requested →
cancelled，固定组/计算机任务种类及来源。旧用户默认、queued 取消切片、生产代码、
schema、租约、超时、并行及 artifact allowlist 保持。详情见各自
[组契约](group-asset-export-v3-contract.md)与[计算机契约](computer-asset-export-v3-contract.md)。
候选精确 head CI、独立产物审查及 main-push 均待完成；本地只允许显式白名单、
审计钩子拒绝子进程/网络/信号的离线 Python 检查，不执行 Go/Node/PG/浏览器/build。
该范围不覆盖 chunk 生成阶段取消、fencing 竞争、完整字段/容量或真实 AD/Windows。
AD-F-053 仍 in_progress，Issue30/151 保持 open，产品验收0/209。

### 2026-10-14 PR163 用户 snapshot 阶段运行中取消合并证据

[PR163](https://github.com/yunpiao/adtr/pull/163)最终 head
`4d5bdcc4e29351ee49cc3f3d2d2ee8ad8e29462f` 与 main
`ec0da2018cc5914b8d0ca345c1c2c4cd5078d177` 共用 tree
`ec801e6ffc0bce09cd89714bb1be3586d2781fad`。
[精确 head CI38579256706](https://github.com/yunpiao/adtr/actions/runs/38579256706)
attempt2逐项69/69；独立[main CI38588506730](https://github.com/yunpiao/adtr/actions/runs/38588506730)
attempt1首次69/69。PR artifact11902942969（SHA256
`1956d254dfe0fd16911e473c52ca8f9fb728d78fcc225590edf99694ec0bec4e`）及 fresh main
artifact11907692240（SHA256
`bc5aefae6611c54291f4d1c8973d1ca87018031fbe51e57dd8075e1911417baf`）均独立审查通过；
main 审查覆盖1071个仓库blob、946个来源hash、2JSON和3PNG。
历史失败仍保留：head aeca058 的 run38571548413为67成功/2失败，真实浏览器通过但
新裁剪与旧全视口验证不符；之后修复3个测试文件。最终head的38579256706 attempt1
出现directory GET effective socket_hang_up，根因尚未查明；同head针对性重跑收敛后
attempt2为69/69。该证据仅覆盖用户 snapshot 阶段，不证明chunk取消或fencing竞争。

### 2026-10-14 用户运行中取消历史候选（当时尚未运行验收）

新候选基于已合并PR162的main `94b098d6acf5187e1ecf9aa659f5abed00122ce7`、tree
`0c669bd750c2ec137311aad08b6a05744ddff907`。仅增加用户导出真实running取消测试，
保留旧68项CI并增加1项，预期69项；不修改生产代码、schema或生产超时/租约。
候选目标是已持久化snapshot/progress10的running → cancel_requested → cancelled，
而不是把queued取消重新计作运行中取消。使用精确owned Worker、临时activity行锁、
全部PG锁等待校验、预备fresh MFA；停止前活租约须大于20秒，同Worker须10秒内CONT。
必须分别证明任务/事件/回执、无下载、取消后snapshot/manifest/chunk清零和同Worker后续活动。
详细门禁与失败清理见[用户导出契约](user-asset-export-v3-contract.md)。

当前仅候选实现，尚无本候选精确head CI、真实浏览器/PG运行或独立产物验收结果。
本地仅静态与明确白名单、无子进程/网络的纯Python检查；不运行Go/Node/PG/浏览器/build。
不证明chunk生成阶段取消或fencing竞争；group/computer仍只有queued取消浏览器证据。
AD-F-053保持in_progress，产品0/209，Issue30/151保持open。

### 2026-10-14 PR162 EXP-007合并证据补充

[PR162](https://github.com/yunpiao/adtr/pull/162)最终head
`09e1c52f8ceac2a2d85cdc8b4c71fbf515fa078a`与实际main合并提交
`94b098d6acf5187e1ecf9aa659f5abed00122ce7`共有tree
`0c669bd750c2ec137311aad08b6a05744ddff907`。
[PR CI38550970449](https://github.com/yunpiao/adtr/actions/runs/38550970449)
最终attempt3逐项68/68；独立[main-push CI38561279766](https://github.com/yunpiao/adtr/actions/runs/38561279766)
attempt1逐项68/68。fresh main产物11893251763/11893351368/11892781839独立复核通过，
包含940来源对象、216个实际XLSX单元格和6张PNG；范围为正常/空结果、失败恢复和queued取消。

历史失败保留：初始head74af592/tree791bfd的run38545054964 attempt1为54成功14失败，
其中8项编译sync.WaitComputer错误、4项旧membership权限目录预期、1项权限排序预期及verify。
修复后的38550970449 attempt1为66成功2失败（reader socket-hangup及verify），
连接中断根因尚未查明；attempt2实际reader/verify通过但最新rollup有queued重复记录，
当前attempt重跑及元数据收敛后attempt3为68/68。旧attempt job重跑403保留为失败操作，
不计作测试失败已消失的证明。新候选不得继承这些历史通过结果。

### 2026-10-14 EXP-007历史候选补充（当时尚未通过候选验收）

计算机导出候选以main `7ed455aecee1352f0261e7064253891af1c31c79`、tree
`bdfc54e0982cb07a4679c5fa022f9324da31832a` 为基线，保留既有65项检查，
计划增加正常/空结果、失败恢复、queued取消三项真实浏览器任务，预期68项。
仅五个dictionary3 scalar事实、固定单域观测、共享search/时间范围/creatTm
及全部匹配行；独立computer_asset_exports权限、computer_assets.v3.export任务
与schema23三表。详见[冻结契约](computer-asset-export-v3-contract.md)。

候选实现整合、独立实际diff审查、精确head CI和XLSX/PNG/数据库证据均待完成。
本地仅做静态、gofmt及明确白名单的无子进程/网络Python检查，不声称本地
Go/Node/PostgreSQL/浏览器/build通过。后续发布必须另记实际候选head、PR检查、
合并tree/父提交及main-push结果；历史成功不能作为新候选成功。
AD-F-053继续in_progress，Issue30/151保持open，产品验收0/209；完整旧字段、
DNS/IP、真实AD/八版本Windows、容量和生产门禁未由本候选完成。

### 2026-10-11历史组合状态

当前已合并基线 `6f7f44a4997f78b6a50eab1ba657ffcc15769c1a` 为schema18，
[main-push CI 38154440236](https://github.com/yunpiao/adtr/actions/runs/38154440236)
attempt1逐项36/36成功。全部209项产品验收仍为 **0/209**；机器台账为
**19 implemented_verified_slice、31 in_progress、159 not_started**。19指具名工程切片
状态的需求数，不是完整验收功能数。只有AD-F-157/158/159从not_started改为in_progress。

- F12：PR81独立directory v2生产者、schema16、四类新增事实、API/UI已合并并实测；AD-F-034保持in_progress，完整字段/增量/删除语义/地址关系仍开放
- F14：PR92真实SOC用户队列/固定详情、PR114精确SAM/创建时间范围排序已交付；AD-F-038/039保持in_progress，旧语义全量兼容/关系/业务导出未完成
- F40：PR104/106/109/113/116形成真实health消息、固定查询/计数、单条/批量已读原操作恢复和取消余项；旧类型/appName、更多生产者及完整分组时间线仍缺
- B2：PR103/110已补真实跨账户、无域B、双通知失败回退和真实正文消费后的迟到应用交付；保留原生取消证据，不把应用交付当取消后网络送达
- SOC任务/审计/健康工作区分别由PR94/98/102交付；PR97是CI工程优化，不增加产品验收数量

每个工程切片的精确head、PR CI、实际合并提交、main-push CI、Issue范围与剩余门禁见
[本次逐项核对](delivery-reconciliation-20261011.md)。真实AD/八版本Windows、完整源兼容、
容量/恢复和生产门禁仍独立开放；合成LDAP、保护预算、CI成功或工程Issue状态均不替代验收。
本次没有部署、关闭Issue或勾选未验收项；基线成功不代替本次文档提交的精确CI。

## 历史检查点阅读约定

以下保留原有本地与发布检查点。“开发中”“尚无UI”“CI待完成”描述该节当时范围；
后续已交付部分以上方当前组合状态和逐项核对为准。原失败、缺Docker/DSN及较窄测试边界
继续保留，不追写为当时已通过。

## F40 真实 health 消息后端首片（2026-10-11 开发中）

Refs [#101](https://github.com/yunpiao/adtr/issues/101)、[F40 #50](https://github.com/yunpiao/adtr/issues/50)，基线main `f9b8ed1`，迁移17。仅真实health终态投影、本人固定查询/阅读快照、实时权限与持久单条已读收据；纯messagecore不变。UI/完整时间线/批量进度/完整F40仍开放，产品验收0/209，无部署。

未修改基线的messagecore/auth/store/cmd单元通过；初版及审查修复后的最终候选完整make check均已通过1311项DOM、98项Python、Go race/vet/build、类型检查及依赖审计0。新增认证/存储集成测试已通过integration-tag编译，但真实PostgreSQL实际尝试因ADTR_TEST_DATABASE_URL缺失失败，本地无docker/psql；不计为集成通过。独立实际diff复核发现并修复纳秒SQL边界与持续新增扫描公平性问题，补充回归后未见剩余阻断项；真实链门禁及精确head CI继续逐项核验。[运行时契约](message-runtime-contract.md)说明来源、安全预算、恢复与未完成范围。

## SOC 生产任务工作区（2026-10-10 开发切片）

Refs [#93](https://github.com/yunpiao/adtr/issues/93)、[F48 #12](https://github.com/yunpiao/adtr/issues/12)、AD-F-189、设计 #87。基于 main `a8cc62a` 和已合并 PR89/PR92，制作真实任务紧凑队列、完整执行详情和持久化事件时间线。只使用现有任务读取字段；后端和全部既有写入/证明/幂等/专用任务边界保持。

列表返回保存已应用筛选、页码、任务焦点和滚动；刷新与浏览器前后退由 App 重新核验身份后恢复严格校验的账户绑定只读状态。不保存凭据、私有响应或操作草稿。没有当前权限的已归档/全部可见性不能自动恢复查询。

未改基线完整 make check 通过1043项前端测试、60项Python、Go race/vet/build、类型/构建和依赖审计0。最终候选完整 make check 通过1298项前端测试及上述全部门禁；全量格式、31项浏览器用例发现和原型27项静态/VM检查通过。独立实际diff复核通过，精确head CI及真实1440×900/390×844图像证据在关联PR逐项记录。本地容器集成/生命周期/真实任务浏览器实际尝试因缺少docker阻塞，不算通过；完整远程CI门禁保留。

[任务界面与导航契约](soc-task-ui.md)记录数据、恢复、安全及测试边界。F48 #12、设计 #87及更广功能保持开放，产品验收0/209，无部署。

## SOC 生产用户资产界面（2026-10-10 开发切片）

Refs [#91](https://github.com/yunpiao/adtr/issues/91)、[F14 #30](https://github.com/yunpiao/adtr/issues/30)、AD-F-038/039。按用户确认的 PR89 当前 SOC 原型制作真实生产导航与用户资产列表/详情；用户已自行合入 PR89，开发分支正常整合 main `665fcf7c2ab7373d9e8e9a7a4e289ce8a121a527`。不改原型文件、后端接口、数据授权或默认开关。

未改基线 make check 1017 项前端测试通过；初始候选完整 make check 1042 项前端测试、60 项 Python 契约、Go race/vet/build、类型/构建、依赖审计0通过。独立实际 diff 审查发现并修复同身份会话核验后的模态背景惯性所有权冲突与 StrictMode 焦点重放问题，实际 App 复现和119项针对性测试通过。整合 main665fcf7 后完整 make check 再次通过1043项前端测试及全部上述检查，原型27项静态/VM检查通过。真实 API/Worker/TLS LDAP/PostgreSQL、桌面/390px截图和完整逐提交 CI 待关联 PR 的精确提交运行结果；本地没有 Docker，Chromium 受 OS socket 限制，不把 DOM 测试计为浏览器通过。

[范围与状态契约](soc-production-ui.md)记录设计依据、真实接口、模态/来源/会话边界和测试迁移。原型风险、关系与告警样本不作为真实结果。产品验收仍0/209，F14 #30保持开放，无生产部署。

## F14 用户资产搜索与固定详情（2026-10-10 开发切片）

Refs [#30](https://github.com/yunpiao/adtr/issues/30)、AD-F-038/039。基于已合并
main `26f1463e98d2064182ad3f89e8663af7a0d9ef24` 开发只读 `/api/user-assets/v2`
用户列表和 `/detail`，使用同一经过完整校验的 dictionary2 不可变观测；当前域与凭据
版本在同一授权事务核对。支持 SAM/SID/mail/DN 字面量搜索、用户专属稳定分页、
观测/GUID 固定详情；十个事实字段保留真实 null/0/UTC 年份与原始文本。
范围、原始字段映射、错误/重试及剩余门禁见 [用户资产契约](user-assets-v2-contract.md)。

本地清洁基线 make check 通过759项DOM；新增候选完整 make check 已通过845项DOM、
Go race/vet/build、58项Python契约和依赖审计（0漏洞）。全部非认证集成包已通过本地
真实PostgreSQL17.6；新增域查询/HTTP测试独立覆盖实际持久化、撤权、固定观测、
完整损坏拒绝及来源变化。最终逐提交检查与独立实际diff审查结果另附PR证据。
四个认证集成分片、最终源码复核、精确head CI和当前main兼容性仍分别核验。

Docker容器版集成/生命周期脚本在当前dot云执行环境实际尝试后因没有docker可执行文件
阻塞；不能将本地PostgreSQL版本的成功当作容器门禁通过。新增两个真实浏览器套件
`user-assets-v2`、`user-assets-v2-readers` 已登记CI，需在精确head运行后才记录成功。
其独立合成TLS LDAP模式包含12用户及组/计算机干扰项；原v1/v2夹具未改变。
网页截图、原生跨标签撤权及Worker→TLS LDAP→PostgreSQL→API链路依其实际CI结果验收。

没有schema迁移、生产部署、真实AD/凭据接入、默认开关或权限放宽。AD-F-038/039仅从
not_started改为in_progress；19 verified slices、28 in_progress、162 not_started，
产品验收仍0/209。其余字段/筛选、风险/登录/关系/导出、源系统1:1、真实AD/八版本Windows、
容量与生产门禁仍开放；不关闭Issue30，也不以本切片宣称完整F14或依赖功能已验收。

## 历史合并与运行证据（2026-10-09）

[PR #81](https://github.com/yunpiao/adtr/pull/81) 的精确 head `d6b6063f5fa5407779f465789e652a3dc24ff7c2` 已在 [CI 37945689472](https://github.com/yunpiao/adtr/actions/runs/37945689472) completed/success，28/28 job 通过：22 个隔离真实浏览器/API/PostgreSQL套件、4 个认证集成分片、contracts 和最终 verify。新增 directory-v2、directory-v2-controls、directory-v2-readers 均通过，目录生产者使用真实 Worker 与隔离合成 TLS LDAP；未验证真实 AD/Windows。

实际 main 合并提交为 [`4faafb552a5c7887a62930f7563b22d3cbcdfbf9`](https://github.com/yunpiao/adtr/commit/4faafb552a5c7887a62930f7563b22d3cbcdfbf9)，内容树 `69faa19f15cb7da6a937399340a1d8dc454614b8` 与已审查/测试的 PR head 相同；父提交按序为原 main `9d31a7308c6dfb8b7ef0ce0c97b2f59fe89e7a46` 和 PR head。该合并提交的 [main-push CI 37954766457](https://github.com/yunpiao/adtr/actions/runs/37954766457) 已在 attempt 1 completed/success，28/28 job 全部通过。这是上述实际 main 提交的独立合并后验证结果，未用 PR CI 成功替代。

本次仅更新发布、合并和运行事实；不修改需求状态或验收计数。产品验收仍为 **0/209**，保留 19 implemented_verified_slice、26 in_progress、164 not_started。[F12 #25](https://github.com/yunpiao/adtr/issues/25)、[F14 #30](https://github.com/yunpiao/adtr/issues/30) 及其更广依赖仍开放；v2 的默认关闭开关与独立授权保持。增量/删除语义、完整资产页面/关系/业务导出、源系统字段兼容、真实 AD/八版本 Windows、容量/恢复及生产门禁不因本次合并完成。没有生产部署。

下面有日期的历史记录保留原结果及证据身份；“尚未推送”“未通过门禁”等描述对应当时检查点，不表示当前合并状态。v2 最新范围和具体证据见 [directory-v2-integration-status.md](directory-v2-integration-status.md)。

## 历史集成运行证据（2026-10-08）

[PR #77](https://github.com/yunpiao/adtr/pull/77) 精确 head `4f64d90880b28b3cf7a749a9ba1da57049736dd9` 的 [CI 37772558021](https://github.com/yunpiao/adtr/actions/runs/37772558021) 已 completed/success，25/25 job 通过：19 个隔离真实浏览器/API/PostgreSQL套件、4 个认证集成分片、contracts 和最终 verify。contracts 包含 make check、真实 PostgreSQL 非认证包集成，以及 API/worker 容器生命周期。域检测/目录读取使用隔离合成 TLS LDAP，未验证真实 AD/Windows。

本条是上述固定提交的运行证据；main 目标与合并后验证另行记录。下方有日期的本地检查点及旧版本契约保留原文；其中当时缺少本地数据库/浏览器、尚未发布或未运行远端 CI 的说明不再代表当前远端执行状态。F12 的 directory、directory-controls、directory-readers 已分别通过非空分页、真实空结果/取消/结束确认、只读角色/跨标签原生焦点回退场景。F38 的 Audit/XLSX 历史和 F56 的实际 api/worker 日志包链路也已执行通过。

上述固定检查点的应用 schema 为 15，dictionary2 尚未注册。后续本地 schema16/v2 整合的实现与未通过门禁另见 [directory-v2-integration-status.md](directory-v2-integration-status.md)，不能沿用旧检查点的 CI 作为其运行证据。PR74/75/76尚未整合。

本次台账仅将 AD-F-151/152 从 not_started 改为 in_progress；计数为19 implemented_verified_slice、26 in_progress、164 not_started。产品完整验收仍为0/209，源系统完整兼容、真实AD/Windows、容量与生产门禁仍开放；Issue 关闭与部署不因本次 CI 通过自动完成；代码按下方已授权的 PR 验证与合并流程推进。

## Dictionary 2 本地整合（2026-10-08）

独立用途、schema16、真实有界读取器、受保护 API 与界面已接线，部署默认仍关闭。完整本地检查及隔离 PostgreSQL17.6 的非认证集成组、四个认证分片共243项测试通过；本地修复尚未推送，真实浏览器与精确提交 CI 仍须通过；没有据此增加产品验收计数或宣布 F14 业务资产页面完成。具体命令、已修复审查问题和剩余门禁见 [本地整合检查点](directory-v2-integration-status.md)。

## 基线（2026-10-07）

初始提交 c47bc370b31f2511201c43be2976d1d06e192ae7，仅 README。
没有源码、构建配置或测试，因此基线是「测试不存在」，不是「测试通过」。
云环境 Node v24.19.0/npm 11.9.0 可执行；Docker 28.4.0/Compose 2.40.3 服务可访问。
系统 `/usr/bin/go` 是同名围棋游戏，不能用作 Go 编译器；开发工具链必须独立校验安装。

## 未决输入（G01 保持未完成）

| 输入 | 已知事实 | 解除条件 |
| --- | --- | --- |
| 原始工作簿 | dot 云电脑已读取2026-10-03工作簿原文，SHA-256见 requirements/catalog.json | 文件可读阻碍已解除；逐业务字段冻结与源系统运行兼容核验仍按功能契约进行 |
| 209 编号映射 | 已建立65个规划Issue：57功能组、7门禁和1路线图；209项唯一映射已核验 | requirements/catalog.json 纳入CI检查209项、3258字段与804控件引用；实现验收不能由编号覆盖替代 |
| 三套参考实现 | 工作簿含部分参考分支/提交标签，且采集器快照标记 dirty；尚未取得完整可复现源码、未提交差异与发行构建证据 | 取得完整固定内容，逐项记录差异、采纳/拒绝理由与迁移风险 |
| 字段允许矩阵 | 类型/默认/空值/多值/运算符/时间单位/排序/页长/弃用均待原文 | 每字段可定位到来源和测试，不由 UI 猜测 |
| 容量 | 域、DC、对象、ACL、EPS、保留期、P95、导出量未知 | G01 冻结目标及测量口径 |
| 恢复目标 | RPO/RTO 未知 | G06 冻结并真实演练 |
| 规则 | 专项算法、内容与可判定夹具未收到 | G04 验证外部输入 |
| Windows/AD | 八版本实验机和授权范围未收到 | G05 明确实验资产、工具链和 72 场景 |

需求台账接收后的每行必须保存 AD-F 编号、源 sheet/行、原文、验收原文、唯一归属 Issue、
输入/结果/权限/失败/边界案例、自动/人工测试位置、PR、CI head SHA 和产品验收状态。
未读到的原文不以空白占位行宣称覆盖。优先级与前置依赖分别存储。

## 每次开发与完成标准

1. 读取 Issue、适用 AGENTS 和依赖；记录当前测试基线，先定跨模块接口。
2. 在开发分支实现一个可独立验收的切片并创建草稿 PR；不得直接推 main 或部署生产。
3. 对实际变更测试正常、异常、权限边界和回归；不得删除测试、放宽阈值或将 skip 当通过。
4. 完成后单独审查实际 diff，检查凭据泄露、授权范围、竞争条件、失败语义及文档一致性。
5. 草稿 PR 关联 Issue，记录命令、结果、head SHA、风险与未完成项。部分工作用 Refs，不能提前 Closes。
6. 读取最新 head 的 CI 结果；CI 不可访问时明确阻塞。外部独立审查在可用时记录审查人和 commit，
   自审不能冒称独立审查。全部子项与产品验收未完成的 Issue 不关闭。
7. 按用户已授权的 Issue → PR → 验证 → 合并 main 流程推进。独立审查及精确 head/main 目标的全部必需检查通过后，核验 base/head 未变化，再正常合并；目标变化则重新验证，不绕过服务端规则。
8. 合并后核对 main 的内容树与父提交并完成 main-push CI；失败时继续诊断修复，不把合并、单次 CI 或合成实验结果当作产品全部验收。生产部署和真实凭据/AD操作仍不包含在本流程中。

CI 默认无权限，各 job 仅授予必要 contents: read；actions 固定完整 commit SHA，
不向 PR 执行暴露生产凭据，不使用 pull_request_target 执行不可信分支代码。
运行命令只在实际执行成功后写入根 AGENTS，不能填占位命令。

参考规范：
- https://learn.chatgpt.com/guides/best-practices
- https://docs.github.com/en/copilot/tutorials/cloud-agent/get-the-best-results
- https://learn.chatgpt.com/docs/agent-configuration/agents-md
- https://docs.github.com/en/actions/reference/security/secure-use


## dot 云电脑执行与分层验证（2026-10-07）

切换后在 dot 云电脑重新物化远端源码；官方 Go 1.27.1 校验安装，本地无 Docker。
PR #6 TLS与同容器重启审查修复提交 af1ad3d2b766df4c1ef8a71b9ade58c8d05c6d7e，
本地 make check 及 GitHub CI 的真实 PostgreSQL/容器生命周期均通过。

PR #68 的 F43/F44 提交 c77878d2d090a32c776fc29126971709cd2b3afd：本地 Go race/vet/build、
20个DOM测试、TypeScript/build和依赖审计通过；独立审查后修复审计归属与TRUNCATE缺口。
GitHub CI https://github.com/yunpiao/adtr/actions/runs/37573272781 已通过真实数据库迁移、
认证状态/权限/并发回归、容器生命周期和真实浏览器→API→数据库认证链路。
逐AD-F已验证部分与未决门禁记录在 requirements/catalog.json；当前产品完整验收仍为0/209。
代码、真实链路测试、源系统1:1兼容、生产验收、合并和部署分别记录，不能互相替代。

## F01/F03 B2 本地账户引用与执行结束证明（2026-10-07）

本地实现三种互斥来源：独立凭据、已登记操作账户、未配置。新增未配置建域入口避免重复录入 AD 凭据；绑定要求当前角色的显式使用授权，任务固定为单次连接测试，既有自定义凭据 v1 含义保持不变。契约见 [account-reference-contract.md](account-reference-contract.md)。

数据库保留 reserved/opened/quiesced 使用记录与依赖。执行器实际返回并完成资源清理后，任务引擎才持有独立清理凭据；失败的确认在进程内保留、限量重试并阻止该执行槽继续领取。任务终态、授权撤销、租约过期和来源解绑均不能释放已打开使用。进程崩溃失去确认后继续阻止账户更换/移除，尚无强制释放入口。

已执行本地 make check、Go 全包 race 测试、集成标签编译/vet、5 项 Python 契约检查；最终界面修正后默认及双 worker 的380项DOM测试、类型、构建与格式检查通过；源接口、审计投影与维护循环有针对性测试。实际 PostgreSQL 的迁移、并发/回滚、HTTP/LDAP 与真实浏览器测试已补充代码，当前云环境未运行这些阶段。新增 CI account-references 浏览器任务使用隔离 LDAP/数据库和真实 worker，仍待远端执行。独立源码审查发现的 Finish 与未打开预留清理竞态已修复并补回归；不把源码审查或编译算作运行通过。通用跨标签身份切换及已打开使用的停止确认延迟场景现已另行编写，均待真实执行；B2特定来源跨账户切换仍缺独立浏览器场景。已有DOM/数据库集成用例不替代这些浏览器证据。

迁移13遇到旧版本已经使用新保留任务类型或依赖类型的未知记录时原子拒绝，保留数据和版本，不推断用途、不删除阻断。此项及所有功能的产品验收维持未完成，0/209 不因本地代码或测试增长而改变。

## F38 审计导出历史（本地实现，2026-10-07）

关联 #26、AD-F-151/152。复用实际 audit.export 执行器和 XLSX 字节，不新增模拟导出生产者或 schema 迁移。新增历史列表在当前账户、权限版本、全部快照域和归档边界过滤之后分页计数；任务状态和元数据可下载条件分别展示，下载继续验证完整数据及摘要。细节见 [export-history-contract.md](export-history-contract.md)。

本地 make check 通过：Go race/vet/build、397项DOM测试、类型/构建及5项Python契约检查，依赖审计0漏洞。全部集成标签编译/vet通过；独立源码审查已核对权限、时间精度、导航/跨账户保护及测试修正。9项实际迁移HTTP/数据库用例含真实XLSX字节/摘要、分页/作用域、损坏元数据、自然过期及并发撤权；真实PostgreSQL/浏览器执行尚未在当前环境运行，不以编译或DOM测试代替。Alert、Bashline、System、Leak、appType、其他格式和源系统完整兼容仍未实现；F38及209项产品验收维持开放。

F05 的字段证据未给出完整安装协议或发行包选择/信任契约，且没有实际可分发采集器字节、可复现 dirty 源码快照、域控身份/OS清单及八版本 Windows 实验环境。固定 LDAP 连接测试授权不能用于安装；安装、远端效果/取消/重试和真实发行包验收继续阻塞。

## F56 本应用运行日志与诊断包（本地实现，2026-10-07）

关联 #55、AD-F-205。已实现固定枚举的 api/worker 事件来源、128项非阻塞队列、明确拒绝/未知确认计数、安装租户限定查询及实际 ZIP/JSONL 打包执行器；启动请求不表示监听成功，空轮询完成不表示业务任务完成。打包在执行时冻结可见事件，最多10,000条/16MiB，完整校验后才响应字节。契约见 [operational-log-contract.md](operational-log-contract.md)。

迁移14先在独占迁移门禁下拒绝所有 opened 凭据使用，不能用任务终态、过期或租约丢失替代结束证明；拒绝后回滚门禁让旧版本执行器完成确认。新任务类型与旧未知类型冲突也原子拒绝。新增 system_logs 权限默认拒绝自定义角色，system/system_logs 变化都会更新授权版本。

最终本地 make check 通过：全包Go race/vet/build、430项DOM测试、类型/构建、5项Python契约检查及依赖审计0漏洞；全包集成标签编译/vet和新增通用任务脱敏单元回归通过。七组实际迁移HTTP回归已编写并编译，执行明确因缺少 ADTR_TEST_DATABASE_URL 阻塞；真实浏览器尝试停在缺少 Chromium 可执行文件，未进入API/数据库验收。独立后端、HTTP与界面审查已完成，通用取消绕过控制审计、失败版本门禁可能写入失败审计、TOTP测试窗口与两个异步界面测试同步缺口均已修复；断言、超时和默认并发未放宽。缺失历史模块、任意文件日志、网络诊断及源系统完整兼容仍未交付；0/209产品验收不变。

## 跨标签会话隔离与未知结果恢复（本地实现，2026-10-07）

修复另一标签退出/登录后旧界面继续显示原身份的问题：同源通知只传随机nonce，收到后先移除私有界面，再读取真实会话；通知不可用时由窗口焦点和可见性补核验。核验期间隐藏并禁用私有内容，身份未变则保留草稿；导航、延迟响应及失败核验不能恢复旧身份。任务/审计/维护/域检测的未知结果恢复记录按actor保留，CSRF轮换或另一账户访问不再销毁原幂等键，也不自动重放写入。

本地完整 make check 通过482项DOM、Go race/vet/build、5项Python契约和依赖审计0漏洞；独立源码审查完成。真实跨标签浏览器测试已编写并通过类型/发现检查，但当前环境的实际浏览器/API/PostgreSQL执行未通过。B2已打开凭据使用的浏览器屏障测试仍为契约方案，不能据此声称完成；全部209项产品验收仍保持原有边界。详情见 [session-invalidation-contract.md](session-invalidation-contract.md)。

## B2 执行器返回确认延迟的浏览器验证（本地测试实现，2026-10-07）

新增独立真实链路场景：真实账户引用任务通过TLS/绑定/RootDSE后成功，测试助手仅锁住实际opened使用记录，使生产停止确认暂时待处理；随后真实解绑成功，替换/移除精确返回account_in_use，再释放测试行锁并等待真实executor_returned确认及依赖清零，之后重试成功。助手不写生命周期状态；它验证“执行器已返回、确认尚未持久化”的保护，不能被描述为解绑时网络操作仍在运行。固定2秒LDAP门闩和60秒行锁保护仅存在于集成测试程序，生产权限与超时未改。契约见 [test-account-barrier-contract.md](test-account-barrier-contract.md)。

完整本地 make check 通过482项DOM、Go race/vet/build、9项Python契约及依赖审计0漏洞；全仓集成标签编译/vet通过。LDAP真实TLS/BER race通过（6.781秒），助手协议/协调单元race通过，浏览器类型/格式/发现通过；独立源码审查已完成。实际数据库组件测试尝试因缺少ADTR_TEST_DATABASE_URL明确失败，真实Docker/PostgreSQL/浏览器仍未运行通过。组件测试使用明确标注的合成打开执行器，不能替代实际账户执行器/LDAP证据；B2特定来源跨账户场景仍未单独完成。产品验收边界和0/209不变。

## F12 有界目录观测字段基础（本地纯模块，2026-10-07）

关联AD-F-034。已实现独立字段解码和页累计：objectGUID二进制转换、用户/组/计算机类别、缺失值与零值区分、单值/多值及ASCII描述符校验、明确对象/页/字节/cookie上限，任何失败都阻止输出部分观测。cookie保持opaque且允许重复；缺席不代表删除，本地批次不代表AD时间点快照。见 [directory-observation-contract.md](directory-observation-contract.md)。

全包Go lint/race/vet/build、9项Python契约、最终模块race/vet通过；最终两秒fuzz通过43,432次执行。独立审查发现的分配前上限和Unicode属性名问题均已修复。该检查点尚无LDAP生产者、新读取授权/账本、持久化或界面，不能视为资产同步已交付；AD-F-035增量、AD-F-041地址关系及Windows实测仍开放。下一安全边界见 [directory-authority-plan.md](directory-authority-plan.md)。

## F12 目录传输与用途授权组件（本地实现，2026-10-07）

在字段基础之上新增独立目录读取传输、默认关闭的部署开关及 `domain.directory_read` 用途授权组件。读取固定字段和过滤器，逐页重新授权，严格 TLS/RootDSE/域范围与 BER 控件校验，并在失败、取消和 panic 后清理响应及 cookie。旧连接检测的超时和授权含义保持；用途 SQL 仍是未安装片段，消费者仍关闭。细节见 [directory-component-contract.md](directory-component-contract.md)。

恢复后的云端源码通过完整 `make lint test build`：全包 Go race/vet/build、格式与需求校验及9项Python契约。合成 TLS/BER 的完整传输 race 回归、26,586次 BER fuzz 和独立清理修复复审通过；用途授权与配置独立源码审查未发现阻断缺陷。组件数据库用例已编写，实际 PostgreSQL 执行仍阻塞；真实 AD、浏览器和产品生产者未运行。该增量没有安装新迁移、登记目录任务或实现账本/持久化/API/UI；0/209产品验收不变。

## F12 独立任务身份与凭据生命周期组件（本地未接线，2026-10-07）

新增独立 `domain.directory_read` 任务固定字段、当前用途授权检查和 reserved/opened/quiesced 账本片段，不复用连接检测的最新任务或诊断代次。任务/依赖/用途 SQL 仍未安装，未登记消费者。结束确认只接受实际任务引擎发出的原执行器返回证明，终态/撤权/解绑/租约失效不能释放 opened 使用；从未打开的终态预留另行清理。见 [directory-task-component-contract.md](directory-task-component-contract.md)。

domains/auth/store race、正常vet、全仓集成标签编译/vet通过；新增6组数据库组件用例实际调用均因缺少DSN停在初始化。合成执行器不解析凭据、不访问网络，不能替代实际目录生产者证据。迁移预检/接线、执行器、存储、API/UI与真实数据库/浏览器/AD仍待完成，产品验收0/209不变。

## F12 目录执行器与完整观测暂存组件（本地未接线，2026-10-07）

新增调用真实有界读取器的目录执行器：确认打开事务提交后才解密，每阶段/页/最终暂存重新检查当前用途授权，固定单次执行与清理证明，通用任务结果保持空对象。新增不可变观测暂存片段、字典与域范围校验、总字节限制、摘要和规范字节校验；AD返回的大小写/根点DNS名称先规范化。详见 [directory-producer-component-contract.md](directory-producer-component-contract.md)。

完整本地 `make lint test build`、全仓集成标签编译/vet通过，包括全包race和9项Python契约；快照规范化/边界race通过，独立源码审查发现的主机名兼容缺口已修复。执行器测试明确使用事务替身并验证真实读取器首次授权拒绝，尚未执行成功的目录读取→数据库发布链路。SQL片段、消费者与读接口均未安装/登记；迁移、权限接口、成功任务可见性与取消竞态、真实数据库/浏览器/AD验收仍开放，0/209不变。

## F12 本地整合检查点（迁移15、界面及真实链路用例，2026-10-07）

关联 #25 / AD-F-034。整合独立用途授权、目录任务与账本、默认关闭的出口开关、完整观测暂存、当前域权限下的稳定分页、提交/回执/取消接口和实际前端。数据读取权限独立于凭据使用权限；只有成功任务且仍匹配当前连接/绑定/策略的观测可见。取消接受与实际停止证明分开，旧连接检测语义不变。迁移15原子拒绝保留类型冲突及尚未停止的旧使用，未来升级同时检查两类账本。

最终完整 `make check` 重试通过：全包Go race/vet/build、9项Python契约、556项DOM测试、类型/构建与依赖审计0漏洞。此前544/545失败由异步测试在请求尚未派发前读调用记录造成，已增加真实派发等待；没有删断言或放宽超时。独立审查发现并修复普通对象原型键导致错误提示崩溃、心跳导致取消表单重建清空证明，以及串行维护共享时限可能使目录预留清理饥饿；修复均有回归与复审。迁移、API/用途隔离及数据库用例源码审查未留已确认缺陷。

合成LDAP夹具显式目录模式返回30对象/2页，真实TLS/BER race通过（7.237秒），包含生产读取器的两种TLS模式、严格控件/cookie/边界和旧默认模式回归。此证据使用隔离传输，不代表完整数据库/浏览器或真实AD。新增迁移、用途、账本和HTTP数据库用例已编译/vet并实际尝试；均因缺少DSN停在初始化，未跳过算通过。

真实浏览器用例已准备并纳入独立CI矩阵：实际API/worker/合成TLS目录/PostgreSQL链路，另用真实提交成功后仅丢失浏览器响应的故障注入验证原回执恢复，无伪造API数据。当前仅完成类型/发现检查及缺夹具明确失败，尚未运行成功。真实空观测界面、完整取消生命周期、只读角色和跨标签场景的浏览器证据仍开放；已有DOM/数据库用例不能替代。全部旧CI套件与最终verify门禁保留，浏览器最大并行仍为2。

AD-F-034继续in_progress；AD-F-035增量及AD-F-041地址关系未由此交付。完整字段、容量、源系统兼容、八版本Windows/真实AD、远端CI及产品验收仍开放；验收0/209。

## F12 空观测与取消真实浏览器用例（本地测试切片，2026-10-07）

在独立工作树补充真实空观测、运行中取消及凭据依赖解除的浏览器用例；通过5页固定延迟的合成目录响应观察真实心跳/页进度，验证密码及验证码草稿不会被刷新清空，再用当前未使用验证码请求取消。SQL观察器只读自己创建的数据库，等待实际executor_returned及依赖归零，不写任务/账本状态、不伪造结束证明。原30对象用例、生产超时、权限与最大CI并行2不变。

最终本地make check通过：Go race/vet/build、15项Python契约、559项DOM/单元测试、类型/构建与依赖审计0漏洞；集成标签编译/vet通过。合成TLS夹具race通过25.199秒。独立审查修复了验证码草稿覆盖不足、晚到响应解析错误可能漏报、以及Docker创建超时后遗漏清理的缺口；容器清理现在核对唯一标签并按不可变ID操作。用例类型/发现与缺夹具明确失败已检查，真实Docker/PG/浏览器仍未运行通过。测试代码不代表验收；0/209不变。

## F12 只读角色与跨标签真实浏览器用例（本地测试切片，2026-10-07）

新增独立 directory-readers 套件，用真实界面与 API 创建仅有 domains.read、directory_assets.read 的角色，并读取真实成功的空观测。角色不含任务、账户元数据、写权限或凭据使用授权；负向请求使用实际会话、CSRF、密码与当前验证码。跨标签场景延迟真实管理员响应，实际退出并登录只读账户后再释放，同时覆盖原生通知与通知不可用时的真实聚焦/可见性复核。没有伪造响应体或改写任务账本。

最终完整 make check 通过15项Python契约、559项前端测试及Go race/vet/build、类型/构建、依赖审计0漏洞。独立审查发现并修复延迟响应终态分类不够严格，以及三个目录套件设置动作超过生产敏感操作限额的问题；真实创建的两个管理员分担设置和生产动作，保持原限制。最终源码复核无已确认遗留缺陷，三个用例类型/格式/发现检查通过，新套件缺夹具时在浏览器启动前明确失败。真实 PostgreSQL/浏览器/AD 执行尚未通过；测试代码、类型检查与测试发现均不代替验收。原有套件、生产权限、CI 最大并行2及最终 verify 门禁保持。产品验收0/209。

## 浏览器设置动作限额修复（本地测试切片，2026-10-07）

对15个非目录套件按实际HTTP处理顺序核算，发现操作账户、账户引用、引用确认屏障、周期任务和审计五个套件会在原单一操作者的第11次敏感动作触发429。业务事务回滚不会清除单独提交的限流计数，重新登录或增加标签也不会重置它；重放是否计费按各处理器的真实顺序区分。

修复用真实界面创建并启用独立操作者，或复用屏障中的既有管理员分担绑定，保持原10次/15分钟限制。原任务、回执、导出和重放所有者保持一致，所有原断言及超时保留。五个文件最终类型、格式及发现检查通过，独立源码复核无已确认遗留问题；完整make check通过Go race/vet/build、15项Python契约、559项前端测试、类型/构建和依赖审计0漏洞。真实浏览器/PG/AD仍未运行通过，源码检查不能替代验收。详见[browser-sensitive-budget-contract.md](browser-sensitive-budget-contract.md)。

## F14 补充目录字段字典（纯组件，2026-10-08）

按工作簿与官方协议新增独立 objectSid、mail、description、whenCreated 解码组件。SID区分通用结构与AD属性上限；文本保留实际UTF-8内容，描述保留多值；创建时间只支持明确的UTC秒级子集。缺失、空值、超出本地表示范围与畸形值分开，不默认补零、不推断登录、锁定、风险或关系。现有目录请求、Object、存储/API及授权保持，运行时尚未采集新增字段。

最终make check通过Go race/vet/build、15项Python契约、559项前端测试、类型/构建及依赖审计0漏洞；SID fuzz180,534次、补充字段fuzz110,861次通过。精确旧JSON/LDAP字节、旧解码器拒绝新字段及既有规范存储回归通过。独立审查修复闰秒语法被误标为畸形值的问题，最终复核无已确认遗留缺陷。版本化生产者/存储/接口/界面集成、完整字段语义与真实DB/浏览器/AD/Windows均仍待完成，产品验收0/209。详见[supplemental-directory-fields-contract.md](supplemental-directory-fields-contract.md)。

## 字典2编解码与传输组件（本地未接线，2026-10-08）

新增固定八属性目录读取、原始补充字节/公共字段分离的编解码、完整观测封装和有界累积器。复用原TLS/授权阶段/分页循环，保留字典1字节与行为。补充值以base64原始字节进入规范存储候选体，保留NUL等文本；尚未写入数据库。新类型不代表新授权，没有登记任务、迁移或接口。

完整make check通过Go race/vet/build、15项Python契约、559项前端测试、类型/构建与依赖审计0漏洞；全仓集成标签仅编译/vet通过。真实进程内合成TLS/BER两模式及旧回归通过，完整封装/旧观测race4.020秒；三类fuzz分别63,035/46,752/57,589次通过。独立复核确认原问题修复并无已确认遗留缺陷。真实PG的NUL往返、授权/迁移/账本、浏览器和AD/Windows仍未验证，产品验收0/209。细节见[directory-v2-component-contract.md](directory-v2-component-contract.md)。

## 字典2独立用途与共享账本组件（本地未安装，2026-10-08）

准备独立domain.directory_read.v2用途、精确版本固定字段、共享使用账本与类型依赖SQL片段。旧八字段payload、schema15接口与历史SQL保持，新消费者仍关闭，没有复制旧授权或安装迁移16。停止确认继续要求原执行器真实返回证据，不依赖仍有效的角色/授权/来源。

最终make check通过Go race/vet/build、15项Python契约、559项前端测试、类型/构建与依赖审计0漏洞；全仓集成标签编译/vet通过。独立复核修复严格执行器测试桩的SQL参数期望，并防止文本/NULL来源版本被当作整数1，保留原安全断言。13组数据库测试无DSN明确失败于初始化，不算数据库通过。完整迁移、生产者、接口/界面与真实PG/浏览器/AD仍开放，验收0/209。详见[directory-v2-governance-contract.md](directory-v2-governance-contract.md)。

## F40 health 消息批量已读后端（2026-10-11，工程 #112）

Refs #50 / AD-F-158 / DTL-036。迁移18新增有界冻结目标、原接受回执、逐项首次时间收据及持久进度；每批复核live actor/tasks.readable/epoch，取消仅余项。固定结果all不扩展到实时全库；快照到期、新会话与Worker重启保持已接受操作。新HTTP和Worker已接线，UI后续切片；原纯模块与单条回执保持。详见 [批量运行时契约](message-bulk-runtime-contract.md)。

本地完整make check通过1470前端/104 Python及Go race/vet/build，集成标签编译通过；实际PG调用缺DSN失败，不能算通过。已新增真实health跨页/并发/撤权/取消/原回执、非空17升级及PG连接终止/容器崩溃重启测试，独立设计与实际diff复核进行中，精确head CI待执行。#50及产品0/209保持开放，无生产部署。

### PR167 后续：PG17 nullable observation 安全兼容

- 精确基线为 main `89e3adbf823e507b9d4b9a543f88ab3b54a1855b`，完整树
  `16c9c61dd25b894232aa1f63a0aa4d468466101e`；1088 个文件逐 blob/mode 核验后重建树一致。
- main-push run `39002743684` 的 user/computer running-cancel 分别报告
  `capture_readiness/invalid_session_observation` 与 `capture_readiness/unrecognized_lock`。
  现有安全产物没有原字段或锁类型，实际触发值仍未查明，不能认定具体事务路径。
- 已证实的模型缺口：CI 固定 PostgreSQL 17.6；其
  [backend 初始化](https://github.com/postgres/postgres/blob/REL_17_6/src/backend/utils/activity/backend_status.c#L352-L401)
  使用 STATE_UNDEFINED，
  [统计输出](https://github.com/postgres/postgres/blob/REL_17_6/src/backend/utils/adt/pgstatfuncs.c#L341-L367)
  将其转换为 SQL NULL。原预扫仅接受字符串，无法表示该合法观测。
- 完整结构、锁类别及 blocker 图检查后，NULL state 仅返回 `state_unavailable` 未就绪；
  保留原 1.5 秒 deadline，超时保留闭集最后原因。停后再次完整检查，NULL 立即失败并 CONT 清理。
  不把 NULL 命名为已确认启动，不将其当成 idle 或安全冻结事务。
- 新增闭集 `observationCheck`/`valueTypeCategory` 定位字段契约失败；
  `lockTypeCategory` 仅使用 [PG17 文档枚举](https://www.postgresql.org/docs/17/view-pg-locks.html) 或 `other`。
  所有原不支持的锁仍立即失败，即使属于 PG 的已知类别；不输出 SQL、名称、参数或 ID。
- 原 `validate_sessions`、`validate_checkpoint` 保持不变；固定捕获窗口、单次 STOP、严格停后验证与清理均保留。
  本地只执行禁止进程/网络/信号的离线 Python 契约；真实 PG 与三种导出需由新 head CI 验证。

### PR168 后续：blocker 拒绝点闭集细分

- 精确远程基线 `e1689acc6bf482c3ca4eeb465a913d5a8c91ad80`，树
  `01d2f82fbe262b202c86a0e3833415140a517c27`。run `39005521837` 的 user
  job `117317157083` 报 `capture_readiness/unowned_barrier_blocker`；group/computer 成功。
  安全产物 `12091286378` 未提供具体拒绝点；API/worker 均退出 0，无 forced kill/panic。
- 同一个旧 reason 有三条路径，离线合成分别复现：blocker 不在 owned 样本/barrier 集合、
  owned 图存在环、完整预扫通过但严格 activity 到 barrier 的链不成立。
  新 `blockerCheck` 分别为 `outside_owned_sample`、`cyclic_owned_graph`、`strict_barrier_chain`。
  全部仍立即失败，不进入未就绪重采样，不输出 PID、SQL 或原值；字段仅绑定原精确拒绝 reason。
- [PG17.6 状态快照](https://github.com/postgres/postgres/blob/REL_17_6/src/backend/utils/activity/backend_status.c#L650-L674)
  与 [逐调用 blocker 快照](https://github.com/postgres/postgres/blob/REL_17_6/src/backend/utils/adt/lockfuncs.c#L443-L452)
  分别采集；后者[单次保证瞬时自洽](https://github.com/postgres/postgres/blob/REL_17_6/src/backend/storage/lmgr/lock.c#L3571-L3580)。
  跨来源/跨调用可能出现时间差，但本次没有证据确认 race，也没有证据确认外部会话。
  worker appname 来自专属 nonce DSN，barrier PID 来自持续 psql 自报，尚未发现身份取错的代码证据。
- 新契约覆盖三路拒绝、隐私闭集及严格 validator/checkpoint 函数字节保真；捕获时序与清理不变。

### PR168 合并后：owned speculative INSERT 仅作为运行期未就绪

- 基线 main `8d367ad5c32fc4e5e334701a24dae60e6f1a5bbe`，完整树
  `fbc10ba6e0c664e09158cc2665f1fe12ccc293de`。post-main run `39010380998` 的 computer
  job `117331556087` 报 `capture_readiness/unrecognized_lock`，闭集锁类别确为 `spectoken`；
  group/user 成功。安全产物 `12093410338` 未记录具体 SQL，不能把触发路径认定为某条业务语句。
- [PG17.6 lmgr.c](https://github.com/postgres/postgres/blob/REL_17_6/src/backend/storage/lmgr/lmgr.c#L723-L783)
  定义 speculative insertion 决策锁：插入者取得 ExclusiveLock，等待者取得 ShareLock，决策后释放。
  [ON CONFLICT 执行路径](https://github.com/postgres/postgres/blob/REL_17_6/src/backend/executor/nodeModifyTable.c#L973-L1071)
  在 speculative 插入与唯一索引检查后释放，不必等待事务结束。
  项目 computer 导出快照 `internal/domains/computer_asset_export_v3.go:111` 和 worker 活动记录
  `internal/systemhealth/persistence_worker.go:36-42` 均有 ON CONFLICT 路径，这是候选来源而非本次根因证明。
- 仅对精确 `spectoken` 的
  [PG 投影形态](https://github.com/postgres/postgres/blob/REL_17_6/src/backend/utils/adt/lockfuncs.c#L300-L310)
  识别未就绪：mode 为 ExclusiveLock/ShareLock，granted 为布尔，relation/classid/objsubid 为 NULL，
  objid 为规范的非零 uint32 十进制 token。该 token 仅内存校验，不输出或保存。
  不强求 transaction 状态与锁同步，不凭 SQL 名称推断安全；其他未知锁与非法形态仍立即失败。
- 全部会话、锁形态与 owned blocker 图检查完成后才返回 `speculative_insert_in_progress`，
  后续坏行、未知锁、外部 blocker、环不能被 spectoken 遮住。仍在原固定 deadline 内观察，持续未就绪零 STOP 并超时失败。
  停后新出现 spectoken 立即失败并 CONT 清理，不循环解冻重试；严格冻结 validator、checkpoint 时序及 SQL 白名单原文不变。
- 严格离线 82 项契约通过：两模式/两 granted 布尔、token 边界、非法形态、三种导出瞬态→ready、持续超时、停后 race、
  后续坏样本不被遮蔽、候选 task 身份/租约失败优先以及原关键函数 SHA256 保真。真实行为等待新 head CI 验证。
