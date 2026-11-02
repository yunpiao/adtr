# SIMULATED 规则库独立验收

对应 Issue #36/#40 的合成工程切片，非完整产品验收（仍 0/209），不覆盖 #57 学习、真实 AD/Windows、源系统等价或容量。

## 可重复入口

构建 `go build -o bin/adtr ./cmd/adtr`、`npm ci --prefix web`、`npm run build --prefix web`，安装 Playwright Chromium 后运行：

```
python3 -m unittest discover -s scripts -p test_simulation_rule_catalog_workflow_contract.py
python3 scripts/test_simulation_rule_catalog_integration.py
```

第二条必须实际拥有 Docker/PostgreSQL/浏览器；缺 Docker 记录 `not-run` 并非零退出，绝不把静态契约、编译或 Playwright 用例发现算成运行通过。专用 workflow 对 PR 精确 head checkout，固定官方 Action SHA 与已有 PostgreSQL digest，不扩大既有 engineering-ci 选择器。

## 真实验证链

- 独占、带随机所有者标签且仅绑定 loopback 的临时 PostgreSQL；重复迁移及 schema29；临时用户、显式域/租户许可。拒绝继承外部数据库/AD 参数。
- 作者与另一真实登录审核人分离；审核人仅规则库可读、独立发布标记可读写，不能改草稿。实际服务端验证身份，不接受 Passed、reviewer 或历史快照伪造。
- 真实 API 草稿创建/编辑到规则 v2；真实正/反用例评估；异人发布、激活、发布 JSON；浏览器实际下载文件字节并解析，不只看成功提示。
- 明确确认独立 COPY：新手动模板 v1，私有执行规则 Enabled=true、Version=1；原发布 Enabled=false/version2/digest 保留；SQL 来源记录与回执一致，两个摘要各自核对。
- 真实 `SeedFixtureTx` 产生 `catalog-positive`（登录失败）与 `catalog-negative`（服务访问）。既有 Worker 必须产生精确 `http-rule/catalog-positive` 或 `browser-rule/catalog-positive` 一条匹配；反例零匹配。空成功任务不合格。
- 原操作重放不得新增模板/任务；停用源规则后原独立副本仍运行，重启 API/Worker 后证据保留。禁止把独立 COPY 描述为实时绑定或全局停用。
- `event.code`、`object.kind` 发布可存在，但复制必须 422；不修改原手动数据映射的五个字段。
- 执行全部八个精确命名的后端、HTTP 权限、schema29 升级/回滚/并发 PostgreSQL 测试（含修订预算耗尽后安全停用/归档）；缺少命名 pass、任何 skip 或非零返回均失败。
- 浏览器真实响应丢失后在当前挂载恢复原操作；之后 reload 验证已保存规则、发布与任务。当前 UI 没有跨 reload 保存未决 intent；不声称该能力。

## 证据与安全

仅上传 `artifacts/simulation-rule-catalog/result.json` 及主动截取、标记 SIMULATED 的业务区域 PNG。关闭 trace/video/自动失败截图，不保存会话、原始服务器日志、数据库连接串或密码。Go 失败诊断仅允许已知测试文件名/行号、SQLSTATE 与整数退出码。API 与 Worker 显式独立 loopback 端口，退出检查与原等待预算不变。

源码准备、静态检查、真实运行、独立复审、当前 head CI、合并后 main 验证必须分别记录。此文定义验证流程，不宣称这些门禁已经通过。

## 2026-11-02 本地候选复审记录

独立源码复审提出的五项问题已修正并复核：COPY 回执动作统一为真实
`copy-template`；规范化 Intent 绑定声明、用例与目标模板并校验服务端请求摘要；
读取 COPY 回执重新检查当前手动检测读取权限及功能开关；切换规则时重置编辑器；
普通编辑最多消耗到修订 62，为停用和归档保留 63/64 两个修订。
专用验收明确枚举八个 PostgreSQL 测试，静态清单与源码测试名一致。

完整 Go race 与 vet 已通过。专用静态契约 8/8、严格 TypeScript 编译及
Playwright 用例发现 1 项已通过。上述结果均不能代替实际 PostgreSQL/浏览器执行；
本地专用运行因缺 Docker 返回 not-run，实际链路留给精确提交的专用 CI。

旧 TaskNavigation 全量回归曾分别在审计结果请求与被动 effect abort 清理断言失败，
原日志保留。生产实现未改；两处测试分别先等待既有审计结果、使用默认 waitFor
等待 abort，全部原有精确 URL、旧响应隔离及当前 CSRF 断言保留，未扩大超时。
独立复审确认仅测试观察同步改变；修正后整文件 75/75 与五轮聚焦重复共 10/10 通过。
这是依据源码作出的异步观察竞态判断，尚无失败时序追踪证明完整根因，不宣称修复
生产身份隔离缺陷。最终完整回归结果另行记录。

最终候选本地复核：`go test -race -count=1 ./...`、`go vet ./...`、
`python3 -m unittest discover -s scripts -p '*_contract.py'`（1272 项，143.612 秒）、
前端完整 113 文件/5113 项、typecheck、build、209 条需求完整性、gofmt 与 diff 检查通过。
曾误用更广的 `test_*.py` 发现模式：1284 项执行后，既有显式 PostgreSQL fixture
因未提供容器名在 setUpClass 失败；原日志保留。随后使用 Makefile 规定的契约入口通过，
不把这一结果解释为 PostgreSQL 运行测试已经通过或已修复。
