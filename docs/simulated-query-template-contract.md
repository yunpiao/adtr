# 查询模板离线模拟契约 v1

状态：**合成开发依据，不是产品验收**。关联 [Issue #58](https://github.com/yunpiao/adtr/issues/58)
AD-F-134/135。2026-11-04 逐项读取 Issue 原文，基于 main
`11f18fbcefc12bf6526514fc7b70cb4dd0ca400c`。#58 仍开放，产品验收计数不变。

## 已有证据和未知事项

Issue 的 FLD-2216–2243 列出内置/自定义模板列表及其 id/name/params；
FLD-2244–2258 列出 tplName/content、删除 id 和未定义语义的 result。
UI-0413/0414 声明名称/搜索 maxlength=50。Issue 明确说明字段语义、默认值、
运算符、容量、权限、恢复及真实链路尚未冻结，且前置依赖 F33/F52/G01–03。
本实现不能把源码线索、模拟参数或测试成功升级成旧系统兼容证明。

本模块只保存和验证条件值，创建、列表、名称搜索、可恢复隐藏/还原。
没有 AD-F-136 保留期限或 AD-F-137 查询执行，没有 securityevents.Scope、
API/Worker/浏览器接线、数据库、迁移、鉴权、跨租户/域隔离、网络或部署。
server/DC/user/rule/type 是不解析不执行的合成标签，不代表已授权资源。
生产接入必须另行冻结权限、数据范围、审计、持久化、容量和真实链路契约。

## 明确的模拟假设

- 模板 id 由调用方给出，必填，1–128 Unicode 码点。name 必填，1–50 码点。
  采用码点而非 UTF-16 code units，因此50个 emoji允许；这不是旧前端行为复刻。
  名称/标签不 trim、不转换大小写、不做 Unicode normalization；
  前后空白、控制字符、非法 UTF-8 拒绝。名称大小写敏感精确去重。
- id 和名称在整个 Store（含内置、已删除项）唯一。失败创建不占用任何值；
  删除只隐藏，id/名称/容量仍保留，因此不会被新建占用而阻止恢复。
  同一 id 的删除和还原均可重复；未知 id 报 ErrNotFound。
  内置模板由 New 显式传入，没有伪造产品内置模板；内置不允许隐藏/恢复。
- 每个 Store 最多1000项，含内置和已删除项；进程结束全部丢失。
  这个限制只控制模拟内存预算，不是产品容量。没有清理、保留期、分页或磁盘持久化。
- List 的空搜索列出全部可见项；非空搜索最多50码点，按大小写敏感的子串匹配。
  两类列表分别按 name、id 的 Go 字符串顺序排列；空列表输出 []。
  存入和返回值均深拷贝，调用方不能通过切片修改已保存条件。
  Store 用互斥锁保护并发访问，不可在使用后按值复制。

条件值以下规则全部为**待真实业务定义替换的模拟假设**：

| 字段 | 模拟规则、必要性和缺省 |
| --- | --- |
| content | CLI create 必须显式对象；{}允许，表示没有条件值，不执行全量查询 |
| appName | 可缺省/空；最多100个不重复的0–2147483647整数；数值是标签不是产品枚举 |
| rulesID/dc/userList/types | 可缺省/空；每项1–256码点，最多100项，精确去重；没有解析和资源查找 |
| ipList | 可缺省/空；最多100个有效IPv4/IPv6文字地址，拒绝CIDR和zone；按原字符串去重，不规范化等价地址 |
| time | 可缺省/空，或恰好两个RFC3339Nano时间（双位小时、点号小数1–9位、偏移小时00–23/分钟00–59，不接受闰秒），起点严格早于终点；保存原字符串含偏移；不解释区间端点查询语义 |
| dataSource | 可缺省/空；最多100个唯一(server,appType)组合；server为1–256码点标签，appType为0–2147483647整数 |
| dataSource.appType | 模拟中可缺省，Go零值0；非产品默认或枚举证明。server缺省为空会被值验证拒绝 |

这些限制防止空/畸形值、不可恢复冲突与无界模拟输入。删除限制会接受未建模数据或
扩大内存/输出开销；保留限制的副作用是拒绝可能在未来真实产品定义中合法的值。
显式 null 一律拒绝，缺省数组与 [] 都表示空值，存入后规范化为 []；
不将 null 推断为“未知”“继承”或无限范围。时间、IP和标签不会触发执行。

## 严格 JSON 和 CLI

`internal/querytemplates.Decode` 对目标结构逐层校验：仅精确大小写字段名，
拒绝重复键（含转义后重名）、未知字段、null、错误类型、非整数字面量、
非法UTF-8、孤立UTF-16 surrogate、多个顶层值或尾随垃圾。
最多1MiB输入、深度16、单JSON数组1000项，错误不改变目标值。
这些是模拟解析预算，不是 HTTP 或产品请求限制。

`cmd/adtr-querytemplates` 只从标准输入读取一个场景，不接受文件路径、网络参数、
环境配置或真实身份字段。每次启动独立 Store，不加载产品默认模板。
可选 `defaults` 数组只用于显式合成内置模板；省略或 `[]` 保持旧空 Store 行为。
每个元素必须有合法 `id`、`name` 和显式对象 `params`；`params:{}` 表示空条件，
其条件字段沿用上表，不执行查询。缺省 `params`、`null`、错误类型或无效模板均失败。
CLI 通过既有 `New` 初始化这些值，因此内置/自定义名称与 ID 共享唯一性及1000项总预算，
内置项同样参与大小写敏感名称搜索和稳定排序，却不能 delete/restore。
`defaults` 不是权限、可信来源、持久化导入或产品内置规则声明；任何人可重建合成输入。
需要验证非空 `defaultTplList` 时必须提供它，删除它后仍能运行旧场景但内置列表为空。
它增加本次场景内存/输出量并占用现有总容量，不增加1MiB输入或1000项数组上限。
初始化任意一项失败时不运行 operations，也不输出部分列表。
场景必须含1–100个 operations。支持：

- create：op、id、tplName、content 必须存在；拒绝 search。
- delete/restore：仅 op、id。
- list：op；可选 search。每次列表快照追加到输出 lists。
- 任何未知操作或混入其它操作字段均失败。

成功输出 `{"simulation":true,"lists":[...]}`；这是模拟结果，不使用 Issue 中语义
未冻结的 result 字符串。输入解析/值校验/场景操作失败时退出1，不输出部分场景结果。
整个场景先在内存完成才写 stdout；若输出设备自身失败，无法撤销已被设备接收的字节。
错误仅输出固定类别，不回显输入原值。没有外部副作用、恢复磁盘记录或后台任务。

示例只使用合成值：

```sh
printf '%s' '{"operations":[{"op":"create","id":"synthetic-1","tplName":"合成模板","content":{"ipList":["192.0.2.1"]}},{"op":"list"},{"op":"delete","id":"synthetic-1"},{"op":"list"},{"op":"restore","id":"synthetic-1"},{"op":"list"}]}' |
  go run ./cmd/adtr-querytemplates
```

显式合成内置与自定义并存示例：

```sh
go run ./cmd/adtr-querytemplates < examples/simulation/querytemplate-defaults.json
```

该样例只有人工合成条件值；不是产品预置模板，也不连接样例域名或 IP。

## 具名测试与验收缺口

- TestLifecycleAndIsolation：FLD-2216–2258 的合成值往返、全部条件切片的深拷贝、
  隐藏与还原、重复操作、已删除id/名称保留、内置只读和未知id。
- TestOrderingNamesAndCapacity：稳定排序、大小写、名称/搜索边界、Unicode假设、容量。
- TestValidateConditions：整数、标签、时间、IP、来源组合、数组预算及失败不写入。
- TestStrictDecode：重复/未知字段、null、UTF-8/surrogate、整数类型、预算、失败不改目标。
- TestConcurrentLifecycle、TestNewFailureAndDefaultCopy：race、构造失败不返回半成品。
- CLI TestScenario、TestFailureNoPartialOutput、TestIOFailures、TestBudgets：
  实际场景流转、进程内隔离、错误无部分结果、I/O失败与输入预算。
- #292 / AD-F-134、FLD-2216–2229、UI-0413：CLI 的 TestDefaultsFullValuesAndIndependentLists、
  TestDefaultsSearchAndInvocationIsolation 核验非空内置八字段原值、独立列表快照、
  名称搜索/排序、缺省/空数组逐字兼容与跨调用隔离。
  TestDefaultsImmutableAndReservedNames、TestDefaultsStrictInputsAndAtomicFailure、
  TestDefaultsShareExistingCapacity、TestDefaultsUnicodeBoundariesAndIO 覆盖只读/冲突、
  严格 JSON 与固定错误、零部分输出、1000项共享边界、Unicode 和写入失败。

可复现命令：
`go test -race -count=1 ./internal/querytemplates ./cmd/adtr-querytemplates`；
全仓门禁仍以 `make check`、集成/生命周期、精确head CI及独立复审为准。
模拟测试不能覆盖权限、PostgreSQL、页面、真实AD、八版本Windows或产品容量。
本切片没有实现这些链路，全部仍待验，不关闭 #58。
