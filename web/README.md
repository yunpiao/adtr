# ADTR 账户安全界面

关联：Issue #7 / F43（AD-F-167、168、173），Issue #8 / F44（AD-F-169）。
本切片实现账户认证界面，不显示占位的 AD 业务成功结果，不代表全部需求或产品验收完成。

## 开发与验证

Node 24、npm 11；依赖精确固定于 package.json 和 package-lock.json。

```sh
npm ci --prefix web --ignore-scripts --cache /tmp/adtr-npm-cache
npm run typecheck --prefix web
npm test --prefix web
npm run build --prefix web
npm run dev --prefix web
```

Vite 只绑定 127.0.0.1，将 `/api` 代理到 `http://127.0.0.1:8080`，保留浏览器 Origin。
生产构建输出 `web/dist`；API 启动时显式配置 `ADTR_WEB_DIR` 后才提供该目录。
不要将 Vite 开发服务器用于生产或开放给不可信网络。

## 已实现交互

- 登录、正确密码后的 MFA 挑战、服务器确认后退出
- 首次登录 / 密码过期强制修改；完成之前只允许修改密码或退出
- 账户概览：来源于 `/api/auth/me` 的真实字段，未提供值明确展示
- 本人修改密码与平台管理员凭密码、TOTP 重置指定账户密码
- MFA 设置密钥、手动添加认证器、验证码确认、停用及会话轮换
- 忙碌状态和重复提交防护；取消、Back/Forward、失效响应丢弃及状态重新读取
- 明确的权限、失效、限流、输入、网络及服务器错误；不将网络失败视为确定失败或自动重试写操作

## 安全契约

HttpOnly / SameSite=Strict 会话 Cookie 由服务器设置。前端不读取或保存 session token；
`csrfToken` 仅保留在内存，所有认证后的 POST 发送 `X-CSRF-Token`。
请求使用 `credentials: same-origin` 和 `cache: no-store`。没有 localStorage / sessionStorage 凭据。
取消或切换页面销毁表单和 MFA 密钥，不将密钥写入 URL / 日志 / 剪贴板。
取消中断等待不能撤销已提交的服务器修改，界面会重新读取当前账户；不自动重试不确定结果。
后端是权限与会话状态的最终依据；隐藏按钮不代替服务器授权。

冻结实现与参考字段的有意差异：原参考 token 字段改为 HttpOnly Cookie；密码统一 12–64 Unicode
码点；用户名为 1–64 ASCII 字母、数字、`.`、`_`、`-` 并规范为小写；TOTP 严格六位 ASCII 数字。
本人改密与管理员重置分开，管理员重置要求新鲜密码和 MFA。界面不提供未授权用户枚举。

## 测试边界

`src/App.test.tsx` 是 mocked fetch 的 DOM / 单元 / 交互契约测试，覆盖正常、错误、权限提示、
Unicode 边界、重复、取消、过期、Back 和过时响应。它不证明 PostgreSQL 持久化、真实 Cookie、
TOTP、审计或浏览器到实际 API 的端到端效果；这些须由后端和真实集成层独立提供证据。
真实 AD / Windows 与生产发布均不在本前端测试的声明范围。

## 实际浏览器集成测试

`npm run test:e2e --prefix web` 独立运行 Playwright，不属于 mocked DOM 单元测试。
必须设置 `ADTR_E2E_USERNAME`、`ADTR_E2E_PASSWORD`，指向可丢弃的隔离数据库 bootstrap 账户。
`ADTR_E2E_BASE_URL` 默认 `http://127.0.0.1:18080`，可用
`ADTR_E2E_CHROMIUM_PATH` 指定云环境已安装的 Chromium；否则先安装 Playwright Chromium。
该测试真实修改合成账户密码为 `Changed E2E Password 123` 并启用 MFA，因此不能对真实账户运行。
仓库级 `scripts/test_auth_e2e.py` 负责隔离数据库、API 和清理，执行前必须构建 API 与本前端。

构建说明：锁定的 Rollup 4.64.0 在当前云执行器上对 React 图进行 tree-shaking 用时超过 4 分钟。
当前显式保留全部代码（`treeshake: false`），仍执行生产压缩；验证产物约 199 kB JavaScript、
64 kB gzip。未裁剪功能或放宽测试。该性能优化可在上游工具链问题解决后重新评估。

## 用户、角色与功能权限（F45/F46）

关联 Issue #9 / AD-F-171、172、174–176 和 Issue #10 / AD-F-177、178。
冻结浏览器接口及有意不支持的语义见 `docs/access-contract.md`。

- 点击「访问管理」后才读取 `/api/access/menu` 与 `/check`；两个服务器结果共同控制功能入口与操作按钮，后端逐次执行最终授权
- 用户列表支持全部冻结筛选：用户名、当前本人、多角色 ID、MFA 开启/关闭、密码强度、四个时间边界、分配角色、创建/密码时间排序、分页及最多 1,000 条的全部模式
- 用户完整资料、不可变用户名、新建密码确认、账户停用、删除、单个和最多 100 人原子角色分配；无分配权限时，创建默认 viewer、编辑保留角色
- 角色分页、名称存在检查、详情、创建/备注编辑/删除；内置角色权限不可修改，已分配角色不可删除
- 显式功能权限编辑只提交 mark/auth；写入同时选择读取，服务器元数据仅展示。可选授权受当前 menu.auth 限制，allow_auth 仅为注册表能力
- 每次写入需要当前操作者密码和新的未使用 TOTP，初始用户密码独立；成功后重新读取持久化数据，sessionRevoked 或登录失效时重新读取 `/api/auth/me`
- 支持重复提交锁、加载、取消、Back/Forward、过时响应丢弃和不确定结果提示。取消不能撤销已提交写入，不自动重试
- 不提供头像上传、独立 MFA disable 策略或数据源范围编辑；空 dataSrc 不代表 AD 资源隔离

`src/Access.test.tsx` 是 mocked DOM/交互与请求契约测试，不证明真实数据库、审计或浏览器授权。
`e2e/access.spec.ts` 使用全新隔离 bootstrap 数据库，真实执行首次改密、MFA、角色/完整用户创建、
管理员重置目标密码和目标会话撤销、目标强制改密、角色持久化、菜单/操作检查及带有效 MFA 的越权拒绝。
该用例最多等待几个真实 30 秒 TOTP 窗口，超时 300 秒，不跳过重放保护。
`auth.spec.ts` 另含 MFA 停用；`ADTR_E2E_EXPIRED=1` 时额外断言初始密码已过期。
必须分别给 auth 与 access 场景提供全新数据库，不可直接把两个 spec 对同一 bootstrap 数据连续运行。
测试发现/类型检查不等于真实浏览器执行通过；实际证据由隔离 CI 单独记录。

## 资源组、租户与数据隔离（F47）

关联 Issue #11 / AD-F-179–183；精确 API 及本地语义见 `docs/resource-contract.md`。
这是持久化授权范围界面，不是 AD 域接入、目录查询或全部 209 项验收。

- 点击「资源与租户」后才请求资源能力；`/api/access/check` 的精确路径结果控制按钮，资源组操作同时核对当前 `roles` 菜单读写权限。租户配置只使用服务器的 builtin-admin 能力结果
- 资源组名称字面筛选、布尔创建时间排序、分页/最多 1,000 条全部模式、详情、名称存在检查、名称/备注/完整域成员创建及编辑、删除。名称按 UTF-8 256 字节限制，备注按 500 Unicode 字符限制
- 关联角色列表和分页；修改前读取完整集合并拒绝截断结果。显式选择内置 `platform_admin` / `viewer`，自定义角色须为 F45 的 24 位不透明 ID；完整替换且允许清空
- `maxAdCount`、UTC Unix 秒 `expireTime`、`uid`、`name` 的无虚构默认配置编辑。上限 0 和到期时间 0 都有实际停用语义，没有永久有效选项；UID 不是验证后的客户身份或租户选择器
- 当前会话获授权域和逐项检查结果；每行多个域执行 AND。空范围就是无授权域，平台管理员也没有自动 AD 数据权。未知/跨租户/停用/无授权域统一返回不允许
- 应用只使用本地字符串 `ad`，只支持资源类型 2；不把原参考数字应用、类型 0/1 或空检查解释成应用级/全域授权
- 所有修改均使用当前操作者密码、新的未使用 TOTP、当前 Cookie/Origin/CSRF。按服务器 `sessionRevoked` 返回重新检查账户；配置保存撤销全租户会话。无自动写重试
- 重复提交锁在解析 disabled 表单之前生效；取消/切换/Back/Forward 丢弃过时响应并清理表单；离开资源工作区重新读取账户状态。取消不能撤销已经提交的服务器事务

当前 API 没有域目录查询或写入接口，因此域字段接受已有活动域的明确 ID，不提供虚构域选项。未知域由服务器拒绝。生产目录最初为空；正向域场景只来自隔离数据库的明确合成夹具，不代表真实 AD 接入。

`src/Resources.test.tsx` 为 mocked DOM、请求及验证契约测试，不能证明真实 PostgreSQL、浏览器 Cookie、审计事务或 AD 行为。
`e2e/resource.spec.ts` 只对新的可丢弃 bootstrap 数据库运行，不能复用 auth/access 已修改过的账户。
父级隔离 runner 必须先迁移全部 schema，并在当前 `default` 租户插入活动的 `synthetic-domain-a` 合成目录夹具；不预配租户或资源组授权。
然后仅运行 `npm run test:e2e --prefix web -- resource.spec.ts`，传入现有 `ADTR_E2E_USERNAME`、`ADTR_E2E_PASSWORD`、`ADTR_E2E_BASE_URL`。
用例真实完成改密/MFA、租户保存和重新登录、无管理员自动授权、空组持久化、成员更新、显式管理员关联、正反检查、关联撤销/删除及最终无授权。等待最多八个真实 30 秒 TOTP 窗口，超时 300 秒；未放宽重放保护。
测试发现与类型检查不是浏览器通过；最终真实集成证据必须由独立隔离 CI 记录。真实 AD/Windows、源数字枚举一致性、外部 UID/许可语义和容量测量仍未验收。

## 后台任务（F48）

关联 Issue #12 / AD-F-189、191、192。当前唯一可提交种类为
`infrastructure.health` v1，范围 `platform`，空 payload；实际 Worker 检查数据库与队列。
此切片没有 AD 检测/导出/远程响应/通知执行器，不代表跨域 AD 业务或完整产品验收。

- 独立「后台任务」工作区通过服务器 `tasks` 菜单及六条精确 `/api/tasks` 操作检查展示功能
- 列表支持域、种类、状态筛选与 1–100 条分页；详情展示权威状态、来源状态、UTC 时间、实际尝试、持久化进度、结果版本、结果、游标及事件
- 非终态持续轮询，终态停止；`retry_wait` 不是成功，`cancel_requested` 不是取消完成；`completed-with-cancel-race` 事件展示实际竞争终态
- 提交、取消、恢复要求密码、新鲜未使用 TOTP 和当前 Cookie/Origin/CSRF。重复点击被锁定；没有自动写重试
- 未确认的提交/恢复保留同一幂等键。当前标签页的 sessionStorage 只存非秘密的操作键、目标任务 ID 与账户标识，用于刷新后继续核对；不存密码、OTP、CSRF/session token 或 payload。服务器确认、退出登录/401、账户或内存会话变化时清除。禁用存储时仍以内存保留导航间的键
- 取消等待、切换页面、Back/Forward 均丢弃迟到响应，重新读取真实状态；停止等待不能撤销服务器事务。退出登录/关闭标签页后先核对服务器任务列表
- 失败或死信恢复创建带父任务 ID 的新任务；不修改旧历史。部分失败不提供整体重放。作用域撤权/不存在详情均清除旧数据，不暴露历史结果
- 权限编辑目录新增 `tasks` 并保留服务器返回的所有已有 grants；管理标签仍只有用户、角色、功能权限，不能把任务标记当成 `/api/access/tasks` 路由

`src/Tasks.test.tsx` 是 mocked fetch 的 DOM/契约测试，包括重复/迟到响应、网络不确定重放、刷新恢复、取消竞争、终态停止轮询、分页、撤权清理、恢复父子任务、目录授权保留。
它不能证明真实数据库或 Worker 行为。

`e2e/tasks.spec.ts` 必须使用独立的新 bootstrap 数据库、真实 API 与真实 Worker，
不复用 auth/access/resource 场景的数据库。执行真实改密、MFA、健康任务及结果事件、
新 TOTP 同键重放返回相同 ID、持久化任务只读角色/账户，以及正确密码/MFA下的真实越权拒绝。
仅运行 `npm run test:e2e --prefix web -- tasks.spec.ts`；沿用 `ADTR_E2E_USERNAME`、
`ADTR_E2E_PASSWORD`、`ADTR_E2E_BASE_URL`，最长 300 秒并等待真实 TOTP 窗口。
没有响应拦截、模拟 Worker、时间冻结或直接 SQL 成功结果夹具。测试发现/类型检查不等于端到端通过；本机缺少隔离 PostgreSQL/浏览器环境时，真实运行证据须由 CI 另行记录。

## 操作审计（F39）

关联 Issue #13 / AD-F-153–156。界面使用 `/api/audit` 的真实查询和持久化可见性操作，
并通过 `audit.export` v1 任务请求实际 XLSX。来源是已有认证、访问/资源和任务审计及新的审计控制记录。
记录 ID 由 `auth.` / `resource.` / `task.` / `audit.` 加原始序号组成；历史缺失的用户、IP、
请求路径、请求标识和结果元数据明确标记缺失，不从当前账户资料补写。

- 独立工作区读取 `audit` 菜单与逐路径服务器权限；创建导出同时需要 `audit_exports.writeable` 和 `tasks.writeable`，权限编辑器保存/读回 `audit` 与 `audit_exports` 的显式读写授权
- UTC 时间起点包含、终点不包含；关键词按字面匹配登录用户/IP；事件与九类类型多选使用重复 URL 参数。支持正/倒序、可见/隐藏/全部、分页和最多 1,000 条的全部模式
- 隐藏与恢复最多选择 100 条，要求原因、操作者当前密码和新鲜 TOTP；原记录不删除。`audit.*` 控制记录受保护，不可选；混合可见性选择不能批量执行
- 导出只支持当前已应用的「未隐藏记录」筛选。隐藏/全部视图明确禁用导出，不会悄悄改变筛选。精确八列由 `/columns` 返回，选择 1–8 列，禁止空选择、未知或重复列；上限 100,000 行、10,000 行分批。未支持来源不明的第九列
- 提交后显示服务器任务状态、持久化进度、尝试次数、快照时间与实际行数，非终态持续轮询。零行成功显示「仅表头」，不会把空选择或失败任务当成导出成功
- 下载通过带当前会话的同源 fetch；不使用服务器返回路径跳转。只接受 XLSX MIME 与受限文件大小，失败状态不创建下载。记录隐藏/恢复使旧快照失效，需重新创建导出。可通过任务 ID 重新读取进度
- 网络/响应不确定时保留同一导出筛选、列与幂等键；取消、Back/Forward、重新打开均不会自动重试写入。标签页 sessionStorage 只保存账户标识及上述非秘密请求字段；不保存密码、OTP、Cookie、CSRF/session token、审计行或文件内容。确认、退出登录/401或内存会话改变后清除
- 每次发送审计写入后清空证明字段；重复提交锁、AbortSignal 与请求代次共同防止迟到响应重开旧表单或冒称成功。取消只停止等待，不撤销服务器已经执行的事务
- 通用任务页仍可取消自己有权操作的导出任务，文件结果和游标按服务器要求清空；禁止从通用页面恢复导出，必须从操作审计页面重新创建。收到旧的导出恢复意图时会清除，避免反复提交到不支持的路由

`src/Audit.test.tsx` 使用模拟传输检验 DOM、API 格式、权限、缺失字段、筛选、可见性、
不确定重放、取消/Back、异步状态和下载边界，不代表实际数据库或 Worker 已通过。

`e2e/audit.spec.ts` 必须使用独立新的合成 bootstrap 数据库及真实 API/Worker，不能复用其他套件账户。
使用 `python3 scripts/test_auth_e2e.py --suite audit`（仓库根目录）编排，或对已启动的隔离环境运行
`npm run test:e2e --prefix web -- audit.spec.ts`。用例等待真实 TOTP 窗口，最长 600 秒；
验证实际历史字段、筛选/权限拒绝、隐藏/恢复、受保护控制记录、零/单/八列边界及快照失效，
并读取浏览器下载的 XLSX ZIP/XML 结构和实际单元格。没有响应拦截、假 Worker、冻结时间
或直接 SQL 成功结果夹具。类型检查与测试发现不等于真实运行；缺少 Docker 或真实环境时必须报告阻塞。
源系统一对一兼容、100,000 行实测容量、历史补采及永久清理策略仍属未完成验收。


## 系统存储与资源依赖健康（F52）

关联 AD-F-196、199、200、201、206。`系统健康` 工作区读取 `system` 菜单和八条精确
`/api/system` 操作权限；角色编辑器保存并读回该标记，读取权限不允许修改告警设置。

- 系统信息只显示实际平台/构建信息和服务器能力。缺失 IP、公司、版本、授权许可和升级信息明确显示未提供、未配置或不支持，不生成许可证、引擎或升级入口
- 仅选择服务器登记的 `local-api` 实例和存储目标 ID，不能输入 URL、主机地址或文件路径。默认存储目标为 `runtime-root`；挂载点仅作展示
- CPU、内存、运行时长和负载明确为 `kernel_visible` 内核可见值；不将其解释为容器配额。存储为 `runtime_filesystem` API 运行环境文件系统，不等同于 PostgreSQL 数据盘；字节字符串使用 BigInt 展示，保留大于 JavaScript 安全整数上限的精度
- 真实样本包含来源、范围、UTC 采样和检查时间、可用性及过期标记。未采样、不支持与空值不显示成 0。数据库不可用、撤权或无效响应会清空旧数据显示；读取可自动恢复，写入不自动重试
- 历史提供 15 分钟、1 小时、6 小时、24 小时窗口，使用服务器检查时间作为查询终点。原始百分比保持十进制字符串，统计空值保持空值；实际观测点散点图和缺测区域不补点、不连线、不插值，保留同一 Unix 秒内的不同观测
- 文件系统表格显示总量、已用、可用、保留空间、文件系统、挂载点、阈值、版本、可用性与告警状态。85% 是本地默认策略；只支持 85–90 整数告警阈值，自动清理和日志保存天数不支持
- 阈值修改携带当前版本、Cookie/Origin/CSRF、操作者密码和新鲜未使用 TOTP，发送后立即清空证明字段。重复提交被同步锁定；版本冲突或网络/响应不确定时必须先读取最新设置，不能盲目重发
- 取消、切换分类、账户导航及 Back/Forward 中断等待并丢弃过时响应；取消不能撤销已经执行的事务。所有系统健康数据与证明均不写入 localStorage/sessionStorage
- 服务与依赖检查区分实际 PostgreSQL 协议/模式检查、API、采样器及 Worker 循环活动；缓存/分析引擎未配置时明确显示。Worker 真实循环/执行器租约检查证据和最近成功/活动时间分开展示，没有伪造心跳

`src/System.test.tsx` 为模拟传输的 DOM 与接口契约测试，覆盖权限、精确字节、空值、过期、
重复/迟到响应、历史切换、CAS 冲突、503/403 清理、取消/Back、证明清空和权限目录保留。
这不代表实际 PostgreSQL 或 Linux 采样已经验收。

`e2e/system.spec.ts` 使用新的可丢弃 bootstrap 数据库、真实本地 Linux API 和 Worker。
以 `python3 scripts/test_auth_e2e.py --suite system` 运行独立编排。测试需要
`ADTR_E2E_USERNAME`、`ADTR_E2E_PASSWORD`、`ADTR_E2E_BASE_URL` 和父编排器拥有的
`ADTR_E2E_WORKER_PID`；API 与测试进程须共享本地 Linux `/proc` 及根文件系统观测范围。
它实际比较 meminfo/uptime/statfs、查询持久化历史及统计/缺测、保存并重新加载阈值、验证 CAS
和 CSRF/Origin、创建系统只读角色及有效 MFA 下的写入拒绝；最后只终止指定的合成 Worker
子进程，核验其观测过期，而 API/PostgreSQL 保持健康。父编排器负责清理，不提供产品停止端点。
真实 TOTP 窗口和活动过期等待不被绕过；用例超时 420 秒。测试发现/编译通过不等于真实运行通过。
源码一对一兼容、授权许可/引擎升级/自动清理、远程节点、数据库数据盘发现、生产容量和 Windows/AD
关联链路仍需各自验收，不由本 UI 推定完成。

## 管理操作账户（F03 Stage A）

关联 Issue #17 / AD-F-008–011。此切片登记已存在域账户的凭据，保存状态恒为
「已保存，未验证」。不创建远程 AD 账户、不修改其密码、不删除或停用远程账户，
不执行检测、安装、明文显示或凭据导出；源字段和完整产品验收仍需后续证据。

- 独立 `operation_accounts` 权限标记与七条精确路由检查；权限编辑器完整保存十一项功能标记
- 所属域从 `/api/operation-accounts/domains` 的安全分页投影选择，仅需此模块读取权限和显式 F47 域范围；无需扩大 `domains` 权限，也不要求用户手填版本
- 获授权账户列表按域名及本地标签筛选，提供分页、当前详情、标签修改、明确成对替换凭据和输入完整账户 ID 确认的本地删除；用户名和密码始终不从服务器读取
- 标签省略时保留，显式空值清除；编辑默认保留凭据，切换到替换时输入框为空。每次写入要求操作者密码及新鲜 TOTP，发送、取消、导航、重新读取及身份失效均清空凭据和证明表单
- 记录和凭据版本使用规范十进制字符串。并发修改阻止旧表单继续提交，重新读取后销毁旧表单并绑定最新记录版本
- 每个操作者的未确认操作仅保存非秘密操作种类、幂等键和记录/域 ID；同一标签页的重新登录、刷新、取消和导航保持原编号。不同操作者的记录隔离，读取时失去授权不销毁待核对编号
- 丢失响应通过原始 `/mutation` 回执核对，不自动重发写请求或生成新编号；404 不代表删除成功。历史提交版本与当前版本分别展示，确认回执后重新读取当前记录
- `operation_account.N` 审计来源沿用受保护、域范围内的记录展示；不暴露用户名、密码或凭据载荷

`src/OperationAccounts.test.tsx` 是模拟传输的 DOM、安全投影、权限、分页和恢复契约测试。
`e2e/operations.spec.ts` 使用真实浏览器、API、PostgreSQL 和新 bootstrap 账户，沿用
`ADTR_E2E_USERNAME/PASSWORD/BASE_URL`。父编排器提供有效一天、域容量为二的租户及独立域密钥，
不预建域、不启用网络探针。测试自行登记 F01 父域并执行 F47 显式授权，再检查 F03 CRUD、
真实提交响应丢失、重载恢复、双编辑器冲突、凭据替换、浏览器状态清理及父域删除依赖。
它只中断真实请求的返回，不编造响应或绕过 TOTP；480 秒超时等待实际验证码窗口。
凭据相关 trace、截图、视频关闭。测试发现和本地 DOM 通过不代表真实集成已执行或产品验收。

## SOC navigation and user-assets presentation

Issue #91 adapts the PR89 visual direction to the existing real APIs: grouped desktop/mobile navigation, compact resolved-source summary, user list/cards and fixed-observation detail drawer. All previous authentication, permission and data-source boundaries remain. Scope and current evidence: [SOC production UI](../docs/soc-production-ui.md). Prototype examples are not imported into production.
