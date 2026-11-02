# #253 模拟策略工作流验证

基线 PR252 head e4ca1642ccd7e8c3bd7a72555fd64068547b0940。2026-11-01。

新增 rulecatalog、sensitivelabels、detectionexceptions 及 CLI 操作接线、case-replay。
本地 `go test -race -count=1 ./...` 与 `go vet ./...` 通过；最终四目标
`go test -race ./cmd/adtr-simulate ./internal/rulecatalog ./internal/sensitivelabels ./internal/detectionexceptions`
通过，含 CLI 初始化→创建→正负测试→模拟发布→启用→归档端到端快照流程。

独立复审发现 archived+enabled 不可能状态仍被快照接收（P3）。已增加拒绝规则和
List/Export/Evaluate 回归；独审重现现在 exit2 且无结果，最终无待修具体问题。
复审确认候选摘要和历史夹具实际重跑、修订冲突、归档保留，标签比较拒绝不完整
或策略/覆盖不兼容输入，例外保留原始匹配且必须显式作用域/生效区间。

未改前端、生产 API、schema 或 workflow；未把第一批的 Python/前端测试当成本批新执行。
精确 PR CI 与合并后 main 仍须单独验证。输入快照不是可信身份/防篡改存储；真实产品验收0/209。
