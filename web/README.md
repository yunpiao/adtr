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
