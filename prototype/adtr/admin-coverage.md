# 平台管理工作区：路由、动作和范围

此文件是工程映射，不在产品页面渲染。数据来源是 `requirements/catalog.json` 的 F39、F42–F47、F51–F57，共 40 条需求，以及 `docs/design/overall-design.md` 的管理、安全和运维边界。`admin-pages.js` 提供 14 个页面和 48 个合成对象，不调用网络，不含真实账户凭据，不代表任何真实后端能力或产品验收。

## Renderer 接线合同

- 全局：`window.ADTR_ADMIN_PAGES`，classic JavaScript，静态数据。
- 原始必需字段均保留。所有页面 `scope: platform`；所有对象 `domain: platform`。审计只放平台管理记录；诊断仅有平台入口、数据库与平台日志，不借管理页面展示 AD 业务对象。
- `action.intent` 区分同一 verb 的业务语义。缺失时采用普通 create/edit/delete/inspect；新增时生成本地 ID，编辑保留 ID，删除可恢复且要确认。不能把所有 intent 都当作通用 CRUD。
- `bulkActions`、`toolbarActions`、`sessionActions` 与 `primary` / `rowActions` 同一动作结构。批量操作先冻结选择；空选择禁用。
- `row.fieldKeys` 指明该设置分组的可编辑字段；不可把所有分组缺失字段保存成空字符串。checkbox 保持布尔值，number 保持数值并使用 min/max。编辑分组时同时刷新 summary/updated。
- `action.appliesTo`、`excludeIds` 指定行适用范围；`requiresArtifact` 限制下载；`row.canStart/canStop/canRestart` 限制运维动作。
- `guards` 是动作检查条件，不能当成页面展示文案。按钮禁用不替代提交时重新核对角色、域、身份版本与对象状态。
- 不将 `special`、`guards`、`intent`、`credentialInput`、字段 key 等内部值展示为原始属性。详情只展示有正常 label 的业务字段。这个文件中的需求编号、边界和测试说明也不能进入 UI。

## Special 与 intent 的有限全集

以下状态只变更浏览器中的合成对象，且写入本地平台操作审计。异步步骤使用真实的可取消计时/状态过程；关闭、返回、重复点击或身份切换不得留下多次提交或旧身份回写。

| special | intent | 必须产生的交互与状态 |
| --- | --- | --- |
| profile | edit_profile | primary 打开基本资料；行操作使用 fieldKeys。保存更新对应分组、摘要与时间。avatar 用 avatarPresets 的四个本地视觉选项，更新顶部头像；禁止上传真实身份图片。 |
| account-security | select_account | 选择现有 accountRef 后进入选定账号详情/安全流程；不创建新账号、不接受密码。 |
| account-security | session_toggle | 已登录→已退出，清理会话和私有视图；未登录→按 authFlow 进入安全步骤。first_login/expired 必须先 password_change 再验证；完成才是已登录。 |
| account-security | password_change | 显示“确认安全更新”按钮和会话失效影响；确认后 passwordState=有效，authFlow=active，旧会话失效；不出现真实密码输入。 |
| account-security | sessions | 查看该账号合成会话、登录时间、有效期，不更改状态。 |
| mfa | select_mfa_account | 选择已存在的账号并进入管理详情，不新增凭据。 |
| mfa | mfa_toggle | 未启用→四步绑定→已启用；已启用→确认停用→未绑定/未启用，并清除 verified。禁用前确认影响。 |
| mfa | mfa_verify | 显示账号和验证状态；点击完成验证后 verified=true、lastVerified=当前演示时间，并记录当前身份的验证窗口。无验证码或秘密输入。 |
| mfa | mfa_reset | 确认后 enabled=false、verified=false、status=未绑定、boundAt=未绑定；保留历史。 |
| users | assign_roles | 单选或批量角色选择；确认目标、角色和资源范围后保存，受影响会话失效。不能编辑自己的权限、不能让当前身份自提权。 |
| users | admin_password_reset | 确认目标用户→密码状态改为首次登录需更新并结束目标用户会话；不生成或展示密码。 |
| users | user_toggle / disable_users | 当前用户与最后管理员不可停用；其余启用↔停用，批量停用只设停用而不反向启用。 |
| users | delete_user | 确认后仅把可删用户从活动视图移除，保留可恢复记录；当前用户/最后管理员拒绝删除。 |
| permissions | edit_permissions | 独特权限矩阵使用 permissionMatrix 分组及布尔字段。写入依赖读取；未选即拒绝。保存时重新检查当前身份、最后管理员及角色关联，确认受影响用户会话失效。 |
| permissions | role_members | 按 row.members→users ID 展示成员，不把 memberCount 当成员数据。 |
| permissions | delete_role | 有关联成员、当前身份角色或最后管理员角色时拒绝；其余经确认可恢复移除。 |
| resources | edit_resource / associate_roles | 分别维护名称/域集合与角色关联；domainSet 同步为 domains，roles 同步为 roleIds。不改变操作者自己的授权；撤权使受影响会话/视图失效。 |
| resources | delete_resource | 有关联角色时先解除关联才可删除；确认后可恢复移除，保留审计。 |
| resources | tenant_settings | 用 tenantFields/tenantSettings 独立编辑租户名、默认时区、会话时限；保存后更新页面引用，不新增资源组。 |
| audit | audit_export | 导出当前筛选或已选记录为本地合成文件；导出前重新检查权限，只包含平台审计白名单字段，不把表格HTML/隐藏行整页下载。 |
| audit | audit_archive | 当前可见记录→archived=true/status=已归档；已归档→false/可见。默认排除归档记录，筛选可查看和还原；绝不物理删除。 |
| integrations | authorization_preview | 展示应用、回调地址、范围、有效期以及四个授权步骤，不发起外部跳转。 |
| integrations | integration_toggle | 授权中→确认撤销→已撤销/enabled=false；未授权→核对范围和有效期→已授权/enabled=true。过期引用须先编辑有效期；不创建 token。 |
| integrations | integration_test | 本地核对已登记引用和到期日，结果为有效/过期/待授权；不能用点击直接伪报网络认证成功。 |
| system | storage_settings | 使用 page.settings 打开存储管理表单；保存阈值/保留期/通知偏好，不创建“节点”。阈值范围85–90。保留期改变不删除任何数据。 |
| system | refresh_health | 展示刷新中→刷新本地采样时间；缓存未配置仍保持未配置。不能把未观测依赖改健康。 |
| license | license_import | 只选择已有 packageRef；定位/克隆对应候选许可打开授权预览，不接受真实文件或任意许可文本。 |
| license | license_preview | 显示设备、有效期、模块、额度和当前用量，保持当前激活许可不变。 |
| license | license_validate | 校验 deviceMatched、时间范围及 currentUsage≤候选额度；所有条件通过才 validation=通过，否则明确失败原因。 |
| license | license_activate | 确认后再次校验，失败不得改变 active；通过后旧许可 active=false，新许可 active=true/status=已激活。不得将不匹配设备变为匹配。 |
| settings | edit_settings | primary 先选择设置分组；行操作按 fieldKeys 编辑，保存原对象，不创建新设置组。 |
| settings | sync_time | 仅 settings-time 可用；自动同步启用时完成本地状态与时间更新；关闭时提示选择自动同步或手动设置。无真实NTP请求。 |
| operations | operations_login | 以现有 demo.maintainer 进行无密码按钮验证→session.status=已登录/verified=true，记录会话有效期；不扩大实际角色权限。 |
| operations | operations_logout | session=未登录/verified=false，清理运维详情与待确认操作；不退出普通业务会话。 |
| operations | service_toggle | 有效运维会话＋新鲜验证，确认目标/影响/队列后运行中→停止中→已停止，或已停止→启动中→运行中。整个平台禁止启动/停止动作。 |
| operations | service_restart | 确认名称、affectedScope、impact；停止中→启动中→检查依赖→运行中，recovery相应更新。整个平台重启完成使普通与运维会话失效并要求重新登录。 |
| operations | service_recovery | 展示恢复步骤与最新状态；操作运行中保持状态查询，不重复启动任务。 |
| diagnostics | diagnostic_run | 网络行显示检查步骤→合成明确结果；日志行按时间和level筛选logLines→打包中→已打包/artifactReady=true，更新result/lastRun。需要有效起止时间和登记目标。 |
| diagnostics | diagnostic_result | 网络显示DNS/TCP/TLS检查；日志显示过滤的记录与打包状态。 |
| diagnostics | diagnostic_download | 只对 artifactReady=true 下载该行本地logLines和元数据，使用artifactName；其他行禁用。不包含真实秘密。 |
| install | installation_precheck | 四步步骤器环境→配置→预检→结果；检查固定目标/容量/依赖。容量不足保留失败；通过才 precheckPassed=true。 |
| install | installation_start | precheckPassed 且重新核对目标与容量；确认影响后安装中→逐步推进steps/progress→已完成或失败；不执行真实命令。 |
| install | installation_steps | 展示阶段、目标与每步结果；运行中查询原过程，不再次启动。 |
| install | installation_record | 下载所选部署的本地步骤记录与结果，注明合成数据；不生成可在真实环境执行的安装器。 |

普通 create/edit 也必须进行必填/类型验证，并使用同一权限边界。第三方集成只接受预设凭据引用；诊断只接受 allowedTargets；安装只接受已登记目标。不能用“点击即成功”的 toast 代替步骤、结果和数据状态。

## 40 条需求映射

| 需求 | 路由 | 字段、入口和动作 |
| --- | --- | --- |
| AD-F-153 平台操作记录 | admin/audit | actor、auditType、target、result、time、requestId；各管理动作追加记录 |
| AD-F-154 审计列表与筛选 | admin/audit | 类型、操作人、结果、时间、归档状态筛选；查看详情 |
| AD-F-155 审计导出 | admin/audit | 主按钮导出审计；选择后批量导出 |
| AD-F-156 审计记录删除 | admin/audit | 归档/还原；采用可恢复可见性变更，不实现物理删除 |
| AD-F-167 登录与退出 | admin/accounts | 选择合成账号、登录/退出、sessions |
| AD-F-168 首次登录与密码过期 | admin/accounts | first_login/expired 账号；securityFlows及password_change |
| AD-F-169 MFA管理 | admin/mfa | requirement、绑定状态、启用/停用、验证、重置 |
| AD-F-170 个人资料与头像 | admin/profile | displayName、email、team、头像四选一、语言与偏好 |
| AD-F-171 用户列表 | admin/users | 用户、登录账号、角色、资源组、状态、最近登录 |
| AD-F-172 新增编辑删除用户 | admin/users | create、edit、delete_user、启用/停用 |
| AD-F-173 密码修改与重置 | admin/accounts；admin/users | 个人password_change；管理员admin_password_reset |
| AD-F-174 角色列表详情 | admin/roles | 角色、权限摘要、成员数、详情与role_members |
| AD-F-175 角色保存与删除 | admin/roles | create、权限矩阵edit_permissions、delete_role |
| AD-F-176 单个与批量角色分配 | admin/users；admin/roles | 单个/批量assign_roles；role_members查询 |
| AD-F-177 权限配置 | admin/roles | 7组19个独立布尔权限，包含访问/导出/执行/管理 |
| AD-F-178 菜单页面操作控制 | admin/roles及全局框架 | permissionMatrix作为功能边界；渲染和提交均校验，默认拒绝 |
| AD-F-179 AD资源访问控制 | admin/resources及全局框架 | domains、roleIds、默认无域授权；与功能权限分别判定 |
| AD-F-180 资源组列表详情 | admin/resources | 租户、域集合、关联角色、版本、详情 |
| AD-F-181 资源组维护 | admin/resources | create、edit_resource、delete_resource |
| AD-F-182 角色与资源组关联 | admin/resources | associate_roles；roleIds与domainSet对应关系 |
| AD-F-183 租户配置 | admin/resources | 工具栏tenant_settings；租户名、时区、会话超时 |
| AD-F-184 第三方授权与认证跳转 | admin/integrations | 应用/范围/凭据引用/回调/到期日；授权流程、启停、校验；不外跳 |
| AD-F-185 许可信息与有效期 | admin/license | 类型、设备、额度、期限、状态、功能范围 |
| AD-F-186 许可更新与激活 | admin/license | license_import选择候选；license_activate |
| AD-F-187 授权预览 | admin/license | license_preview；候选与当前授权、当前用量 |
| AD-F-188 设备绑定与许可校验 | admin/license | deviceMatched、许可日期、额度、license_validate |
| AD-F-196 系统信息 | admin/system | 版本、部署标识、时间、OS、运行方式 |
| AD-F-197 系统标识 | admin/settings | systemName、logoPreset三种本地图形方案 |
| AD-F-198 时间与同步 | admin/settings | timezone、ntpEnabled、ntpSource、manualTime、sync_time |
| AD-F-199 存储管理 | admin/system | 容量、storageWarning、auditRetention、diagnosticRetention |
| AD-F-200 系统资源监控 | admin/system | 各节点CPU/内存/存储、采样时间、统计卡 |
| AD-F-201 服务与运行状态 | admin/system；admin/services | 依赖状态、服务状态、节点、版本、恢复状态 |
| AD-F-202 服务启动停止重启 | admin/services | service_toggle、service_restart、canStart/Stop/Restart、影响确认 |
| AD-F-203 平台重启与恢复 | admin/services | 整个平台行、重启影响、恢复步骤和重新登录 |
| AD-F-204 网络诊断 | admin/diagnostics | 固定平台入口/数据库目标，DNS/TCP/TLS步骤和结果 |
| AD-F-205 日志查询打包下载 | admin/diagnostics | 类型/服务/时间/级别筛选、logLines、打包与受控下载 |
| AD-F-206 平台与依赖健康 | admin/system | API/Worker/PostgreSQL健康、缓存未配置、采样时间 |
| AD-F-207 运维登录退出 | admin/services | page.session、operations_login、operations_logout |
| AD-F-208 平台管理项 | admin/settings | 会话超时、登录提示、维护模式 |
| AD-F-209 安装部署 | admin/install | 固定目标、部署方式、容量、依赖；预检/安装/步骤/记录 |

## 不能模拟或不能据此宣称完成的范围

1. 本文件记录管理页面的数据与动作设计；实际行为由 workbench.js 和 admin-engine.js 实现。动作名和 schema 本身不是交互证据；本轮精确提交的浏览器结果以 PR #89 链接为准，未运行前不宣称通过。
2. 不连接生产 API、AD、真实网络端点，不做真实服务重启、操作系统时间修改、安装、网络探测、数据库维护或物理文件清除。
3. 不提供密码输入、真实TOTP绑定、二维码/secret、OAuth授权交换、访问令牌生成、许可签名或真实证书校验。安全流程仅为已存在合成账号的步骤状态；不能作为身份验证实现证据。
4. 角色、资源组、菜单、数据范围的前端演示不能替代服务端授权，不能证明跨租户隔离或权限竞争安全。平台管理员页面只含平台元数据，没有业务审计/AD对象的旁路。
5. 审计原需求中的删除在这里收敛为可恢复归档；不隐藏该差异，不声称物理删除兼容。合规保留和实际删除策略需独立评审。
6. 许可、平台版本、硬件额度、时间与资源用量是合成数据；预设许可选项不是真实可用许可。设备不匹配的候选始终不能激活。
7. 安装部署方式、容量阈值和版本为产品流程提案；F57源需求仍标注具体部署方式待定。当前步骤不能证明目标平台、Windows版本或生产安装已支持。
8. 日志包是本地文本产物；没有远端日志、敏感数据脱敏实现或服务器下载授权证据。文件不存在/未打包必须阻止下载，不能用成功提示掩盖空内容。
9. 浏览器内存中的恢复/会话过期/撤权只是交互；页面刷新、并发标签、持久化与后端审计需真实实现后独立验证。
