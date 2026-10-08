# F52 系统信息、资源与依赖健康契约

范围：AD-F-196、199、200、201、206；路线图 #1 的 F52。此切片是本地 API 运行环境的真实采样、持久化查询、安装所有者授权和存储告警设置，不代表原系统 1:1 兼容或全部产品验收完成。

## 权威来源与作用域

- API 只注册 `local-api`。CPU、内存、启动时长及负载来自 Linux `/proc`，`scope=kernel_visible`：容器可见的内核统计可能覆盖宿主机，不能描述为容器配额或整个平台集群
- `runtime-root` 的受信任启动配置固定为 `/`；文件系统通过 `statfs` 采样，`scope=runtime_filesystem`。它是 API 进程看到的根文件系统，不能声称是 PostgreSQL 数据卷。没有远程节点发现、外部地址访问、端口扫描、shell 或子进程
- CPU 使用连续两次真实累计计数计算；首次为 `warming_up`、百分比 `null`。读取失败、计数回退/重置、时间回退、零增量、不可支持平台均保留明确原因，不补零、不沿用旧值
- 内存使用 `MemTotal-MemAvailable`；字节数为十进制字符串。文件系统 `used=(blocks-bfree)*bsize`、`free=bavail*bsize`、`reserved=(bfree-bavail)*bsize`；使用率为 `used/(used+free)*100`，保留空间单列
- API 采样周期 15 秒；超过 45 秒标记陈旧。PostgreSQL 是采样和设置的权威存储，重启不清空历史。请求不会即时触发采样，也不会把当前值重复制造成历史
- 未配置引擎、缓存、许可证、升级源、公司信息及 OS 发行版时如实返回 `not_configured`、`unsupported` 或 `null`。Go 版本、编译目标 OS 和实际 VCS 构建标记不是产品版本或引擎版本

## 身份、权限与事务

所有 `/api/system` 路由需持久化有效会话，密码状态允许访问，且 `session.tenant=default` 和 `system.readable=true`。`default` 是服务端安装所有者，HTTP 不接受 tenant、actor、role、owner。其他租户的平台管理员也被拒绝。新增 `system` 权限不会自动授予现存自定义角色；内置平台管理员只在安装所有者租户有效。

`POST /storage/settings` 另需 `system.writeable`、精确可信 Origin、会话 CSRF、新鲜密码与 TOTP。未知、重复、缺失、空值和类型错误在消耗证明前拒绝。成功设置、模块旧值/新值不可变审计、统一平台 `system_storage_alarm` 审计及 TOTP 消费处于同一事务；任一写入失败全部回滚。TOTP 成功后不得重放。设置使用 `expectedRevision` 比较交换，旧版本返回 `409 revision_conflict`，客户端重新读取后才能另行确认提交。

每个请求先建立 PostgreSQL 真实协议连接，再在同一事务获取共享迁移锁并核验精确 schema 7，之后才获取租户/用户锁。连接与 schema 门禁合计最多 2 秒；完整认证/操作请求上下文最多 10 秒。错误 schema、迁移锁等待超时或数据库故障失败关闭为 503；不复用缓存身份、不返回历史健康成功响应。权限、会话和所有者检查每次重做。错误仅包含稳定代码，不返回 DSN、SQL、密码、TOTP、内部路径或原始数据库错误。

请求元数据来自实际连接的 IP 和已匹配静态路由。忽略 `X-Forwarded-For` 等非可信身份/地址声明；审计不记录请求正文或查询串。模块审计禁止 UPDATE、DELETE、TRUNCATE；没有日志清理或自动删数据能力。

## HTTP 输入

所有响应 `Cache-Control: no-store`、`X-Content-Type-Options: nosniff`，JSON 错误为 `{"error":"stable_code"}`。路径和方法精确匹配，不接受尾斜杠、前缀扩展和 HEAD 替代 GET。方法错误为 405 并返回 Allow；未知路径 404。GET 不接受正文；所有查询拒绝未知键、重复键、空字符串和非法编码。客户端标识必须为 1–64 个小写字母/数字，可在首位以后包含 `-`、`_`，不接受 URL、地址或路径。

| 路由 | 方法 | 输入与默认值 |
| --- | --- | --- |
| `/api/system/info` | GET | 无查询 |
| `/api/system/nodes` | GET | 无查询 |
| `/api/system/resources/current` | GET | 必须 `instance=local-api` |
| `/api/system/resources/history` | GET | 必须 `instance,startTime,endTime,graphType`；时间为规范十进制 Unix 秒；`graphType=cpu_basic\|ram_basic\|disk_usage`；磁盘图必须 `storageId=runtime-root`，CPU/RAM 图禁止 storageId |
| `/api/system/storage` | GET | 必须 instance；page 默认 1、范围 1–100000；pageSize 默认 20，只能 10/20/30/40/50 |
| `/api/system/services` | GET | type 默认 all，只能 all/service/port/engine |
| `/api/system/health` | GET | 无查询 |
| `/api/system/storage/settings` | POST | JSON，最多 4096 字节；以下七项都必填 |

设置 JSON：`instance`、`storageId`、`setType=alarm`、整数 `percent=85..90`、非负且小于 int64 最大值的整数 `expectedRevision`、1–256 字节 `actorPassword`、6 位数字字符串 `totpCode`。拒绝未知键、重复键、null、字符串代替数字、小数代替整数、尾随 JSON 和查询参数。

`setType=log/autoClear` 及旧字段 `storageDataAlarmValue/storageLogValue/storageAutoClearValue/mount` 返回 `422 unsupported`；不会默默忽略，也不会把旧挂载路径当作服务器目标。非本实例/非注册 storageId 返回 404。其他格式错误 400；未登录 401；越权、CSRF、Origin、MFA 未配置或必须改密 403；错误密码/失效 TOTP 401；速率限制 429；冲突 409；不可用存储或不兼容 schema 503。审计内部失败可返回 500，设置仍回滚。

## 结果与原字段映射

类型定义以 `internal/auth/system_types.go`、`internal/systemhealth/types.go`、`persistence_types.go` 为实现契约。新增明确单位和可用性，不把原始字段未定义的空值语义推断为成功值。

| 原记录 | 当前结果语义 |
| --- | --- |
| FLD-3151–3156 | `/info.status` 保留 `memoryUsagePercent,cpuUsagePercent,diskUsagePercent,bootTime,LoadAverage`；新鲜且可用时取当前观察，其他为 null。bootTime 按原“开机使用时间”解释为 uptime 秒，不是启动时间戳；LoadAverage 为 one/five/fifteen |
| FLD-3157–3163 | `/info.version` 保留 engineVersion、majorVersion、两个时间戳、OSPlatform、OSVersion；未有可核实来源者 null。增加 goVersion、revision、modified；VCS 信息缺失时 null |
| FLD-3164–3168 | `/info.basic.systemName=ADTR`；ip、companyName、officialWebsite 为 null。接口不推断对外服务 IP |
| FLD-3169–3172 | `/info.upgrade` 两个版本为 null；systemCurrentTime 为数据库本次检查的 UTC 时间；capabilities 明确未配置/不支持状态 |
| UI-0635–0645 | 机器指纹、授权应用/期限、许可证上传和升级不是已实现能力；页面不展示虚构有效授权或可用上传控件 |
| FLD-3185–3191 | 旧设置字段与 log/autoClear 显式 unsupported；新设置只接受注册 storageId，保留 alarm、percent 85–90；增加 CAS 与身份验证字段 |
| FLD-3192 | 成功 `{result:1,setting:{instance,storageId,alarmPercent,revision,updatedAt}}`；只有提交成功才返回 1 |
| FLD-3193–3205 | `/storage` 返回 storage、total、page、pageSize、availability、reason、stale、observedAt、checkedAt。条目包含 id、mount、fs、percent、totalBytes、usedBytes、freeBytes、reservedBytes、alarmPercent、revision、alarmExceeded 与采样元数据。明确字节数替代原 total/free/used 的模糊字符串；percent 替代 usePercent；clearPercent 未实现，不伪造阈值 |
| FLD-3206–3209 | `/nodes.nodeList` 仅注册 local-api；hostName 仅在 os.Hostname 成功且有效时返回，ip 为 null；附 scope/availability |
| FLD-3210–3223 | 历史严格按秒范围、实例、图类型及存储 ID 查询实际持久化值；info[].mode/data.timestamp/data.value/dataStatistics 保留结构；补充 gaps、availableSince、sampleIntervalSeconds |
| FLD-3224–3226 | 当前资源为 `{instance,snapshot,availability,reason,stale,availableSince,checkedAt}`；snapshot.cpu.percent 和 snapshot.memory.percent 显式关联来源/时间，不使用没有可用性信息的裸 cpu/ram 字段 |
| FLD-3227–3228 | `/services` 接受原 type 枚举；结构化 dependencies 和稳定字符串 result 替代不可审计的自由文本诊断 |
| AD-F-206 | `/health` 汇总 API、已配置 PostgreSQL、真实采样与 Worker 活动；未配置缓存/引擎明确表示，不合成健康状态 |

原字段别名、单位转换和空值兼容需要源系统行为核验；本表是明确差异记录，不能计为已通过 1:1 源系统验收。

完整 `snapshot` 各指标带 `observedAt,source,scope,availability,reason`。当前接口可以保留陈旧快照用于解释历史，但 stale=true 且 availability 不可用；`/info.status` 方便字段会置 null，禁止把陈旧或未来时钟的值当“当前”。存储 alarmExceeded 只有新鲜可用指标才为 bool，否则 null。没有样本时仍可列出受信任注册的存储目标，容量/使用率未知，默认告警设置为 85、revision 0、updatedAt null。

历史范围为 `[startTime,endTime)`，必须 end>start、end 不晚于数据库当前时间、范围不超过 24 小时。最多 5760 个实际值；超出返回 `422 history_point_limit` 而非静默截断。按观察时间排序，百分比以十进制字符串输出，统计 max/avg/min/current 基于本查询实际值；无值时空数组、统计 null、完整范围 gap。缺失样本与采样失败不变成零，不插值。availableSince 是该实例/指标首次有效样本时间；部署前及中断区间不能被当前样本补齐。采样间隔 15 秒，中间及尾部间隙容忍 1 秒调度抖动；相邻有效值或查询结束与最后有效值相距超过 16 秒时，记录从应有下一次采样时刻起的 gap。查询起点距离第一个值超过 15 秒时也记录前置 gap。未实施删除/保留期清理或下采样。

## 服务与健康语义

返回 `{result,checkedAt,dependencies,worker}`。result 为 healthy/degraded，依赖 status 为 healthy/unhealthy/unknown/not_configured，并附 reason/source/scope/observedAt。

- `api`：处理本次已认证请求的进程，依据 serving_request；不等于所有副本健康
- `postgresql`：只检查受信任配置中的数据库，真实协议与精确 schema 门禁成功；type=port 表示此既有连接的协议检查，不做 TCP 扫描，不输出地址/连接串
- `sampler`：当前观察新鲜且 CPU/RAM/全部注册磁盘有实际可用百分比才 healthy；部分失败/CPU 预热为 partial_resource_observation，全部失败为 unavailable_resource_observation，不支持平台为 unsupported_platform，均使整体 degraded；从未观察为 unknown
- `worker`：队列/恢复周期每 5 秒的实际完成结果，以及执行器成功租约续期/检查点所产生的活动；独立定时心跳不能制造健康。超过 15 秒陈旧；至少同一 worker 的队列与恢复两条证据有效且没有当前失败才 healthy。未曾观察为 unknown，停止或失败为 unhealthy。附每条活动的时间、状态和证据类型，不能解释为 AD 任务完成证明
- `cache/engine`：not_configured，不展示虚构版本、探测结果或运行服务。未配置的可选依赖不独自使整体 degraded

`services?type=` 仅筛选固定依赖目录；all/health 显示全部，service 显示 API/采样/Worker/缓存，port 显示 PostgreSQL，engine 显示未配置引擎。筛选后的 result 根据所列依赖计算；完整 worker 证据附在响应中。

数据库不可用时无法核实身份，业务接口整体 503，不泄露依赖详细状态。已有 `/livez`、`/readyz` 仍是无敏感数据的基础设施探针，不由本业务 API 替代。

## 验证与未完成项

- `internal/auth/system_test.go`：路由/所有者权限矩阵，严格查询/JSON、边界和弃用输入、Origin/媒体类型、错误脱敏、缺失/陈旧/未来时钟、预热及不支持平台
- `internal/auth/system_integration_test.go`：独立真实 PostgreSQL 的会话/同安装所有者隔离、默认拒绝与撤权、实际采样持久化读取和历史间隙、存储分页、密码/TOTP/CSRF、并发 CAS、双审计原子回滚、重建 Store 后持久性、真实 Worker 活动、schema 先于身份锁、2 秒门禁及数据库不可用失败关闭
- 数据库测试必须设置 `ADTR_TEST_DATABASE_URL` 且可创建/销毁自身随机测试库；缺失数据库直接失败，禁止 skip。本机无隔离 PostgreSQL 时只可报告单元/race/vet 与 integration 编译；编译不是数据库验收
- 存储/采样本身的确定性和真实 Linux 检查在 systemhealth 包；API→浏览器链路、容器重启/依赖故障恢复和当前 head CI 应分别记录。未实际运行不得声称通过
- 原系统源码完整运行兼容、部署容量与保留期、生产最小权限数据库身份、多节点/多安装部署、Windows/AD 联动、授权/升级服务和外部引擎仍未验收。产品完整验收仍为 0/209；本功能不关闭前置门禁或 F52 全项

## 周期调度后的运行状态

Schema 8 起，运行中的 Worker 还必须有同进程的 scheduler 周期证据。
只有实际 Tick 完成后记录成功或失败；不以定时器写入制造健康状态。
队列、租约恢复与调度三个周期都必须新鲜，调度失败或超过15秒未观察
同样使 Worker 不健康。旧 schema 7 的组件测试只要求当时已有的两个周期。
