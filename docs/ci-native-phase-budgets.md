# Native-only CI 阶段预算合同

## 结论与范围

这是**显式增加两个 native job 总时限**的工程合同变更，不是保持原有时限，也不让
APT 下载变快。仅 `.github/workflows/ci.yml` 的 `native-no-store` 和
`native-no-store-fixed` 变化：2 个 job 级阈值和 15 个 step 级阈值，共 17 项。
旧两 job 都由整个 job 共享 10 分钟；新值分别是 41、51 分钟。安装不再与实际 native
CLI 争用同一个 10 分钟窗口，CLI 单独保留名义 10 分钟取消阈值。

当前 main 的 [native stable job](https://github.com/yunpiao/adtr/actions/runs/40368246014/job/121732236438)
在安装601秒后取消，验收与validator均 skipped、实际0 case、上传因无文件失败。
历史较长安装出现在其他job：[group job](https://github.com/yunpiao/adtr/actions/runs/40298849093/job/121492066829)
的APT15分39秒/安装958秒，[operational-logs job](https://github.com/yunpiao/adtr/actions/runs/40302670120/job/121506287335)
的APT15分58秒/安装975秒；这些不是native运行时长或native验收记录。
时间线支持“安装耗尽共享预算”的推断；镜像慢根因及平台确切取消触发仍未知，annotation尚未取得。
这些新阶段上限是否足够，必须由新 head 的真实 CI 验证，静态检查不回答该问题。
当前切片不恢复暂停的 APT B、cache、镜像、源、信任权限或新 probe 工作。

## 阈值、必要条件及删除影响

| 既有阶段（顺序不变） | stable 分钟 | fixed 分钟 | 解决的问题 / 删除该字段的后果 |
| --- | ---: | ---: | --- |
| checkout | 1 | 1 | 限制 checkout 阶段；删除后可占用外层剩余时间 |
| setup-node | 2 | 2 | 限制 Node/cache 准备；删除后可占用外层剩余时间 |
| npm ci | 2 | 2 | 限制锁文件依赖安装；删除后可占用外层剩余时间 |
| 原 Playwright 安装命令 | 20 | 20 | 单列浏览器/系统依赖安装；删除后可能再次挤占测试时间 |
| 原 fixed 官方 archive 下载/验证 | — | 10 | 单列下载、重试、校验、解压；删除后可占用外层剩余时间 |
| 原 native 验收 CLI | 10 | 10 | 限制完整 native 测试进程；删除后可占用外层剩余时间 |
| 原独立证据 validator | 2 | 2 | 限制独立验证；删除后可占用外层剩余时间 |
| 原诊断 upload | 2 | 2 | 限制证据上传；删除后可占用外层剩余时间 |
| 正向 step 阈值之和 | **39** | **49** | 不含 runner 初始化、action 获取及 post 工作 |
| 外层 job 阈值 | **41** | **51** | 约束整个 job；删除后失去本合同的外层取消边界 |

这些参数不是业务功能所必需的 YAML 配置，却是采用此阶段隔离合同时的必需项；当
依赖准备可能占满旧 10 分钟预算、仍须为原验收保留独立阈值时，需要调整外层阈值。
checkout1/setup2/npm2 可能比其以前可用的共享时间更短，副作用是准备失败可能更早。
新外层阈值也可能延后失败通知、增加 runner 占用和实际费用；具体费用变化未知。

完整 85 个选中 job 的**名义配置阈值和**是：
`plan10 + prepare20 + contracts20 + auth16×20 + browser55×20 + health30 + native10+10 + fixed7×20 + verify20 = 1680`。
仅 native20 改为 native92，成为 **1752**，增加 **72 名义 runner-minute**。
这不是实际消耗、整轮墙钟、计费硬上限或速度保证；browser inventory 的 56 项已含 health，
不能再重复计数。没有新付费 runner、组织设置或安全权限变更。

## 取消、post 与 fail-closed 边界

GitHub 的 job/step `timeout-minutes` 是触发取消/终止的阈值。信号、宽限与最终强制终止
仍有处理时间，不能当成事务清理或严格秒级停止保证。
[Workflow syntax](https://docs.github.com/en/actions/reference/workflows-and-actions/workflow-syntax#jobsjob_idtimeout-minutes)、
[cancellation](https://docs.github.com/en/actions/reference/workflows-and-actions/workflow-cancellation)。

41−39、51−49 的 **+2 分钟仅是名义管理/post 余量，不是保障**。action 包获取可能在
step 前发生；checkout 有 post hook，setup-node 有成功时 cache-save post hook。post
可能再次使用 action 的 timeout，余量不保证每个正向步骤都耗满后仍可完成。平台取消
可能中断 JS `finally`、cleanup、validator、post 或 `always()` upload。

fixed 原 curl 保持 `--retry 2 --connect-timeout 10 --max-time 180` 和 HTTPS 校验。
180 秒限制一次尝试，不是所有重试总时限；服务端 Retry-After 可能增加等待，新增 archive10
也可能中断下载后的校验。删除 archive10 会让该阶段依赖外层剩余时间。
[curl retry](https://curl.se/docs/manpage.html#--retry)。不修改命令、TLS 或重试参数来凑预算。

证据 validator 仍为 `success()` 条件，诊断 upload 仍为 `always()`，缺文件仍 error，
retention 仍 7 天。超时、缺证据、验证失败和 job 非成功都 fail closed；不能用可能残缺的
always-upload JSON 代替通过。JSON 在最终断言/cleanup 前保存，不能独自证明清理完成。
每 job 都须原两生产 reader × 五 case 共 **10 个准确源绑定记录**、CLI 成功、独立 validator
通过和最终 job 成功；fixed 还必须有原官方 archive provenance。

## 不变部分与历史源码投影

保留所有命令、顺序、action SHA、Node24.19.0、Playwright 固定 Chromium141 与 fixed157、
cache、curl、源 URL/大小/SHA/版本、权限、selectors、needs、env 和证据条件。
保留原 10 case、每 case30秒、beforeAll 继承120秒、等待10秒、单项cleanup5秒、workers1/retries0，
原生产 reader 的真实 stream/parser/body/byte-cap/transport 断言不变。
native 只使用合成 loopback fixture 和 browser，不启动 API、PostgreSQL 或 LDAP。

其他 55 个 browser、7 个 fixed、health30 及其**安装前建立**的 now+1620 秒 deadline、
Go600秒、auth16、concurrency 和完整85项选择集合均不变。生产功能与产品验收0/209不变。

`restore_native_phase_budgets` 先直接核当前 raw 两 block、全部17项阈值与每step身份，
再依据有界、严格 schema 的 `native_phase_budgets_ci_delta.json` 只反转此获审 delta，
恢复完整旧 workflow SHA256 `0fd9599e994b86e28d72d2fbf28be4fffa14f7bbff171b3d47c96069e592b195`。
新增 helper/hook 必须精确撤销到旧 helper SHA256
`8cfce8da1c1bcd81b2d81b8dde6e27803a87195d5e33ccb3be817e0eb2d30244`，
再运行原 auth→access→archive→health→directory/messages→selection 历史链。
旧 SHA 与旧负测不换不删；该投影保护历史其他语义，并不表示当前 native 总预算没有改变。
旧 `timeout-minutes: 10` 子串断言改为分别锚定 raw job 与每个 step，拒绝 outer999+step10。

## 交付门禁

本文件记录候选合同，不宣称新预算已在实际 CI 通过。本地须保留完整适用源契约、需求、
选择器与新增负例的命令/原日志、首失败、唯一测试数及19项既有重复导入记录。
运行过的检查与仅依据未改输入重绑的历史检查分别列出；未运行 `make check`、browser、
PG、API/Worker、LDAP、Docker、安装或外部依赖 audit 时不能算作通过。

独立源码审查、发布前精确 base/head 重核、该 head 全部必需 CI、两个 native 的各10 case
及原独立证据检查仍是独立门禁。发现真实新预算不足时诊断原因，不能静默扩大时限、
删 case、跳依赖或放宽 validator。合并与生产验收仍分别记账。
