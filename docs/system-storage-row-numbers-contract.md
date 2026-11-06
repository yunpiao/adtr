# 存储列表本页序号（UI-0646）

## 需求与当前范围

原始 [Issue #14](https://github.com/yunpiao/adtr/issues/14) 的 AD-F-199
“存储管理”记录 UI-0646 为 `el-table-column`，标签“序号”，属性
`{"type":"index"}`。该控件没有直接数据绑定。本候选补齐当前存储表格的
展示编号，列名为“本页序号”，避免把它解释为跨页排名或资源标识。

本页序号只按当前已通过现有响应校验的 `storage` 数组顺序显示 `index + 1`。
每页从 1 开始，第二页的两行也显示 1、2；不使用
`(page - 1) * pageSize + index + 1`。序号随当前返回顺序改变，不是稳定身份。
服务端缩减返回集合时按剩余行重新编号，本切片没有增加搜索、筛选或排序入口。

当前后端的受信任注册仍只有 `local-api` / `runtime-root`。多目标、第二页、
重排和缩减集合用合成 DOM 数据检验展示逻辑，不表示已提供多磁盘注册，也不证明
真实后端或真实浏览器的第二页可达。实际采样与设置边界以
[系统健康契约](system-health-contract.md) 为准。

## 实现与保留行为

`web/src/SystemWorkspace.tsx` 的存储表格仅增加第一列表头、`map` 的 `index`
参数和普通 `td` 中的 `index + 1`。

- `index` 是数组遍历提供的当前位置，仅用于这一展示需求；实现本页编号需要它，
  原有读取、分页和修改不需要它。移除此列会恢复编号缺失，保留空白列不是完整实现。
- 新列占用额外横向宽度，仍使用既有 `table-scroll` 容器。真实浏览器窄屏布局尚未验证。
- 资源 ID 继续是 `tr` 的 React key、`th scope="row"` 的行标题，以及操作按钮、
  编辑路由、设置请求的目标身份。不能用序号替换这些位置。
- 翻页和每页条数只使用既有 `instance/page/pageSize` 查询；每页条数仍为
  10/20/30/40/50，更换条数仍回第一页。编号不触发额外读取、写入或预取。
- 刷新和 15 秒轮询采用原有生命周期；已离开的请求被取消，迟到响应不能重建旧内容。
  空数组只有表头和既有空态，不制造编号为 0 的数据行。
- 缺失指标继续为不可用，字节数不补零。陈旧观测继续显示原陈旧提示，告警状态仍要求
  重新确认。编号不表示采样可用或目标健康。
- 系统菜单、读取操作及写入操作的既有权限检查保持。只读用户可见编号和资源身份，
  不会因此获得阈值修改入口；错误或失效会话清除旧数据。
- 告警设置仍绑定选中 `StorageItem.id`、`revision` 和 `alarmPercent`，阈值仍为
  85–90 的整数。`expectedRevision` 用于比较交换，修改时必须提供；移除会失去
  明确的并发版本前提，并被既有接口拒绝。版本冲突仍要求重新读取后另行确认修改。
  证明字段、CSRF、重复提交锁、取消及迟到写入处理均不变。

本候选不改 API 类型或校验器、后端、查询排序、URL 格式、权限、CAS、清理策略或
真实采样来源；不会把序号作为新的请求字段或持久化字段。

## 本地测试证据与未验证边界

`web/src/SystemStorageRowNumbers.test.tsx` 使用真实组件与现有 API 序列化/校验代码，
仅以合成 `fetch` 响应代替网络。20 项 DOM 用例覆盖：

- 第一列及资源行标题、第一页、第二页重新从 1 开始、上一页和每页条数。
- 原 15 秒轮询重排后的编号与 DOM 行身份、服务端返回集合缩减、空页。
- 缺测、陈旧、菜单与操作权限、只读、非法响应、403、503、网络错误、401。
- 换页尺寸时取消旧读取并忽略迟到结果、第二页选中目标的 ID/版本/证明/CSRF、
  重复提交、CAS 冲突后的显式重读、取消后迟到写入不恢复编辑器。
- 请求数量与原查询键不增加，普通展示和读取不发送设置请求。

在本地候选上执行以下命令：

```sh
node web/node_modules/vitest/vitest.mjs run src/System.test.tsx --root web
node web/node_modules/vitest/vitest.mjs run src/SystemStorageRowNumbers.test.tsx src/System.test.tsx --root web
(cd web && node node_modules/typescript/bin/tsc --noEmit)
node web/node_modules/prettier/bin/prettier.cjs --check web/src/SystemWorkspace.tsx web/src/SystemStorageRowNumbers.test.tsx docs/system-storage-row-numbers-contract.md
git diff --check
```

改前系统回归基线 182/182 通过；候选新用例 20/20 加既有系统回归 182/182，
合计 202/202 通过。首次新测试中无样本行误用 `observedAt: null`，被既有元数据
校验拒绝；已改为后端零时间字符串表示，并保留容器级 `observedAt: null`。
没有放宽校验器或改变生产代码来迁就 fixture。首次失败与修正后通过分别留存。
类型检查首次运行发现本测试查询辅助函数误用了角色查询不支持的 `exact` 选项，
已删除该选项；同时发现并行编写中的浏览器证据辅助文件尚未落盘。最终组合类型
检查待所有并行文件稳定后统一运行，当前不登记为通过。格式检查和差异检查通过，
不继承旧 CI。

本轮没有运行真实浏览器、API/Worker/PostgreSQL 集成、`make check`、完整构建、
依赖审计或 CI，没有发布、合并或部署。当前后端真实第二页、窄屏视觉、独立复审、
精确发布 head 与 main 兼容门禁仍需分别验证。AD-F-199、F52 全项和产品验收
0/209 状态不因本地展示补齐而改变。
