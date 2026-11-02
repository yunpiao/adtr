# AD-F-055–059 身份风险评分：模拟契约 v1

结论：`internal/riskscore` 提供可调用、确定性、无外部副作用的风险事件重放计算；它不是经验证的 AD 风险模型，不代表 Issue #39 或五项产品需求已验收。

## 依据与假设

已核对 `requirements/catalog.json`：AD-F-055 分布、056 趋势、057 变更记录、058 重置、059 检测结果更新均属于 F17 / Issue #39。目录仅规定功能目标，未给出经确认的评分公式。本契约明确新增**模拟假设**，不冒充源系统算法、真实威胁结论或业务阈值。

版本 `simulated-risk-v1` 固定 low=5、medium=15、high=30、critical=50。当前分数从 0 起累加到 100 截断；没有时间衰减、自动解封、远程响应或外部通知。权重不是配置项，变更必须发布新版本并保留历史版本语义。`Weights()` 返回副本，不能修改引擎策略。

## 可调用 API

```go
result, err := riskscore.Evaluate(riskscore.Request{
    Scope: riskscore.Scope{TenantID: "synthetic-tenant", DomainID: "synthetic-domain", UserID: "synthetic-user"},
    AsOf: observedAt,
    Events: []riskscore.Event{{
        ID: "synthetic-event-1", Scope: authorizedScope,
        Kind: "high", OccurredAt: detectedAt,
    }},
})
```

`Evaluate(Request) (Result, error)` 不修改输入、不访问网络/数据库、不读取系统时钟。调用者必须加载完整的已授权单用户证据集合，不能把增量批次当成完整历史；调用者也必须在接受重置前执行身份、当前租户/域/用户权限检查并持久化审计。本包只拒绝 scope 不一致，不提供身份认证；不能直接用客户端提供的 Scope 作为授权证明。

输入参数：

| 字段 | 用途、必需条件与省略影响 |
| --- | --- |
| `Version` | 可选；空值选择固定 v1。非空未知版本拒绝，防止静默换算法。 |
| `Scope` | 必需，TenantID/DomainID/UserID 均为非空、无首尾空白、最多128字节标识；缺失拒绝。隔离调用而非授予访问权限。 |
| `AsOf` | 必需非零时间；界定可见证据、消除隐式当前时间。缺失拒绝。晚于该时间的有效唯一证据计入 IgnoredFuture，不参与当前/历史输出。 |
| `Events` | 可空，空输入没有风险证据而不是已确认安全。每项 ID、Scope、Kind、OccurredAt 必需。ID 最多128字节；Kind 必须是四类之一。 |
| `Resets` | 可空，无重置则从全部有效历史累计；每项 ID、Scope、At、Reason 必需，理由最多512字节。重置改变模拟当前分数而非删除证据。 |

零时间、未知类别、跨 scope、空或首尾带空白标识、无理由重置、超限批次均整批返回 `ErrInvalid`，不产生部分结果。Events 与 Resets 合计最多 `MaxEntries=10000`（含重复/未来项）；这是内存和排序预算，不是生产容量保证。超过预算应由后续持久化快照设计解决，不能静默截断证据。

## 去重、时间、重置与输出

- 同一调用的事件/重置共享 ID 空间。相同 ID、相同规范化证据只计算一次；不同内容返回 `ErrConflict`，不按先到者覆盖。时间按瞬间比较并统一输出 UTC；相同瞬间的不同时区表示属于相同证据。
- 所有输入（包括未来证据）先验证，再重放。不同作用域的记录即使在未来也拒绝，不能通过时间过滤隐藏非法输入。
- 排序键：时间、普通事件先于重置、ID 字典序。重置将该用户分数和当前分布清零，边界 **包含** 重置时间；同一瞬间的事件不会穿过边界。边界前迟到事件会更新历史，不会重新增加当前分数。
- 多个重置按相同规则重放；历史记录始终保留。`ResetBoundary` 是最后一次可见重置时间。删除已保存的重置会改变当前分数，因此调用者必须保存而非通过遗漏“撤销”。
- `Result` 始终输出 `Version`、`Simulated=true`、Scope、AsOf、0–100 的 `Score`、当前有效 `EvidenceCount`、Duplicates、IgnoredFuture、可选 ResetBoundary。
- `Distribution` 固定 low/medium/high/critical 顺序，给出 Count、Weight、RawPoints。它只统计最后重置后的证据。RawPoints 是未截断值，其和可超过100；不能将其误称为截断后分数归因。
- `Records` 包含全部可见唯一事件与重置的 ID、At、Kind、可选 Reason、Before、After、Delta；饱和后的事件 Delta=0 仍保留记录。记录中重置 Kind="reset"。
- `Trend` 每个证据时间点保留该时间点所有记录处理后的分数，不补造日桶或没有观测的时间点。空历史返回空数组和分数0；这仅表示当前输入没有证据。

该重放模型支持 AD-F-059 的合成检测结果计算接口；检测器映射、生产事件身份、持久存储、真实 AD 数据质量和来源、授权路由及完整 UI 必须由集成层明确验证，不由纯函数测试代替。

## 本次验证

基线：`go test ./internal/directoryassets` 通过（缓存命中）；新增前无 riskscore 包。

使用项目已校验工具链 `source ../adtr-recovered-toolchains/dev-env.sh`：

- `go test -race -cover ./internal/riskscore`：通过，语句覆盖率 100.0%。
- `go vet ./internal/riskscore`：通过。
- 测试覆盖分布、趋势、记录、乱序确定性、重置及同时间边界、迟到历史、重复和冲突、0/100分边界、饱和记录、空输入、未来过滤、时区规范化、三维 scope 隔离、缺失/非法输入、可变权重隔离、10000/10001 条边界。

这些是纯 Go 合成测试证据；不声称 PostgreSQL、浏览器、生产评分准确性或真实 AD 验收通过。产品验收计数不因本实现改变。
