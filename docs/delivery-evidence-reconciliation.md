# 当前交付说明与历史证据对账

## 结论与冻结边界

截至 **2026-10-26 17:29 UTC**，源码基线为已合并的
[main `8b3524dad9a9adb9d4a1d3a6f64557e0143b7aa3`](https://github.com/yunpiao/adtr/commit/8b3524dad9a9adb9d4a1d3a6f64557e0143b7aa3)，
内容树 `f7601cf7be1e928710eedf59d63fe2a2972e45f2`，有序父提交为
`a987e1c54add2fcc1679398dc73c2c70ece1dea5`、
`34ab891596d3bf93e8ca5c5e5bbbd6f0723ea1f1`。完整 1,341 文件的
Git blob、模式、SHA256 和重建 Git tree 已核对。

**用户/组/计算机有界导出、计算机关键词清除/长度限制、原始组成员界面和观测计数已有合并实现及各自历史工程运行证据。**
README 和 catalog 中的早期“候选待验/尚未交付”不能继续作为这些切片的当前状态。
这不提升任何 AD-F 状态、产品验收字段或计数；产品验收仍 **0/209**。

本次仅改 README、catalog 的说明/索引及本文件，不修改生产代码、测试、fixture、
runner、CI 或旧交付文档。历史文档及 catalog 的候选记录保留原时点含义，
本页用于说明哪些旧描述已被后续事实取代，不抹去失败或未验证范围。

[PR227](https://github.com/yunpiao/adtr/pull/227) 已合并；其
[新 main-push run40368246014/attempt1](https://github.com/yunpiao/adtr/actions/runs/40368246014)
在上述冻结时点仍在独立验证。**本页没有当前 main 全量通过结论。**
下列旧运行均只证明其明确列出的提交/场景，不继承为新 main 或本次文档候选的测试通过。

## 逐条旧描述 → 当前事实与来源

### 用户资产与导出：AD-F-038/039/053、EXP-006/007/008

旧描述将业务导出概括为“未交付”，并把 EXP-007 写成尚待候选 CI。
当前已有三个独立的、受权限保护的固定 dictionary3 单域观测 XLSX 生产者：

| 切片           | 已合并 PR / merge commit                                                                      | 本次回读的所属历史 main 运行                                                                                                                                                             | 精确边界                                         |
| -------------- | --------------------------------------------------------------------------------------------- | ---------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- | ------------------------------------------------ |
| 用户 EXP-006   | [PR159](https://github.com/yunpiao/adtr/pull/159), `370e405b50a23a85a63126c562bb1790dd4eca87` | [38487379365](https://github.com/yunpiao/adtr/actions/runs/38487379365), attempt1 60/60；[原 job115604285283](https://github.com/yunpiao/adtr/actions/runs/38487379365/job/115604285283) | 六个已采集 scalar 列；全部匹配行，不限当前页     |
| 组 EXP-008     | [PR161](https://github.com/yunpiao/adtr/pull/161), `7ed455aecee1352f0261e7064253891af1c31c79` | [38535766672](https://github.com/yunpiao/adtr/actions/runs/38535766672), attempt1 65/65；[原 job115758273273](https://github.com/yunpiao/adtr/actions/runs/38535766672/job/115758273273) | 五个 scalar 列；复用组列表搜索/创建时间排序      |
| 计算机 EXP-007 | [PR162](https://github.com/yunpiao/adtr/pull/162), `94b098d6acf5187e1ecf9aa659f5abed00122ce7` | [38561279766](https://github.com/yunpiao/adtr/actions/runs/38561279766), attempt1 68/68；[原 job115842883500](https://github.com/yunpiao/adtr/actions/runs/38561279766/job/115842883500) | 五个 scalar 列；复用计算机搜索/成对时间范围/排序 |

三个原 job 均实际完成浏览器、API/Worker/PostgreSQL 链和 XLSX evidence validator；
测试 LDAP 是隔离合成 TLS LDAP，不是真实 AD。源码入口为
[userAssetExportSourceTx / executeUserAssetExportV3](../internal/domains/user_asset_export_v3.go)、
[groupAssetExportSourceTx / executeGroupAssetExportV3](../internal/domains/group_asset_export_v3.go)、
[computerAssetExportSourceTx / executeComputerAssetExportV3](../internal/domains/computer_asset_export_v3.go)。
对应契约：
[用户](user-asset-export-v3-contract.md)、
[组](group-asset-export-v3-contract.md)、
[计算机](computer-asset-export-v3-contract.md)；
实际场景在同名 `web/e2e/*-asset-export-v3.spec.ts` 中。
F38 的[统一导出历史](export-history-contract.md)仍只接 Audit/XLSX；
业务导出的独立工作区不能冒充已接入这一历史入口。

[PR226](https://github.com/yunpiao/adtr/pull/226) 已将可选 UAC 禁用位筛选合并为
`0fb8c177000eeede4fbdf879d02a17167e8e2f0a`。
[userAssetsV3Rows](../internal/domains/user_assets_v3.go)在已完整验证的观测上先做
kind/UAC/原有筛选，再计算总数/分页；导出调用相同行筛选，不自行猜造业务状态。
详情和 v2 不接受这一新筛选。

`uacDisableBit=set|clear|not_returned` 解决“按保存的 0x2 位分区”问题；
仅用户 v3 列表/导出选择分区时必需，省略保留不筛选，空值/未知枚举拒绝。
删除已选参数会扩大到原无 UAC 筛选结果；它不修改 AD，也不能由 clear 推断
账户活跃、可登录、未锁定或密码有效。旧 Worker 不接受新可选载荷，
未来部署需协调 API/Worker 版本，本次没有部署。

PR227 的成功 format2 实证在
[run40335636523](https://github.com/yunpiao/adtr/actions/runs/40335636523) 的
[原 job121616947812](https://github.com/yunpiao/adtr/actions/runs/40335636523/job/121616947812)，
[artifact12658329956](https://github.com/yunpiao/adtr/actions/runs/40335636523/artifacts/12658329956)：
绑定实际 checkout `9e11175391c45bcd97bf50ba6510ba70b331983e`、tree `f7601cf7...`、
1,207 项执行源码摘要，保留无 UAC 筛选的 `user-` **12 行**以及
`user-` AND `set` **0 行、只有表头**两份 XLSX。
success 使用 format2，原 failure/recovery、queued-cancel、running-cancel 保持 format1；
不把新成功格式冒充三类旧终态证明的升级。

旧 unmatched-keyword 空列表 GET 保留，但其原完整第二次空结果导出交易链被 set-empty 链替换，
这是**非等价覆盖取舍**。PG 合成执行器覆盖 set/clear/not_returned/省略，
真实合成 LDAP 用户只含 UAC0/512；非空 set、null/not_returned LDAP 全链和正向 filtered-clear
真实下载仍缺，不能由空结果或 DOM/PG 通过推导出来。
[当前用户契约](user-assets-v3-contract.md)及上述 PR 明确保留这一范围。

AD-F-053 的 `running_cancel_candidate` / `kind_running_cancel_candidate` 是历史候选记录：
[PR163](https://github.com/yunpiao/adtr/pull/163)、
[PR164](https://github.com/yunpiao/adtr/pull/164) 后续已合并 snapshot/progress10 的三类
running → cancel_requested → cancelled 工程验证。保留原字段状态并另加历史上下文，
不据旧记录声称当前只能 queued 取消；也不将 snapshot 证明扩展为多块或全部 fencing 竞争验收。
PR164 记录的 [main38615254322](https://github.com/yunpiao/adtr/actions/runs/38615254322) 为 attempt2 重试后通过；
attempt1 为 69 成功、auth4 原 600 秒包级超时与 verify 两项失败。原超时与原因未明的耗时差异保留，
不把这一历史运行写成首轮全绿；本次该 run 的 jobs 读取存在超时/仅部分页返回，不冒称重新完整复核全部 71 项。

[PR225](https://github.com/yunpiao/adtr/pull/225) 仍为 draft，已发布 head `86e75058219682d8a15894478730eeb47cecdd26`
的 [run39951432969/attempt1](https://github.com/yunpiao/adtr/actions/runs/39951432969) 保留 70 SUCCESS、7 CANCELLED、verify FAILURE。
原 [multichunk job120407263040](https://github.com/yunpiao/adtr/actions/runs/39951432969/job/120407263040) 先在 database 阶段真实失败、随后才被平台取消，不能当作纯技术取消或多块验收通过。
尚未发布的后继修订仅有离线 245+2 检查，上传审批状态未知；这些不是已发布 head 的唯一运行记录，
也不能替代后继的真实数据库/浏览器与多块字节证据，本次不重试发布。

### 计算机关键词：AD-F-040、FLD-0418 / UI-0437

旧说明“允许超长草稿、提交才拒绝”和“关键词清除仅本地”已过时：

- [PR176](https://github.com/yunpiao/adtr/pull/176) 的 keyword-only clear 清空草稿并恢复焦点，
  保留时间/排序/来源/pin/已应用查询，不自动发请求，显式 Apply 才查询。
  合并 main `91e05b9d5790e1b9e4654ef6f03deb55195c925b` 的
  [39211561445](https://github.com/yunpiao/adtr/actions/runs/39211561445) 首轮 71/71；
  [原 computer job117925993760](https://github.com/yunpiao/adtr/actions/runs/39211561445/job/117925993760)
  实际完成浏览器/PG；PR 运行是一次 failed-jobs 重试后通过，不能写成首轮全绿。
- [PR178](https://github.com/yunpiao/adtr/pull/178) 增加 **50 UTF-16 单元整次超限拒绝**，
  恢复此前有效草稿、选区/方向与焦点，不截断；IME composition 期间允许暂存，
  compositionend 再接受或恢复，期间暂禁提交/刷新/清空等动作。
  合并 main `20234bd82523307990b7b10a70c53a9f1ad90c85` 的
  [39274635431](https://github.com/yunpiao/adtr/actions/runs/39274635431) 首轮 71/71；
  [原 computer job118135868203](https://github.com/yunpiao/adtr/actions/runs/39274635431/job/118135868203)
  和保留的 main-default/main-fixed 证据覆盖键盘、粘贴、选区、emoji。
  OS 级 IME 与 native drop 仍仅有合成事件契约，没有独立原生平台实测。

当前实现锚点为
[ComputerAssetsV3Workspace 的 rejectKeywordLength/changeKeyword、composition/paste/drop、clear 按钮](../web/src/ComputerAssetsV3Workspace.tsx)，
具名用例在
[长度测试](../web/src/ComputerAssetsV3KeywordLimit.test.tsx)、
[clear 测试](../web/src/ComputerAssetsV3ClearKeyword.test.tsx)和
[真实场景](../web/e2e/computer-assets-v3.spec.ts)。
50 UTF-16 防止超长编辑进入有效草稿，是本工程约束，不是对原系统 maxlength 行为的完整兼容证明；
移除会恢复可输入超长草稿的旧行为。composition 暂禁操作是明确副作用，结束后恢复。
12 事实字段、页内序号及 clear/长度切片不能代替旧 47 字段、跨页序号、地址关系或完整 UI 验收。
[原计算机对账](computer-assets-v3-delivery-reconciliation.md)继续保留当时缺口与失败。

### 组列表：AD-F-042

旧说明“PR128/132 未合并、PR136 尚无真实链”已被后续合并和运行取代。
[PR136](https://github.com/yunpiao/adtr/pull/136) 已合并为
`f8f2e1e7a638473c77514d74f87647dcbf39ca2f`。
精确 head `a42645f39ecde4342f4359f4b9b790fbe3ce7dc8` 的
[run38200302860](https://github.com/yunpiao/adtr/actions/runs/38200302860) 首轮 50/50；
[原 group job114652075655](https://github.com/yunpiao/adtr/actions/runs/38200302860/job/114652075655)
实际 checkout `45888bf...`，原日志确认合成 TLS LDAP/Worker/PG/API/browser 通过。
这是该 PR 运行，不称为本次 main 或 PR136 独立 main-push 的新复验。

[group_assets_v3.go](../internal/domains/group_assets_v3.go)、
[GroupAssetsV3Workspace](../web/src/GroupAssetsV3Workspace.tsx)和
[原生场景](../web/e2e/group-assets-v3.spec.ts)提供固定观测只读列表、12 原始事实、
SAM/SID/DN 字面搜索与稳定分页；契约见[组列表](group-assets-v3-contract.md)。
20 UTF-16/80 UTF-8 字节是工程查询限额，不补造组类型/范围/显示名/应用/敏感分类。
后续有界导出和原始 member 的存在不完成完整成员/关系或旧字段兼容。
早期缺 DSN/Docker 的本地失败仍是失败，不因远端成功改写成本地运行成功。

### 原始组成员与界面：AD-F-043

旧“后端实现中、尚无 UI、真实链待运行”已被
[PR152](https://github.com/yunpiao/adtr/pull/152) 后端、
[PR153](https://github.com/yunpiao/adtr/pull/153) 固定观测页面和
[PR195](https://github.com/yunpiao/adtr/pull/195) 控制/原任务恢复界面取代。
后者 merge `39eb1077df159c6d83cd7f0c4f5e504cd335066e` 的
[main39518300817](https://github.com/yunpiao/adtr/actions/runs/39518300817) 首轮 75/75；
[原 controls job118956165657](https://github.com/yunpiao/adtr/actions/runs/39518300817/job/118956165657)
和保留的 main39eb-controls JSON/PNG 绑定原任务、当前授权、原始 member 顺序及恢复行为。
本次回读日志/JSON，不重新宣称对全部历史 PNG 做过像素复审；PR 中记录的原独立视觉复审与本次检查分开。

当前入口：
[App 的 group-members-v4 / directory-v4 路由](../web/src/App.tsx)、
[GroupMembersV4Workspace](../web/src/GroupMembersV4Workspace.tsx)、
[DirectoryV4Workspace](../web/src/DirectoryV4Workspace.tsx)、
[控制场景](../web/e2e/directory-v4-controls.spec.ts)及
[dictionary4 契约](directory-v4-contract.md)。
原始 member 的未返回 null、空数组与返回值顺序严格区分；
returnedMemberCount 不是旧 memberCount，ranged 属性整次拒绝。
memberOf、主组补齐、递归/跨域完整性、ranged continuation 及完整旧 UI/字段语义仍未完成或未验收。
两个默认关闭的目录/v4 开关和独立当前授权仅在执行生产者时必需，
解决意外采集/旧授权升级问题；移除会改变默认准入边界，启用则引入实际目录访问与任务负载。
读取已保存观测仍须当前读取权限，但不靠启用生产者来取得权限。

### 观测计数：AD-F-052、FLD-0586/0588/0590

旧“candidate only”被
[PR165](https://github.com/yunpiao/adtr/pull/165) 的合并与运行取代。
merge `7140933be2459dbfc4b824aa6d6d6c46a9f4f8da` 的
[main38644413239](https://github.com/yunpiao/adtr/actions/runs/38644413239) 首轮 71/71；
[原 directory-v3 job116127773697](https://github.com/yunpiao/adtr/actions/runs/38644413239/job/116127773697)
实际完成浏览器/PG 链。原独立产物审查及 mixed12/12/6、empty/null、
跨 kind/page 与撤权边界见[Issue30 原记录](https://github.com/yunpiao/adtr/issues/30#issuecomment-6180387833)。

[directoryV3Page / DirectoryV3ListTx](../internal/domains/directory_query_v3.go)
在同一完整有效观测中按明确 kind 计数，再筛选/分页；继承 user objectClass 的 computer 不重复计入 user。
[具名测试](../internal/domains/directory_v3_test.go)和
[现有真实场景源契约](../scripts/test_directory_v3_contract.py)覆盖此行为。
`observedCounts` 是 required nullable 响应字段：不可用为 null，成功空观测为三个 0；
它解决“无观测”和“确实观察到零”的混淆，删除会破坏严格客户端解析。
代价是必须先完整验证/计数观测，不能仅由当前页推算。
这不是旧资产总量、安全/风险概览、差异或图表的验收。

## 失败、重试与平台元数据不得消失

- PR159 的 38474688250（3 成功/57 失败）与 38478693008（49/11）、PR161 的
  38520290658（62/3）及 38525081993（63/2）保留于各 PR 和 catalog。
  后续修复运行的通过不改变这些原结果。
- PR162 38545054964（54/14）与 38550970449 attempt1 reader socket/verify 失败保留；
  后者 attempt2 实际 reader/verify 成功但 rollup 有 queued 重复，最终 attempt3 68/68。
  socket 底层原因未查明，失败的旧 attempt 重跑 403 也不是测试通过。
- PR176 的 candidate_strict 实际失败原因及 directory reader socket 关闭原因未查明；
  39205201921 attempt2 是一次受限重试后成功，四条旧同名 queued/无 runner 但有 completed steps
  的不一致记录保留，不计四次额外成功执行。
- PR178 的采集下界不一致和测试 helper 路径错误已有源码修复与后继验证；
  较早一次 journal write failure 的底层原因仍未知，后续成功不证明根因已修。
- PR165 初始 38629361562 为 68/3：一项测试错误期待跨租户同名自有域返回 404 已修正；
  computer unsafe_relation_lock 的原 relation/mode/granted 未保留，具体原因未知，
  仅有界诊断增强，不放宽安全判断。
- PR152 的未知-purpose/治理 expiry fixture 错误、镜像 429 和 auth 超时；
  PR153 的 PG 镜像配额与 390px 溢出；
  PR195 的配置/源查询/observer 测试同步、文本可见性及 reload/body/socket 失败均保留在 PR 历史。
  明确修正的机制与仍未知的 timeout/socket 原因分开，不笼统称全部根因已修复。
- PR226 的 [main40235305421](https://github.com/yunpiao/adtr/actions/runs/40235305421)
  实际为 75 SUCCESS + directory-v4-readers/verify 两个 FAILURE。
  `cdp_body_no_data` 表示原 Response 唯一一次 body 读取未取得 CDP 正文；
  现有证据不足以确定底层 Request/focus/JSON 时序原因。保留失败并靠后续独立诊断/运行定位，
  不把 PR 重试通过转记 main 全绿。
- PR227 最后 run40335636523/attempt5 的当前 85 个 check-linked 逻辑 jobs/checks
  与 verify 均 SUCCESS：**80 首轮 + 4 项各一次受限复验 + 最后 verify**，
  不是 85 项全重跑。原 3 次取消、legacy socket 与中间 verify 失败保留。
  raw93 中另 8 条 queued 无 runner、独立 steps 为空，代表保留的未完成平台记录，
  不能当成额外执行或 raw 全终态。重复形成原因未知；合并采用的是明确披露的元数据例外，
  不删除、不伪造状态，也不据此再发重试。

## 未验产品门禁与本次候选验证

G01 源字段/参考实现与容量目标冻结、G04 规则/专项算法输入、G05 真实 AD/八版本 Windows 的 72 场景、
G06 RPO/RTO 与真实恢复演练等门禁仍开放；范围不因输入阻碍或优先级缩减。
合成 LDAP、真实 PostgreSQL、浏览器工程切片和代码保护上限均不能替代这些门禁。
APT/B/probe/Stage3 已停，不由本次文档对账重启。

本次候选只运行需求映射/证据边界检查、有限源码契约、文档/JSON检查与实际 CI selection 规划；
不启动 PostgreSQL/浏览器、不重新执行历史业务测试。
`requirements/catalog.json` 不是 selector 的 Markdown 文档豁免路径，
因此未修改的 selector 对这三个路径保守选择全 **85** 门禁
（56 browser、7 fixed、16 auth、2 native、4 core）；选择计划不是这些门禁已经运行。
未来发布须另外完成独立 review、精确提交检查及实际 main 兼容性，本地候选不宣称已发布或全面验收。
