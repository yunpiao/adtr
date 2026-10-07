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
