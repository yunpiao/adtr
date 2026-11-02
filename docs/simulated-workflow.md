# 显式模拟业务工作流

关联 #251。用户于 2026-11-01 授权缺失业务依据先用模拟假设开发。
本批提供可运行的离线 JSON 工作流；它不启动服务器、不访问数据库、AD 或外部服务。
每次输出均包含 `simulation: true` 和 `contract: adtr.offline-simulation.v1`。
它不是生产 API，也不把输入中的 scope 或权限布尔值当成真实用户身份。

## 运行

使用仓库指定 Go 1.27.1：

```sh
go run ./cmd/adtr-simulate risk < examples/simulation/risk.json
go build -o bin/adtr-simulate ./cmd/adtr-simulate
```

示例 high 事件模拟权重为 30，因此输出 score=30，不能解释为真实业务风险评级。
CLI 参数为一个必需的子命令；省略、未知命令或无效输入退出 2，不输出结果。
输入来自 stdin、结果到 stdout、固定非敏感错误到 stderr；写入失败退出 1。

| 命令 | 输入 | 执行内容 |
|---|---|---|
| assets | assetassessment.Input | 状态、活跃度、组分类；见 simulated-asset-assumptions.md |
| groups | snapshot, query | 直接/传递成员图与诊断；见 simulated-group-assumptions.md |
| dns | records, filter | 全部匹配的规范化稳定排序记录；见 dnsip-simulated-contract.md |
| dns-xlsx | 同 dns | 同条件真实 XLSX，result.workbookBase64 为 base64 字节 |
| risk | riskscore.Request | 分数、分布、趋势、去重、重置；见 risk-simulated-contract.md |
| rules | scope, rules, events | 声明式规则编译与匹配；见 detection-simulated-contract.md |
| events | scope, provenance, events, filter, windowSeconds, threshold | 事件规范化/分页与失败窗口关联；见 events-simulated-contract.md |

每个输入字段使用相应包的 JSON tags；事件模型没有 tags 的字段使用导出字段名。
windowSeconds 是必需的模拟关联时间窗（1–86400 秒），threshold 是必需的触发数量；
取消这些参数将拒绝执行，扩大窗口会将更多事件关联，参数不来自真实威胁算法。
各包的版本/来源字段防止模拟结果被误标为真实观测；删除标记将失去可追溯性或被拒绝。

## 边界与门禁

stdin 上限 4 MiB，嵌套深度上限 64，拒绝非 ASCII 对象键、未知字段、大小写折叠后重复键、多个 JSON 值和 null。
预算用于防止异常输入耗尽资源，超过预算失败，绝不截断成成功；不作为生产容量承诺。
全部模块保留自身记录/遍历/匹配预算。输出先计算和编码再写入，校验失败不会输出半份结果；
输出管道本身失败仍可能已接收部分字节，调用者必须检查退出码。

本批新增纯业务计算和离线入口，不包括生产 HTTP/UI/持久化与真实授权接线。
独立模块的单元测试及 CLI 跨模块测试不能替代真实 AD、Windows、浏览器、容量和产品验收。
事件 case 状态机可由 Go 调用者使用，当前 CLI events 子命令只做查询和关联，不修改持久状态。
后续将按依赖继续敏感标签、白名单、规则管理、报告和模拟适配器，逐切片验证。

## 第二批交互式快照操作（#253）

新增命令：
- `rule-catalog`：init/create/edit/list/detail/test/publish-simulation/enable/disable/archive/export。
  输入 Request 见 rulecatalog-simulated-contract.md，输出 snapshot 可保存再作为下次输入。
  每次变更必须提供 expected_revision，避免同一快照的过期编辑；修改后必须重新测试，
  模拟 reviewer 记录不等于身份认证或生产发布审批。
- `labels`：敏感组和手工标签的合成推导；输入见 sensitive-labels-simulated-contract.md。
- `label-changes`：before/after 两个完整同策略输入，重算后输出变化；不完整或不兼容直接失败。
- `rule-exceptions`：action、access、snapshot 和对应动作字段；见 detection-exceptions-simulated-contract.md。
  init 只接受 access；add/edit 接 entry、expectedRevision；archive 接 id、expectedRevision；
  import 接 entries、expectedRevision；evaluate 接 events、matches、asOf。
  preview-from-event 接 event 及 entry 的 id/reason/rule_id/rule_digest/not_before/expires_at，
  只生成禁用草稿；export/list 不变更快照。各动作拒绝无关顶层字段。
- `case-replay`：scope、provenance、id、changes；最多1000条显式合成变更，从初始revision=1重放。
  任一冲突则整次无结果；输出最终case和审计值，不写数据库、不构成防篡改日志。

CLI 原有输入/键/字节预算仍生效；JSON snapshots 是不可信数据，模块重新验证。
同版本自洽的合成快照可以由用户重建；这些离线工具不提供跨进程 CAS 或身份可信性。
