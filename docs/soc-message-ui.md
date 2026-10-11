# F40 站内消息固定查询工作区

当前单条已读与未知提交恢复在后继 [#107](https://github.com/yunpiao/adtr/issues/107) 切片接入，见 [单条已读恢复契约](message-read-recovery.md)。下文“只读”描述 PR106 首片范围；查询时快照、分页与计数语义继续有效。

Refs [#105](https://github.com/yunpiao/adtr/issues/105)、[F40 #50](https://github.com/yunpiao/adtr/issues/50)。依赖 [PR #104](https://github.com/yunpiao/adtr/pull/104) 的真实 health 消息运行时，开发基线 `1ee7d1f673ec55505b66e301ad16460956365652`。后端合并、UI 合并、实际浏览器证据及产品验收分别记录，不提前解除依赖。

## 来源与已确认结果

安全运营 → 站内消息。仅当前用户收到的真实 `infrastructure.health` 任务终态，由 Worker → outbox/event → 消息投影提供；类型为 `task_result`，平台范围，五个当前登记终态可筛选。任务 ID、消息 ID、发生/入库时间和静态脱敏说明原样取 API；不加入原型消息、虚构来源、原始 payload 或任务证据。

- 默认 `readFilter=all`、`sort=desc`、`limit=20`，显式确认后才 POST query；进入、StrictMode、恢复导航、定时器均不创建快照
- 页长 20/50/100/200；它是当前 UI 选项，不是容量承诺。GET page/locate 使用当前快照的页长，不受未确认草稿影响
- 时间输入明确 UTC，接受 RFC3339 UTC 文本，保留 1–9 位小数原精度。两端同时为空使用服务器确认前精确168小时；显式范围开始包含、结束不包含；禁止单边界/反向/无效时间。不会经过 JavaScript Date 序列化丢失纳秒
- 关键词仅匹配标题，字面、区分大小写；当前子类型明确归属 task_result
- 已确认的 counts 来自阅读筛选前基础集合，列表 total 含阅读筛选；显示全部/查询时未读/查询时已读/当前筛选总数，不能以当页长度代替总数
- 固定 resultId、确认时间、有效时间完整显示；分页与定位持同一 resultId。定位只按消息 ID，并在成功读取该固定页后高亮和键盘聚焦，未命中不创建查询
- 草稿变更有明确提示，旧结果、页长、统计保持原确认含义。此片没有已读写入，也没有批量/时间线分组/appName/旧枚举或任务详情跳转

## 副作用、故障与身份边界

POST query 创建服务端派生快照，非幂等。忙碌同步锁防双击，不能自动重试或轮询。停止等待、网络/无效响应/5xx 都可能发生于提交之后，界面明确说明快照可能占用配额；再次确认会创建新快照。服务端每用户8、每租户128、全局1024及10分钟期限是保护策略；UI 不推断可用时间或虚构倒计时。

GET page/locate 的手动重试保留原目标与 resultId；草稿不能改写重试。410 清旧结果并要求重新确认；404 清旧结果但只说明当前不可取得；429 说明活动配额且没有服务端可用时间；422 要求缩小时间范围，说明关键词不能绕过扫描预算；预算失败不输出部分成功。

读取需要 `tasks.readable` 和当前三条精确操作授权，服务端每次最终鉴权。无 MFA/任务写权限的用户可读取自己的消息，不增设写授权。401/强制改密转 App 复核；403 清私有结果与操作能力并要求重新核验。组件以账户 ID、用户名、CSRF 会话身份重建；请求 Abort 与代际共同阻止卸载、取消、晚 JSON、旧分页/定位恢复旧结果。没有 private response/resultId/filter/CSRF 在浏览器存储或历史中的持久化。

## 验证层次

`message-api.test.ts` 与 `MessagesWorkspace.test.tsx` 使用模拟传输验证请求与页面状态，不代表真实消息来源。导航精确列表更新保留所有原入口。真实 `messages.spec.ts` 使用独立合成 bootstrap 数据库，真实改密/MFA、两次健康任务提交、Worker、PostgreSQL outbox/event/message 只读联接和浏览器确认；SQL 连接强制只读，等待真实投影不轮询 POST query。

真实 API 的 limit=1 两页验证两条实际健康消息的固定成员；UI 仍只提供20/50/100/200，21项 UI 翻页由明确 DOM 夹具覆盖，不能混称21条真实来源。真实浏览器同 resultId 定位、任务/消息 ID 核对、UTC、草稿隔离和零自动 POST 验证后输出1440×900、390×844视口/全页四张截图与提交/文件/二进制哈希来源 manifest。

messages suite 在 runner choices、Worker集合、独立 CI matrix 显式登记；保留原35 gates，新增为36，verify 聚合继续涵盖所有 browser。本地实际真实 runner 因缺少 Docker 失败；截图须等待精确 head CI 实际生成后独立像素复核，类型检查/发现/DOM通过不能替代。未关闭 #50 或改变产品验收0/209；无生产部署或真实AD接入。

## 本地开发检查点（2026-10-11）

以 PR104 精确 head 的既有1311项DOM及后端全检查为基线。此次最终候选完整 `make check`
已通过1375项DOM、100项Python、Go race/vet/build、TypeScript/生产构建、依赖审计0漏洞。
全量格式检查与32项浏览器用例发现通过；真实 messages runner 实际尝试因缺 Docker 失败。
独立实际diff复核发现并修复损坏page与counts矛盾、固定页内容改变、定位index不符的拒绝缺口；
补充已确认筛选/排序/纳秒半开边界、非默认阅读计数、取消定位、原定位重试和迟到分页回归后复核通过。
作者自审另行执行。桌面/移动像素复核、精确head36项CI及当前main兼容性仍待运行结果。

首轮 UI CI `38111202021` 的 messages 用例在两次真实health任务和outbox/投影核对后，
因原包裹select的标签包含选项文本而未通过精确标签定位。修复复用现有Field的独立aria-labelledby，
保留E2E严格匹配、断言与超时，并局部隔离Field样式与定位说明span。独立源码/产物CSS顺序复核完成。
修复候选完整make check通过1376项DOM、100项Python和其余全部门禁，最终CSS另经类型/生产构建通过。
新增messages矩阵项调整到首位以提前反馈；原35项、并行度和verify保持，仍需全36精确新head运行。

## main 依赖整合检查点（2026-10-11 06:42 UTC）

PR104 已正常合并至 `7913321590acea3d61e696bd251bd30e75e04f64`，包含已合并的 PR102/103。
UI 普通双亲整合该精确 main；唯一 CI 相邻证据步骤冲突已保留 message/domain/system 三者。
独立整合复核确认除 App/CI 外所有继承文件与 main 逐字节相同，原消息实现与 fc7f28a 相同；
App 保留同域 Back 队列/身份核验修复、消息接线保持最小。相对 main 仍仅17个消息文件。
整合候选完整make check通过1418项DOM、100项Python、Go race/vet/build、类型/构建/audit0，
全量格式与32项浏览器发现通过。新精确head的36 CI与真实四图再次运行；不会用旧stacked绿色替代。
工程Issue105按实际证据更新，完整F40 #50继续开放，产品验收0/209不变。
