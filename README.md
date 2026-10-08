# ADTR

面向 Active Directory 的安全产品，全部 209 项需求在
[路线图 #1](https://github.com/yunpiao/adtr/issues/1) 范围内。
当前已有 G01/G02 设计、G03 工程基础设施，以及 F43/F44 本地账户认证、F45/F46 用户角色功能权限、F47 本地资源授权管理、F48 持久化任务框架、F39 平台审计、F52 本地系统监控及 F49 健康任务周期计划/可恢复归档实现。
认证包括持久化登录/退出、首次/过期密码修改、管理员同租户重置、TOTP MFA 和实际浏览器界面。
访问管理包括同租户用户/角色维护、单个/批量角色分配、显式功能权限、服务器菜单与操作检查；
所有权限修改需要密码与新鲜 MFA，角色变化撤销受影响会话。
资源组、角色关联和租户配置持久化；AD域范围必须显式授权，平台管理员也没有默认数据绕过。
初始目录无业务数据，尚未完成真实AD接入验收；测试只使用明确标记的合成域和目录对象。
集成检查点 `4f64d90880b28b3cf7a749a9ba1da57049736dd9` 的 [25 个 CI job](https://github.com/yunpiao/adtr/actions/runs/37772558021) 已全部通过，包含真实 PostgreSQL、API/Worker 与 19 个浏览器套件；具体切片、独立审查及剩余范围见 [交付台账](docs/delivery.md)。产品验收计数仍为 **0/209**。
任务 Worker 注册基础设施健康、审计XLSX导出、运行日志打包、域诊断及目录读取执行器；具备租约、隔离、进度和按类型限制的重试、取消与恢复机制。
审计支持持久化查询、筛选、可恢复隐藏/还原，以及受当前权限保护的异步导出；历史缺失元数据明确为空。
F01 域连接正在实现：加密配置、明确资源授权、单次诊断任务和严格 TLS/出口策略；安全传输与凭据策略的合成测试及独立审查已通过。隔离合成 LDAP 目标下的数据库及真实浏览器链路已在上述 CI 通过；真实 AD 仍待验收。契约见 [docs/domain-contract.md](docs/domain-contract.md)。

F39/F52/F49 的真实 PostgreSQL/浏览器切片已在上述 CI 通过，Linux 采样及 Worker 活动也经过实际执行；这不覆盖完整源系统兼容或生产验收。
系统监控每15秒记录真实内核可见CPU/RAM与API文件系统数据；Worker健康来自实际队列/租约活动，未观察、过期或失败会明确显示。
存储阈值85–90支持新鲜MFA、版本冲突检查和持久审计；无自动物理清理或外部告警。
周期计划只调度真实基础设施健康检查，创建后暂停，按UTC固定间隔启用；停机合并到最近一期，当前一期未结束则跳过重叠。
任务归档仅改变可见性，可还原，不清除审计、幂等或消息记录，也不声称释放磁盘空间。
这些工程验证不能替代产品验收。
已有默认关闭的有界 LDAP 目录读取代码；增量采集、检测、AD业务导出和阻断执行器仍未交付，未进行生产部署。

F12 当前切片包含独立目录凭据用途、逐页授权、受执行结束证明保护的账本、完整观测暂存、当前域权限下的稳定分页与界面。迁移15原子安装新约束，并在未确认旧凭据使用已停止时拒绝升级。`ADTR_DIRECTORY_READ_ENABLED` 默认关闭，启用仍需独立密钥、CA、出口策略和当前角色显式 `domain.directory_read` 授权。连接检测授权不会自动变成目录授权。当前仅覆盖 GUID/DN/类别/对象类/可选 SAM/UAC 字段；没有增量、缺席删除推断、完整字段兼容或真实 AD 验收。契约见 [directory-api-contract.md](docs/directory-api-contract.md)，证据与剩余门禁见 [delivery.md](docs/delivery.md)。

## 本地构建与测试

需要 Go **1.27.1**、Node **24.19.0**、npm、make；集成测试另需 Python 3、Docker Engine 与 Compose v2。
Go 依赖由 `go.mod`/`go.sum` 固定，容器基础镜像固定 digest。

```sh
make check
(cd web && npx playwright install --with-deps chromium)
python3 scripts/test_integration.py
python3 scripts/test_auth_e2e.py
python3 scripts/test_auth_e2e.py --suite tasks
python3 scripts/test_auth_e2e.py --suite audit
python3 scripts/test_auth_e2e.py --suite system
python3 scripts/test_auth_e2e.py --suite maintenance
python3 scripts/test_auth_e2e.py --suite directory
python3 scripts/test_auth_e2e.py --suite directory-controls
python3 scripts/test_auth_e2e.py --suite directory-readers
python3 scripts/test_lifecycle.py
```

`make check` 执行格式检查、go vet、带 race 的单元/HTTP 契约测试、编译、前端锁文件安装、类型检查、DOM测试、构建和依赖高危审计。
数据库脚本创建随机命名的全新 PostgreSQL，测试迁移前拒绝就绪、并发迁移、重复迁移及不兼容版本，
退出时只删除自己创建的容器。生命周期脚本验证 API/Worker 启停、依赖故障、恢复和重启，
并只清理自己随机命名的 Compose 项目与卷。没有测试环境会失败，不会跳过算通过。
所有测试只使用合成基础设施数据，不连接 AD 或发送外部通知。

云工作区可显式使用已校验安装的工具链；先把 `ADTR_DEV_ENV_SCRIPT` 设为当前激活脚本的实际位置，避免依赖临时挂载路径：

```sh
source "${ADTR_DEV_ENV_SCRIPT:?set the verified dev-env.sh path}"
go version
```

普通开发机安装官方 Go 并使用正常 PATH 即可。
构建镜像前由宿主 Go 下载、校验并生成忽略提交的 vendor 目录，容器内离线编译。
这样无需把宿主环境的证书或认证配置加入镜像，也不会关闭下载的 TLS 校验。

## 启动开发环境

```sh
export ADTR_DEV_PASSWORD="$(python3 -c 'import secrets; print(secrets.token_hex(24))')"
make prepare-image
docker compose up --build --detach --wait
./bin/adtr -probe http://127.0.0.1:8080/readyz
./bin/adtr -probe http://127.0.0.1:8081/readyz
docker compose stop
docker compose start --wait
```

先运行 `make check` 生成探针客户端。同一开发数据库卷重启时保留同一个 `ADTR_DEV_PASSWORD`；
新的随机密码仅用于新的开发卷。密码不得提交、输出到日志或复用为真实 AD 凭据。
`docker compose down` 删除开发容器并保留数据库卷；需要重置数据时，
确认该项目只含可丢弃数据后再手动删除其卷。

容器迁移命令先于 API/Worker 执行。默认端口只绑定本机回环地址，数据库无宿主端口。
`/livez` 表示进程存活；`/readyz` 仅在数据库可访问且 schema version 匹配时返回 200，否则返回 503。
Worker ready 仅表示工程基础设施可用，不代表业务执行器已实现。
未配置认证时业务 URL 返回 404。认证启用、引导和完整安全契约见 [身份认证契约](docs/identity-contract.md)。探针不输出数据库地址、凭据或错误细节。

独立进程支持 `bin/adtr -mode api|worker|migrate|bootstrap`，必须通过环境注入 `ADTR_DATABASE_URL`。
`ADTR_LISTEN_ADDR` 默认分别为 `127.0.0.1:8080`/`127.0.0.1:8081`。
数据库 URL 必须指定 `sslmode=verify-full`；仅 `ADTR_DEVELOPMENT=true` 时允许显式 `sslmode=disable`。
启动时验证 pgx 实际主连接及全部 fallback 的证书校验和主机名；连接复用已验证配置，
不在探针或迁移时重新读取环境配置。重复查询参数、`ssl` 别名和 URL 中的 host/service 覆盖均拒绝。
生产连接禁止 Unix socket，避免绕过 TLS。
Compose 使用此例外连接隔离的本地测试库；其共享开发数据库身份不代表生产最小权限方案。
生产认证验收、分离迁移/运行角色、数据库 TLS 与发布验收仍在后续门禁范围内。

## 设计和验收边界

- [架构 ADR、安全与兼容性](docs/architecture.md)
- [任务与数据契约](docs/task-contract.md)
- [持久化任务实现与状态语义](docs/tasks-contract.md)
- [任务实时授权与撤销边界](docs/task-authorization.md)
- [审计查询、恢复与导出契约](docs/audit-contract.md)
- [系统资源与依赖监控契约](docs/system-health-contract.md)
- [周期计划契约](docs/schedules-contract.md)
- [可恢复任务归档契约](docs/task-archive-contract.md)
- [需求台账、阻碍和交付约定](docs/delivery.md)
- [资源、租户与域范围授权契约](docs/resource-contract.md)
- [用户、角色与功能权限契约](docs/access-contract.md)
- [209项完整需求与分层验收台账](requirements/README.md)
- [本地身份认证安全契约](docs/identity-contract.md)
- [当前工程验证记录](docs/validation-g03.md)

工作簿字段冻结、209 项唯一映射、三套参考实现差异、真实 AD/Windows 八版本实验、
规则输入、容量和 RPO/RTO 均待核验。mock、编译、容器就绪或 CI 通过不能替代产品验收。


F03 已实现本地操作账户凭据登记、受域范围约束的查询/修改/移除与加密存储；这些动作不创建、验证或修改远端 AD 账户。显式凭据使用授权治理已在本地 B1 接入；B2 已接入 F01 的显式账户引用与固定连接测试；F05 安装等消费者仍未实现。契约见 [docs/operation-accounts-contract.md](docs/operation-accounts-contract.md)；真实 PostgreSQL/浏览器链路及源系统兼容验收仍待对应证据。

F42 已在本地接入个人资料查看与本人头像替换，包含迁移 11、私有图片读取、跨标签身份校验和上传结果恢复。浏览器测试已编写；集成版本的真实 PostgreSQL/浏览器执行与截图检查仍待 CI，不能据此认定产品验收完成。契约见 [docs/profile-contract.md](docs/profile-contract.md)。

F02 第一阶段提供已授权、已保存域连接的数据源选择，选择后重新核验配置版本并进入现有详情/检测流程。历史检测通过不表示当前在线、已采集或获得产品授权；真实 DC/Agent 清单仍需后续采集链路。接口和范围见 [docs/domain-selection-contract.md](docs/domain-selection-contract.md)。

F03 B1 将凭据使用授权与元数据管理权分开，所有角色（包括内置管理员）默认无使用授权。B2 仅启用固定连接测试消费者，仍须每次验证显式授权、当前绑定和执行栅栏；过期或超额租户保留按原有域范围撤销授权的入口。迁移、SQL治理屏障和真实链路验证要求见 [docs/credential-use-contract.md](docs/credential-use-contract.md) 与 [docs/account-reference-contract.md](docs/account-reference-contract.md)。进程崩溃后尚未确认停止的凭据使用会保留依赖，阻止更换或移除账户；任务终态不能代替执行结束证明。

F38 增加当前账户的审计导出历史：离开页面后可重新发现实际持久化任务，按状态/创建时间分页查看并使用现有受保护 XLSX 下载。历史列表先校验当前权限、提交时权限版本及快照域范围，再计算总数；文件状态“可请求下载”表示元数据匹配，实际字节、摘要和实时权限仍在下载时核验。当前只有 Audit/XLSX 生产者，其他业务导出和 appType 未实现，见 [docs/export-history-contract.md](docs/export-history-contract.md)。

F56/AD-F-205 本地切片记录本应用从部署后实际产生的 api/worker 运行事件，可按确认的时间范围查询并生成受保护 ZIP/JSONL 诊断包。它不采集历史 stdout、不读取任意路径，也不生成不存在的服务日志；失败、队列拒绝和不确定确认分别记录，覆盖范围始终是尽力记录。默认安装租户还须具有 system 与 system_logs 权限。迁移14遇到尚未确认停止的账户凭据使用时会拒绝升级，必须让旧版本执行器完成真实确认后再重试。详见 [docs/operational-log-contract.md](docs/operational-log-contract.md)。网络诊断 AD-F-204 仍待目标、协议、端口与授权契约。

浏览器会话隔离已在本地增强：另一标签的登录/退出会触发真实会话重核验，通知不可用时由重新聚焦/可见性补核验；旧私有响应不能恢复旧身份。非敏感未知结果记录按actor保留，避免会话轮换丢失原幂等键。真实跨标签运行证据仍待CI，见 [docs/session-invalidation-contract.md](docs/session-invalidation-contract.md)。
