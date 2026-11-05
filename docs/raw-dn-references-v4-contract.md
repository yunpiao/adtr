# 同观测 DN 原文反向组引用

工程 Issue #284；原需求 #30 / AD-F-043，工作簿“AD 功能清单”第47行“查看对象所属组和组内成员”，FLD-0481–0488 的对象/DN 输入及组列表输出。原需求支持关系查看方向，不证明逐字 DN 比较等同于完整 AD 所属组语义。本切片只接通现有保存观测中的文本引用，不提升原功能或产品验收。

## 数据和语义

目标对象与候选组都来自同一已保存、完整校验的 dictionary4 观测。以 objectGUID 定位保存对象，把它的 distinguishedName 与每个 group 保存的 member 值逐字比较，命中组按 GUID 升序且每组只出现一次。

不 trim、大小写折叠或解析 DN 转义等价形式，不展开嵌套，不补 primary group，不跨域、跨观测或推断有效权限。观测保证 GUID 唯一，不保证 DN 唯一；相同 DN 的不同 GUID 会得到相同文本引用结果。多页采集不是 AD 事务快照，引用不证明当前目录的实际身份或关系。

member 未返回的组保持未知，返回空数组仅表示采集返回零值。零匹配应显示“本观测未发现完全相同的 DN 文本引用”，不能显示“没有所属组”。界面必须显示未知组数量和原观测来源。

本功能继承 dictionary4 的有界原始 member profile：每组最多256值、单值4096字节、总计65536字节；range 属性不受支持并使采集失败，不能把未采到的大组当零匹配。上述限制是已有观测的可验证容量边界，不是 AD 的实际组大小上限；本次不扩大或降低它们。

## 只读 HTTP 契约

GET `/api/directory-assets/v4/raw-dn-references`，只接受七个必需的单值参数：

- domainId、expectedRevision、expectedCredentialRevision：核对当前域资源、连接及凭据版本。每次读取均必需；缺少或旧值必须拒绝，不能放宽为只检查观测存在。
- observationId：固定成功的 dictionary4 观测，必需。指定观测不可用返回409，不能自动回退最新观测。
- objectGUID：固定该观测的目标对象，必需；目标不存在返回404，不据此推断已从 AD 删除。
- pageIdx：1..10000；pageSize：25、50或100。二者必需，限制响应大小并允许稳定翻页；超界拒绝，不截断或静默改值。

响应字段固定为 dictionaryVersion=4、selection、observationId、source、object、list、page、coverage。object 使用现有 PublicObjectV4，允许 user/group/computer；list 为匹配 group 的 PublicObjectV4。page 使用现有 pageIdx/pageSize/total/totalPage，零结果总页为0。coverage 为整份观测的 groupCount、memberReturnedGroupCount、memberNotReturnedGroupCount；后两者相加为前者，匹配总数不超过已返回 member 的组数。

复用现有完整观测加载器：成功任务、源绑定、当前配置/凭据/账户/策略版本、body 摘要、条数、规范编码与全部对象字段均先验证。不能先筛选再验证，未选中对象损坏也必须整份拒绝。

## 安全与容量

读取需要当前登录、domains.readable 与 directory_assets.readable 的交集及显式域资源权限；沿用 schema/tenant/actor 事务检查和审计上下文。handler 仅注入保存观测的 Store，不能提交任务、解密凭据或连接 LDAP；无需使用权或生产者权限。此变更不授予任何权限，不改采集器或数据库 schema。

沿用10秒请求预算与8MiB完整编码上限。预算约束保存观测读取的占用时间，上限防止文本 JSON 转义膨胀；两者为本只读路由的必要守卫，移除会扩大资源占用，副作用是过慢或过大的合法观测也会明确失败而非返回部分成功。响应使用 no-store、nosniff 和当前 actor 标头，编码完成后才输出成功字节。

前端核验严格字段、类型、选择版本、观测和 actor 绑定，组 GUID 唯一有序、每组原始 member 精确包含目标 DN、分页及覆盖计数一致。切换域、类型、对象、分页、关闭、退出、撤权或重读时隔离旧请求与迟到响应；401/403/409 必须清除失效数据。

## 验证与交付边界

以真实保存 body 独立计算预期关系；现有隔离 TLS LDAP 夹具的 group-03 精确引用 user-01 和 group-02。只用合成数据，不连接真实 AD，不为测试向生产观测写入关系。保留原组→原始成员流程及旧证据门禁。

必须覆盖同 DN 不同 GUID、逐字不等、自引用、未返回/空 member、分页排序、输入与响应上限、坏未选对象、权限/域/租户/旧绑定、取消与迟到响应。实际 API/Worker/PostgreSQL/TLS LDAP/浏览器链路和桌面/移动 PNG 由精确提交 CI 及独立复核验收；本地无 Docker 不能算集成通过。

浏览器证据由同一原生 Response 的读取记录产生解析值、字节数及 SHA，并绑定请求身份、actor、no-store、未取消 EOF 与 reader release；校验器另从保存的观测 body 独立重算文本引用。manifest 不另存 HTTP 原始字节，因此离线不能单独重算 HTTP SHA；这一关联依赖已绑定精确源码的同 Response 采证链，不能把它描述为独立 HTTP 原字节归档。

工程提交、测试、合并和产品验收分开记录。209项/57组/3258字段/804控件与0/209产品验收保持不变，原 Issue #30 保持开放。
