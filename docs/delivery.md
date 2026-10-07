# 交付台账与工作约定

总路线图：https://github.com/yunpiao/adtr/issues/1 。全部 209 项均在范围内。
工程文档、代码、PR、最新 head CI、合并、产品验收分别记录；任一阶段不能代表后续阶段完成。

## 基线（2026-10-07）

初始提交 c47bc370b31f2511201c43be2976d1d06e192ae7，仅 README。
没有源码、构建配置或测试，因此基线是「测试不存在」，不是「测试通过」。
云环境 Node v24.19.0/npm 11.9.0 可执行；Docker 28.4.0/Compose 2.40.3 服务可访问。
系统 `/usr/bin/go` 是同名围棋游戏，不能用作 Go 编译器；开发工具链必须独立校验安装。

## 未决输入（G01 保持未完成）

| 输入 | 已知事实 | 解除条件 |
| --- | --- | --- |
| 原始工作簿 | Library ID libfile_fd1e8820ccdc8191912c6818c3dea575；当前执行器缺少物化读取入口 | 在本执行器验证文件与原文；当前不填造字段矩阵 |
| 209 编号映射 | 父任务核验 17 表、209 项、51 条支持细节，计划 57 功能单元 | 接收完整编号/原文/验收/实际 Issue 映射并验证唯一覆盖 |
| 三套参考实现 | 当前未收到引用及具体 commit | 固定引用，逐项记录差异、采纳/拒绝理由与迁移风险 |
| 字段允许矩阵 | 类型/默认/空值/多值/运算符/时间单位/排序/页长/弃用均待原文 | 每字段可定位到来源和测试，不由 UI 猜测 |
| 容量 | 域、DC、对象、ACL、EPS、保留期、P95、导出量未知 | G01 冻结目标及测量口径 |
| 恢复目标 | RPO/RTO 未知 | G06 冻结并真实演练 |
| 规则 | 专项算法、内容与可判定夹具未收到 | G04 验证外部输入 |
| Windows/AD | 八版本实验机和授权范围未收到 | G05 明确实验资产、工具链和 72 场景 |

需求台账接收后的每行必须保存 AD-F 编号、源 sheet/行、原文、验收原文、唯一归属 Issue、
输入/结果/权限/失败/边界案例、自动/人工测试位置、PR、CI head SHA 和产品验收状态。
未读到的原文不以空白占位行宣称覆盖。优先级与前置依赖分别存储。

## 每次开发与完成标准

1. 读取 Issue、适用 AGENTS 和依赖；记录当前测试基线，先定跨模块接口。
2. 在开发分支实现一个可独立验收的切片；不得直接推 main、合并或生产部署。
3. 对实际变更测试正常、异常、权限边界和回归；不得删除测试、放宽阈值或将 skip 当通过。
4. 完成后单独审查实际 diff，检查凭据泄露、授权范围、竞争条件、失败语义及文档一致性。
5. 草稿 PR 关联 Issue，记录命令、结果、head SHA、风险与未完成项。部分工作用 Refs，不能提前 Closes。
6. 读取最新 head 的 CI 结果；CI 不可访问时明确阻塞。外部独立审查在可用时记录审查人和 commit，
   自审不能冒称独立审查。全部子项与产品验收未完成的 Issue 不关闭。

CI 默认无权限，各 job 仅授予必要 contents: read；actions 固定完整 commit SHA，
不向 PR 执行暴露生产凭据，不使用 pull_request_target 执行不可信分支代码。
运行命令只在实际执行成功后写入根 AGENTS，不能填占位命令。

参考规范：
- https://learn.chatgpt.com/guides/best-practices
- https://docs.github.com/en/copilot/tutorials/cloud-agent/get-the-best-results
- https://learn.chatgpt.com/docs/agent-configuration/agents-md
- https://docs.github.com/en/actions/reference/security/secure-use
