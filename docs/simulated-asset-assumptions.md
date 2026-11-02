# 模拟资产分类依据 v1

**结论：这是已可执行的合成证据分类器，不是真实 AD 状态、风险算法或产品验收。**
工程 Issue #251；覆盖 AD-F-038/039/040/042/052 的有限分类计算切片。
入口 `internal/assetassessment.Evaluate(Input) (Result, error)`，不接生产 HTTP、数据库、LDAP、
凭据、网络、通知或远程响应。风险分数由其他模块负责；本模块不提供列表、汇总统计、持久化或授权。

## 依据和版本

由于旧业务状态优先级、空值和活跃窗口尚未冻结，本模块明确提出可替换的**模拟假设**。
输入必须使用 `version="simulated-asset-v1"`、`source="synthetic"`，结果保留两个标记。
它们用于阻止调用者无意混用契约；省略、错版、非 synthetic 均报错。标记不是真实性证明，
调用者仍须确保只送合成夹具，绝不能把真实观察改标为 synthetic。
更改阈值、优先级或语义须发布新版本；不能改同版本含义或覆盖历史结果。

`tenantId/domainId/objectId/observationId` 必需，绑定结果所属合成对象和证据，最多256字节，
禁止首尾空白和ASCII控制字符；删除任一值将拒绝计算。它们不是授权凭据。
若将来接服务，必须在读取证据前按真实会话执行租户/域/功能授权，并使用独立模拟存储；
当前库没有任何身份检查，不能直接用作对外业务端点。

`observedAt/asOf` 必需、非零，分别为固定证据时刻和评价时刻，前者不能晚于后者。
输出统一 UTC；算法不读取系统时钟，重复输入得到同一结果。删除时刻将拒绝计算；
副作用是消费者必须显式选择评价时点，不能将历史分类误当现在的状态。

## 冻结的模拟判断

| 判断 | v1 依据 | 影响和限制 |
| --- | --- | --- |
| 新鲜度 | `asOf-observedAt > 24h` 为过期；等于24h仍新鲜 | 过期的所有适用分类均 `unknown/stale_observation`；不会沿用旧正常状态。24h为模拟阈值，不是AD服务保证 |
| 账户状态 | disabled=true 优先；disabled=false 且 locked=true 为 locked；两者false为enabled | disabled未知时即使locked=true也返回unknown，因优先级无法确定；disabled=true允许locked缺失。enabled只表示这两个模拟标记明确为false，不证明可登录 |
| 活跃度 | `login=observed`且`asOf-lastLogin >= 30*24h` 为inactive，否则recent | 等于30天即inactive；不使用本地日历/月长度。recent只说明所给最后登录时间在窗口内；不证明在线或无风险 |
| 未曾观察登录 | `login=never` 且无lastLogin 为never_observed | 必须是显式合成来源断言；缺时间不能推断never，也不能推断inactive |
| 组类型 | securityEnabled=true 为security，false为distribution | 不从组名称、SID、成员数量或任意其他字段猜测；nil为unknown |
| 组范围 | 明确给定global/domain_local/universal 原样分类 | 空或unknown为unknown；不推断递归成员、跨域或权限有效性 |

`account` 对 user/computer 适用，`group` 对 group 适用，传入错误类型证据会报错。
证据对象与可空布尔值均非必需：缺失返回 `unknown/missing_evidence`，不是false。
`login` 缺失等价unknown；observed必须有非零lastLogin且不晚于observedAt；
unknown/never带时间视为矛盾而拒绝。组scope非法枚举也拒绝。
输出对不适用的分类固定 `not_applicable/asset_kind`，区分“不适用”和“证据未明”。
参数选项不会开启采集或授予权限；阈值作为版本常量不可由请求覆盖，防止同版本结果漂移。
保守unknown可能增加待核对数量；处理方式是提供新的、有明确来源的合成证据，不填造false。

错误统一为 `ErrInvalidInput`，不回显标识、输入或部分结果。先验证再计算，过期不能掩盖非法输入。
库无可变全局状态、不改输入、不持有指向输入字段的输出引用，可并行用于独立输入。
调用期间调用者不应并发修改同一输入指针对象。

## 最小合成示例

```json
{
  "version": "simulated-asset-v1", "source": "synthetic",
  "tenantId": "synthetic-tenant", "domainId": "synthetic-domain",
  "objectId": "synthetic-user", "observationId": "synthetic-observation-1",
  "kind": "user", "observedAt": "2026-11-01T12:00:00Z", "asOf": "2026-11-01T12:00:00Z",
  "account": {"disabled": false, "locked": false, "login": "observed", "lastLogin": "2026-10-02T12:00:00Z"},
  "group": null
}
```

结果 `accountStatus=enabled`、`activity=inactive`，原因为明确flags均false、登录恰好30天边界；
组字段not_applicable。30天边界是假设，不能声称原系统已经确认。

## 验证与未完成范围

新增前基线 `go test ./internal/...` 通过。
本模块 `go test -race -cover ./internal/assetassessment`、`go vet ./internal/assetassessment` 通过，
当前语句覆盖率100%；覆盖9组布尔真值表×用户/计算机、30天±1ns、24h±1ns、
缺失/never/未来/矛盾时刻、非法scope/kind/版本/来源/标识、UTC、重复执行与输入不变。
这些只是隔离合成单元验证；整仓检查、真实PostgreSQL/浏览器、精确提交CI由整合流程独立记录。
真实AD/Windows、完整源系统字段/状态枚举映射、持久化、容量及产品验收均未完成，0/209不变。
