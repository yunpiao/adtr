# 离线规则生命周期模拟契约

## 结论与依据

本包将规则创建、编辑、查阅、测试、模拟发布、启停、归档与导出做成真实可运行的
快照状态转换，便于缺少正式业务依据时继续验证开发。
依据为用户“缺少依据，先模拟一个依据”的授权、catalog AD-F-086/087/089/090/
093/101 的子集，以及 architecture.md 的草稿→夹具测试→审核发布不可变版本原则。
关联工程 Issue #253。它不完成这些需求的全部 UI、生产审批、持久化或实际检测接线。

契约标识 `adtr.rulecatalog.simulated.v1`；来源继承
`synthetic-business-contract-2026-11-01`。
标签说明数据是显式模拟输入，不能认证调用者、数据来源或审核人。
不存在真实 AD、数据库、网络、文件访问或外部通知副作用。

## 用户操作入口与 API

`rulecatalog.Execute(ctx, Request) (Response, error)` 接收：
- scope：必须显式指定 tenant/domain，且与快照、规则、事件一致。
- snapshot：上次响应的 snapshot；首次 init 必须为 null。
- action：下表闭集。
- expected_revision：每次写操作必须等于输入快照 revision。
- id：除 init/list 外必须给出。
- rule、cases、reviewer、query：只允许用于对应操作，误用参数拒绝。

Response 返回 snapshot，按操作附带 entries、entry 或 exported。
父层 CLI 可直接在严格 JSON 解码后调用 Execute，不需连接生产系统。
用户需自行保存响应 snapshot 并作为下一次输入；**stdout 输出不等于已持久化**。
本包不自动读写文件，调用者保存时应避免用 shell 同时读取和覆盖同一个文件。

| action | 操作与必要输入 |
| --- | --- |
| init | 空目录，revision=1；不接受已有 snapshot |
| create | 新 ID + rule；Version=1，Enabled=false |
| edit | 已有未归档 ID + rule；Version=当前草稿或最后发布版本+1；清除草稿测试 |
| list | 可选 query，仅对 ID 作区分大小写的字面子串匹配；包含已归档项 |
| detail | 返回指定 ID 的完整草稿、报告、发布历史及活动指针 |
| test | cases 至少包含一个预期命中和一个预期不命中；实际调用检测内核 |
| publish-simulation | 草稿通过绑定摘要的测试，reviewer 非空；保存不可变发布版本 |
| enable | 活动指针指向最新已发布版本，不允许只启用未发布草稿 |
| disable | 活动指针置零 |
| archive | 保留所有版本和证据、关闭活动指针；该 ID 后续写操作拒绝 |
| export | 返回指定 ID 的全部发布版本、摘要、夹具、测试与模拟审核人；不导出未发布草稿 |

发布后草稿/草稿报告置空；再次 edit 从发布版本建立新草稿。
编辑、测试和新发布均不改变现有活动版本；只有 enable 显式切换到最新发布版本。
无物理删除、取消归档、指定历史版本回滚或批量导出操作；
归档是本切片对“移除”的可恢复数据保留取舍，不冒充完整删除需求。

## 测试、摘要与模拟审核

TestCase 含 event 和 want_match。事件采用 detectionrules 的合成闭集；
报告保存候选规范 JSON 的 SHA-256、完整有界夹具和实际 Passed。
测试临时将候选复制为 Enabled=true 后真实求值，因此草稿禁用位不会跳过测试。
正/负期望都符合才 Passed=true；合法夹具不通过仍保存失败报告并增加 revision，
但不能发布。语法错误、跨域、重复事件或预算超限则原子失败。

发布的 Rule 保留 Enabled=false；活动指针是目录唯一生效状态，不能读取 Rule.Enabled
推断该版本是否活动。后续执行适配器必须显式选择 ActiveVersion，再在私有副本启用；
本包没有自动运行发布规则，也没有隐式接入生产任务。

每次请求重新执行输入历史中的夹具，并核验候选/报告摘要和实际 Passed，拒绝
“只把 Passed 改为 true”、陈旧候选报告、跨域夹具或缺失模拟审核人。
但用户掌握整个 JSON，能够一致地重写规则、夹具和摘要；这些自描述快照
**不是防篡改审计、可信测试证明或真实审核授权**。生产版本必须另接持久化、
服务端身份、权限、新鲜 MFA（如最终契约要求）、独立审批及可信测试证据。
reviewer 仅是明确的合成角色记录，既不发送审核请求，也不修改真实批准策略。

## 边界、错误与参数取舍

- 最多 64 个目录项、每项 8 个发布版本、每份报告 2–32 个夹具。
- 规则字段、条件、事件字段预算全部复用 detectionrules；任意代码与正则均不支持。
- Scope/ID/reviewer/query 的非空值最多 128 字节，UTF-8 有效，无控制字符和首尾空白。
- 全部对象 ID 唯一，发布版本严格递增，草稿版本大于最后发布版本。
- 版本和 revision 为 uint64，增加前防溢出；不自动丢弃历史换取容量。
- expected_revision 防止针对同一输入快照的错误修订操作，但不提供多个进程之间
  的原子 CAS；保存者必须提供真实锁或持久化事务，才有跨进程并发保证。
- ctx 必需；取消时不返回成功新快照。
- 所有输入在复制前校验预算，输出不与输入共享 map/slice。响应内同一目录项的
  多种视图可能互相共享对象，调用者应将返回快照视为单一结果。
- 接入层仍须限制原始 JSON 字节、拒绝未知/重复键；Go API 无法阻止调用前的分配。
- 查询也检查全部历史报告，换取对输入的一致性核验；副作用是历史增大会增加
  查询 CPU，预算尚无生产容量依据，不宣称吞吐量。

ErrInvalid 表示格式、快照关系或证明不一致，修正输入后重试；
ErrConflict 表示 revision 不匹配或无法递增，应重新获取真实最新快照，不能盲目改号；
ErrTransition 表示当前状态不允许操作，应按草稿/测试/发布顺序处理；
ErrNotFound 表示当前 scope 快照不存在目标 ID，应核对目标；
内核错误/取消直接返回，无部分修改或事件字段值泄露。
纯读取不递增 revision；每个成功写操作（包括重复 disable）递增一次。
输入和修改均在副本执行，失败不会改变调用者对象。

## 验证与未完成项

本包初次编译检查显示 no test files，仅作为实现中间状态，不算测试通过。
新增测试后执行：

```sh
source ../adtr-recovered-toolchains/dev-env.sh
go test -race -cover ./internal/rulecatalog
go vet ./internal/rulecatalog
```

测试覆盖完整生命周期、正负夹具、失败测试阻止发布、版本与活动指针分离、
编辑使报告失效、不可变旧发布内容、归档保留、查询/导出、不共享输入、
revision 冲突/溢出、伪造历史/摘要/Passed、作用域隔离、非法参数和取消。
证据只支持本离线包；CLI 接线、完整 CI、真实数据库、浏览器与产品验收另行记录。
无生产审批、GUI、规则库算法、持久审计、跨进程版本冲突保护或产品验收声明。
