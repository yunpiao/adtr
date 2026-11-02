# SIMULATED 检测例外独立验收

关联 Issue #43（AD-F-103–110）及既有离线核心 #253。仅为同租户/同域合成工程切片；不代表真实 AD/Windows、正则兼容、跨域全局、自动抑制、风险或告警级联。产品验收保持 0/209。

## 入口与实际执行要求

构建 API 与前端、安装 Playwright Chromium 后执行：

```
python3 -m unittest discover -s scripts -p test_simulation_detection_exceptions_workflow_contract.py
python3 scripts/test_simulation_detection_exceptions_integration.py
```

第二条需要实际 Docker/PostgreSQL/浏览器。缺环境写入 not-run 并非零退出，静态契约、Go 编译或用例发现均不算真实运行通过。专用工作流 checkout 精确 PR head，官方 Action 固定完整 SHA，PostgreSQL 使用既有 digest；不扩大原 engineering-ci 选择器。

## 真实链路断言

- 独占随机名/所有者标签的 loopback PostgreSQL，迁移30重复执行。通过实际 API 登录、显式权限和域授权；管理员不自动获得业务访问。
- `SeedFixtureTx` 固定两条标记 SIMULATED 的事件：exception-positive 为失败登录，exception-negative 为服务访问。实际 API 创建模板并提交，真实 Worker 必须生成正例一条/反例零条、attempt1、两事件及准确模板摘要；禁止手写成功任务行。
- 源结果未成功时评估拒绝。服务端预览绑定 task/event/rule 后显式创建禁用声明，原始匹配字段逐项保持。启用添加精准注释、停用/归档后新评估无注释，旧评估固定修订和结果不变。
- 修改实际模板生成新执行摘要，旧规则例外不得继承；同域全局例外可以解释新摘要，但不授予跨域读取。
- 导入只收禁用声明数组，任一不支持字段使整批失败且配置不变；证据 JSON 导出包含事实快照，不伪装为可原样导入的历史/启用配置。
- 原操作重放、API/Worker 重启与浏览器重载保留证据。撤回手动检测读写授权后，历史评估和源回执不可读；重新授予不复活旧 epoch 任务。
- 精确枚举八个具名 PostgreSQL 测试：后端4、HTTP权限1、schema30升级/失败回滚/并发3。真实执行缺 pass、任何 skip 或非零退出均失败，静态测试同时核对源码完整清单。
- 浏览器实际请求提交一次但丢失响应，从原回执恢复；实际文件上传声明、点击导入并下载/解析 JSON 字节；保留原任务结果与归档前评估。

## 图片、隐私与诊断

只上传 result.json 和主动截取的 SIMULATED 结果区域 PNG。关闭 trace、video、自动失败截图；不上传原始服务器日志、会话、连接串、口令或整页身份内容。
结果截图先自然滚动，检查结果框位于 sticky 导航下方且完整落在真实 viewport 内，再截取小型业务结果框。不注入隐藏导航 CSS，不以长元素截图中的遮挡推断生产布局缺陷。
命名 Go 测试失败只输出允许的测试文件名/行号、SQLSTATE 和整数退出码。API/Worker 使用不同显式 loopback 端口，沿用原等待与清理预算。

## 当前本地状态

源码及实际 API/DTO/界面选择器已接线；独立静态契约8项、严格 TypeScript、Playwright discovery1项通过。真实 PostgreSQL/API/Worker/browser 运行仍待精确提交的专用 CI，不能以本地源码或 mock 结果替代。独立复审、精确 head 全 CI、依赖与 main 兼容、合并后验证分别记录。

## 2026-11-02 候选复审与本地证据

独立复审修正并复核两项问题：回执 GET 不存在后，原操作重试收到明确拒绝时应解锁
页面重新读取/编辑，未知传输结果仍保留 pending；专用 workflow 的 artifact 名称由
误复制的规则目录名称改为 detection-exceptions，并加入精确名称防回归断言。
来源结果的 owner/epoch、模板/映射/实际命中、当前来源权限及不可变历史经过独立复核。

Go 完整 race 与 vet、209 条需求完整性和 gofmt/diff 检查通过。独立聚焦 backend/auth
race、UI 25 项和专用契约 8 项通过。源码边界 default356/CI367/wiring74 通过独立审计，
只更新三份摘要证书，没有放宽旧任务保护、预算、允许列表或 CI 选择器。
本地 PostgreSQL 实际尝试因缺隔离数据库/Docker 失败或 not-run，失败记录保留，
不以编译通过替代真实数据库测试。完整最终前端/Python回归及远端验证状态另行记录。

最终复审后候选：Python 完整仓库契约 1280 项通过（144.914 秒）；前端完整
114 文件/5142 项、typecheck、build 通过。P2 修复前 5140 项结果和修复后结果分开保留；
此处报告最终源代码结果。早一轮 Python1280 也通过，但最终轮重新覆盖 artifact
命名防回归修改。全部结果仍是本地工程验证，远端精确提交 CI 尚待发布后执行。
