# 合成定时检测：独立验证入口

## 结论与证据边界

新增专用 workflow `simulation-detection-schedules`，不修改旧 engineering-ci 浏览器清单、
健康调度器或手动检测回执防伪门禁。对应既有 Issue #41（AD-F-079/080）及 #15 的
合成检测子集；需求不足采用明确的 SIM-SCHEDULE-001 假设，不声称恢复原产品语义。
真实 AD、Windows 矩阵、包速率 Rate、完整批量/容量验收未覆盖，产品验收仍为 0/209。

本地已执行：Python 离线 wiring 契约 6 项通过；独立 TypeScript 严格编译通过；
Playwright `--list` 发现 1 个专用用例。实际 harness 已执行并以 exit 1、`status=not-run`、
`checks=[]` 失败：当前云环境没有 Docker/PostgreSQL。没有把缺环境、发现测试或静态检查
记作数据库/浏览器通过。实际运行证据必须来自当前提交专用 CI，不能继承其他 PR 结果。

## 两条独立且真实的链路

1. API 与浏览器：创建暂停计划，月/周/日周期输入，编辑生成新修订并暂停，CAS
   启用/暂停/逻辑归档，历史分页，响应已提交但浏览器丢失后的相同操作 ID 回执恢复，
   页面重载。旧回执保持原暂停状态，详情独立显示当前归档状态。
2. 真实执行：测试拥有的隔离数据库内，先通过 API 创建并验证模板与计划，再单次
   **INSERT 一个独立、明确标注 historical producer 的历史计划及不可变修订**，复制
   服务端校验后的模板/数据集固定摘要与当前授权 epoch，以历史每日锚点使下一期到期。
   此后必须由真实 Worker 的 Tick、SubmitTx、原子 intent 账本、执行器及最终成功门禁
   产生唯一 task 与合成评估结果。API 与浏览器读取这条真实任务结果和发生历史；
   API/Worker 重启后校验结果及唯一发生不变。

第二条不是第一条浏览器新建计划提前触发的证据。不存在等待一天的伪装：没有生产快时钟
环境参数、修改服务器时间、重写旧不可变修订、禁用触发器、倒退游标、手插 intent、
制造 terminal task 或伪造成功结果。历史 fixture 只用于隔离测试，不用于产品能力。

## 必须实际执行的 PostgreSQL 用例

专用 workflow 先运行完整 `go test -race ./...`（包括 canonical payload 防篡改单测）；以下10个实际 PostgreSQL 顶级用例逐项执行。

每项使用精确锚定 `-run ^Name$ -count=1 -tags=integration -json`。
必须看到指定测试 pass，任何子测试 skip、无匹配测试、命令失败均拒绝。

- `internal/store`: `TestSchema27UpgradePreservesHistoryAndGrants`、
  `TestSchema27FailureRollsBackPermissionAndTables`：26→27、原历史/授权保持、重复迁移、失败原子回滚。
- `internal/auth`: `TestSimulationDetectionSchedulesHTTPAuthorityReceiptsAndCAS`。
- `internal/simulationdetectionschedules`：
  - `TestPostgresScheduleCRUDCASReceiptAndAuditRollback`
  - `TestPostgresScheduleConcurrentTickActualExecutionAndIntent`
  - `TestPostgresScheduleMissedOverlapRevocationAndStalePin`
  - `TestPostgresScheduledKindForgedOriginAndTemplateArchive`
  - `TestPostgresScheduleOverlapAcrossRevisions`
  - `TestPostgresEnableAlreadyEnabledDoesNotSkipDue`
  - `TestPostgresFinalPublicationRejectsPostExecutionConfigurationOrEpochChange`

这些用例覆盖 CRUD/CAS/回执审计原子性、并发 Tick 唯一发生、实际 RunOne、错过/重叠、
撤权 epoch、过期配置、伪造来源拒绝和跨修订重叠；还覆盖重复启用不跳过到期发生，以及执行结束后、最终发布前模板归档/epoch 撤回再授予的真实 Finish 拒绝。具体断言以测试源码为准；尚未实际执行
通过前不将标题当结果。HTTP 另检查匿名/admin-only、同域不同 owner、跨域/租户、
CSRF、未知 rate/actor/anchor 字段拒绝；最后分别移除双授权之一，并验证恢复不复活旧 epoch 任务。

## 运行与安全证据

```sh
python3 -m unittest discover -s scripts -p test_simulation_detection_schedules_workflow_contract.py
go build -o bin/adtr ./cmd/adtr
npm ci --prefix web && npm run build --prefix web
(cd web && npx --no-install playwright install --with-deps chromium)
python3 scripts/test_simulation_detection_schedules_integration.py
```

需 Go 1.27.1、Node 24.19.0、Docker、Chromium。harness 不接受外部数据库地址；创建随机
命名、带 owner 标签的固定 digest PostgreSQL，仅清理其拥有的资源。API/Worker 使用不同
显式 loopback 端口；Worker readiness 与真实业务结果分别验证。两个模拟开关只在隔离
子进程设置，生产默认仍关闭。去掉独立调度开关会使专用 API/Worker 不可用；无需真实凭据。

workflow 只取当前 PR head/推送 SHA，Actions 完整 SHA 固定，contents:read，checkout
不保留凭据。不上传原始服务器日志、浏览器 trace/video/storage state 或数据库连接串。
失败命名 Go 测试只打印有限已知文件名的 source line/SQLSTATE/整数退出码，不打印原始 stdout/stderr。
证据目录 `artifacts/simulation-detection-schedules/` 只含带当前 head 的结果 JSON 与
明确合成区域截图；`unavailable` 保留 Rate 等未实现项。缺环境非通过，UI 成功非产品验收。

## 最终本地与独立复审记录

2026-11-02：完整 Go race（count=1）及 vet 通过，最终前端 111 文件 / 5,045 测试通过；
定向 46 项、类型检查与构建通过。前端首次全量被环境重启中断，保留未完成日志，
使用相同测试集合、限制本地并发为 2 后完整重跑通过。
完整 Python 契约 1,252 项通过；之后同步父分支 dashboard fixture 的精确 schema
事务门禁修复，新增专项 9 项通过、相关 integration-tag 编译通过。
编译成功不代表实际数据库执行。

独立复审覆盖最终发布前配置/授权校验、独立 occurrence intent、防伪造任务、跨修订互斥、
月末锚点、不可变历史及回执当前状态。修复了 canonical JSON 键序比较误冲突，并复核
完整类型化 payload 比较的全部字段。既有 tasks.Finish 撤权失败可能保留内部计算结果，
本模块不宣称擦除；旧 epoch 读取必须返回 404，历史不暴露结果或计为成功。

源码边界独立审计默认 324 / CI 335 个生产文件，最终双标签 boundary/vet 通过。
首次生成 wiring 清单遗漏 8 个未变更条目，失败证据保留，恢复完整清单后重跑通过。
仅修改三份源码证书，没有放宽 guard/allowlist/预算。上述修复与重新验证均不替代
当前提交真实 PostgreSQL、API、Worker、browser CI 和 main 合并后验证。
