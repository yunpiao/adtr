# ADTR 开发约定

- 先读对应 Issue、README 与 `docs/delivery.md`；优先级不替代依赖。
- 架构入口：`docs/architecture.md`；任务契约：`docs/task-contract.md`。
- 程序入口 `cmd/adtr`；基础设施 HTTP/配置 `internal/runtime`；数据库 `internal/store`。
- 使用 Go 1.27.1。已验证命令：`make check`、`python3 scripts/test_integration.py`、
  `python3 scripts/test_lifecycle.py`。后两项需 Docker；缺环境必须报告失败/阻塞，不能跳过算通过。
- 云环境工具链、Go/buildx 可写缓存配置见 README，不使用同名的系统围棋程序。
- 先记录测试基线，再实现；覆盖正常、异常、权限边界与回归，单独审查实际 diff。
- 每个 PR 关联工程 Issue 或具体 AD-F，附命令、证据、最新 head CI、风险与未完成项；
  外部独立审查与自审分别记录。禁止删测试、放宽阈值或虚报验收。
- 仅开发分支与草稿 PR；不推 main、不合并、不部署生产、不配置真实凭据或扩大权限。
- 不连接真实 AD、不执行封禁/远程响应、不发送外部通知；只使用隔离的合成测试数据。
- 默认拒绝业务权限；保留证书校验。Actions 最小 job 权限，action 必须固定完整 SHA。
- 文档、代码、PR、CI、合并与产品验收分别记录；209 项范围不因 P2 或输入阻碍缩减。
