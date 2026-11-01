# #257 合成风险纵向验证记录

基线事件工作台 PR256，后续同步其诊断和合法测试角色修复、以及 #252 的结构化admission测试修复。
日期：2026-11-01。实际数据库/浏览器CI仍须独立确认。

本地Go全量race/vet通过；Python1217项契约通过；前端108文件4962项通过，TypeScript/Vite构建通过。
最后补入测试角色24字符校验及共享事件诊断后，18项专用workflow契约通过。
风险工作台28项DOM测试覆盖回执恢复、身份/域撤权、迟到响应与来源展示。
Go integration标签只完成编译，本地无Docker/数据库；不得记为PG执行通过。

独立复审修复：
- 实时读取在服务端时钟回退后曾可能忽略已提交reset。现在读取/重置共享边界拒绝，
  历史回执仍使用原时间和历史前缀重建，不受新reset影响。
- 恢复结果原先依赖已清空选择器显示来源。现在结果直接展示自身dataset/fixture/hash/契约。
- 复用事件测试时继承25字符角色ID；改为生产契约要求的24字符并新增回归，未放宽生产校验。

独立固定源码边界审计完成：295 default /306 CI源文件；连接只经dbadmission，
ledger/audit/receipt使用同一调用者事务，无init/后台生产者或worker启动变化。
仅刷新3个证书快照，checker/controller/guard/deadline/allowlist/CompleteWorkerClosure不变；
两tag边界检查及相关vet通过。它不是动态运行中取消证明。

专用risk工作流必须实际跑PG/TCP/浏览器，要求指定Go集成测试出现真实pass，不接受缺失匹配或skip。
事件专用workflow也继续运行，证明25升级后旧事件工作台仍有效。
模拟权重不是检测效力或真实AD验收，产品仍0/209。PR exacthead/main验证另记。
