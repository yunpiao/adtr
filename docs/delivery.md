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
| 原始工作簿 | dot 云电脑已读取2026-10-03工作簿原文，SHA-256见 requirements/catalog.json | 文件可读阻碍已解除；逐业务字段冻结与源系统运行兼容核验仍按功能契约进行 |
| 209 编号映射 | 已建立65个规划Issue：57功能组、7门禁和1路线图；209项唯一映射已核验 | requirements/catalog.json 纳入CI检查209项、3258字段与804控件引用；实现验收不能由编号覆盖替代 |
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


## dot 云电脑执行与分层验证（2026-10-07）

切换后在 dot 云电脑重新物化远端源码；官方 Go 1.27.1 校验安装，本地无 Docker。
PR #6 TLS与同容器重启审查修复提交 af1ad3d2b766df4c1ef8a71b9ade58c8d05c6d7e，
本地 make check 及 GitHub CI 的真实 PostgreSQL/容器生命周期均通过。

PR #68 的 F43/F44 提交 c77878d2d090a32c776fc29126971709cd2b3afd：本地 Go race/vet/build、
20个DOM测试、TypeScript/build和依赖审计通过；独立审查后修复审计归属与TRUNCATE缺口。
GitHub CI https://github.com/yunpiao/adtr/actions/runs/37573272781 已通过真实数据库迁移、
认证状态/权限/并发回归、容器生命周期和真实浏览器→API→数据库认证链路。
逐AD-F已验证部分与未决门禁记录在 requirements/catalog.json；当前产品完整验收仍为0/209。
代码、真实链路测试、源系统1:1兼容、生产验收、合并和部署分别记录，不能互相替代。
