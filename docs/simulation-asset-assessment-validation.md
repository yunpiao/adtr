# SIMULATED 资产分类报告：验证边界

这是固定合成观测上的同步 `assetassessment.Evaluate` 报告，不是 Worker 任务、真实 AD 数据或产品验收。产品范围仍为 0/209；enabled 不等于原产品“活跃 0”，完整报告统计仅涵盖固定 11 个对象，`complete=false`。

## 独立预期与实际链路

`test_simulation_asset_assessment_integration.py` 手写逐对象值/原因及完整分类计数，不调用 Go Evaluate 生成预期。基线包含 user 4 / computer 3 / group 4；账户 enabled 3 / disabled 1 / locked 1 / unknown 2 / not_applicable 4；活动 recent 2 / inactive 2 / never_observed 1 / unknown 2 / not_applicable 4。精确 30 天、前后 1ns 的边界在 fixture 中独立列出；正好 24 小时仍新鲜，24 小时加 1ns 全部适用字段为 unknown/stale_observation；不适用字段保持 not_applicable/asset_kind。缺失禁用位不能由 locked 推断 enabled；never 必须显式断言。全部字段原因计数也精确核对。

专用 harness 创建随机命名、所有权标记的 PostgreSQL 容器，只绑定 loopback，清除继承的 ADTR/PG 配置，生成临时合成身份。可信 Go seed 仅调用 `SeedFixtureTx`，不接受客户端事实。实际 API 验证创建、幂等回执、全量统计与分页/筛选分离、跨用户/租户隔离、重启后报告/历史不变、当前权限撤销和再授予 epoch 隔离（旧报告/回执 404、历史清空、旧操作 ID 重建 409）。

必须执行且不能 skip/漏匹配的 9 个 PostgreSQL 测试：

- `TestPostgresAssetAssessmentPersistenceReplayAndPaging`
- `TestPostgresAssetAssessmentOwnerEpochAndSource`
- `TestPostgresAssetAssessmentAtomicRollbackAndImmutable`
- `TestPostgresAssetAssessmentTamperAndQuota`
- `TestPostgresAssetAssessmentConcurrentIdempotence`
- `TestSimulationAssetAssessmentHTTPAuthorityEpochAndRollback`
- `TestSchema32UpgradePreservesHistoryAndGrants`
- `TestSchema32FailureRollsBackPermissionAndTables`
- `TestSchema32ConcurrentUpgrade`

专用 Playwright 实际登录并选择保存域，真实 POST 创建响应先由 `route.fetch()` 完成，再只中断响应交付；查询原操作回执而非制造成功响应。再次 POST 明确携带 exact Origin + CSRF，确认同一回执、历史仅一份。实际重载后历史按钮打开原报告，核对缺失原因、组筛选不改总统计、恢复原回执及另外两个时间场景。固定 11 对象小于 UI 页长 20；实际分页验证由 API 每页 4 行完成，不能声称浏览器翻过第二页。

## 执行与证据

云工具链必须在同一 shell 加载：

```sh
source /workspace/scratch/2d420540fec7/adtr-recovered-toolchains/dev-env.sh
python3 -m unittest discover -s scripts -p '*_contract.py'
mkdir -p bin && go build -o bin/adtr ./cmd/adtr
npm ci --prefix web && npm run build --prefix web
cd web
npx --no-install tsc --noEmit --strict --target ES2022 --module ESNext --moduleResolution Bundler --skipLibCheck simulation-asset-assessment.playwright.config.ts simulation-asset-assessment-e2e/asset-assessment.spec.ts
npx --no-install playwright test --config simulation-asset-assessment.playwright.config.ts --list
npx --no-install playwright install --with-deps chromium
cd .. && python3 scripts/test_simulation_asset_assessment_integration.py
```

专用 workflow 固定官方 Action SHA / 共享固定 PostgreSQL 镜像，不扩展冻结的 engineering-ci 清单。只上传封闭 `result.json` 和两张带 SIMULATED 的小结果图片。统计图必须有 11 对象完整统计；历史图必须含实际报告 ID/场景/时间与历史计数，不能仅标题。截图先核对自然 viewport 与固定顶栏几何，不隐藏任何 UI。禁止发布凭据、数据库 URL、原始服务/Go 日志、浏览器 trace/video/DOM 快照。命名 PG 失败诊断仅包含退出码、白名单源码位置和 SQLSTATE。

2026-11-02 本地：7 个专用静态契约通过、strict TypeScript 通过、Playwright 发现 1 个测试、可信 Go seed 编译通过。实际 harness 因 Docker 不可用退出 1，`result.json.status=not-run`；PostgreSQL、实际 Chromium、图片视觉核验和当前提交 CI 尚未执行通过。上述离线结果不得替代运行验收，后续必须由专用精确 head CI 产生证据并独立复审。

## Final candidate review and local checks (2026-11-03 UTC)

Independent review identified old-epoch lookups returning403 instead of the specified404.
The backend now hides old-epoch reports/receipts with404 while missing current permission
remains403. Previously used operation IDs remain reserved: retries with the same or a
new report ID return409 without extra report/receipt/audit rows. The original PG test
includes these regressions; lookup fixes do not enable overwrites or duplicate inserts.

The final candidate passed full Go race/vet,1298 repository contract tests (143.524s),
116 UI files/5196 tests, typecheck/build,209-record requirements integrity and gofmt/diff
checks. A generated real11-object backend JSON fixture additionally passed client parser
and detail-transport validation. This is cross-language compatibility evidence, not a
replacement for the separate handwritten classification/count/reason oracle. An initial
fixture test file-URL error was fixed in test code only; its failed log remains distinct.

Independent source audit validated376 default/387 CI sources and74 wiring sources,
refreshing only three digest certificates. No old task guard, scheduler, admission budget
or CI selector was weakened; this slice is synchronous and does not pretend to be a Worker.

Inherited parent fixes are included: DNS resource-ID400/unauthorized404 assertions,
known schema31 profile compatibility extended explicitly to32 (33/future still denied),
and the actual DNS browser apply-filter/recovery steps. Thus the candidate does not omit
known parent corrections while dependency publication/CI proceeds.

Nine exact PostgreSQL tests and real API/browser execution remain dedicated CI gates.
Local missing Docker/DSN attempts are not-run/failed, never runtime passes. The observation
is an incomplete synthetic subset and report summaries are only its observed objects;
source-system/AD/Windows/capacity/product acceptance remains unverified,0/209 unchanged.
