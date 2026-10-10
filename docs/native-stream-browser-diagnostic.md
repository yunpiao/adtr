# no-store 原生流完成：浏览器对照诊断

本记录属于 [PR #90](https://github.com/yunpiao/adtr/pull/90) 的工程诊断，
不替代 F14、真实 AD 或产品验收。生产读取器、`Cache-Control: no-store`、
8 MiB 上限和现有浏览器断言保持不变。

## 已执行的旧浏览器基线

提交 `e96425e8c40690a3f3ab37ec9bb34c5b5e4a2c38` 的
[CI 38027555775 / native-no-store job 114141522008](https://github.com/yunpiao/adtr/actions/runs/38027555775/job/114141522008)
使用锁定的 Playwright 1.56.1 / Chromium 141.0.7390.37：

- `held-eof` 和 `held-second-chunk` 两个正例均失败。实际生产读取器完成 JSON 解析，
  收到完整 54 字节及未取消的 EOF，原始字节 SHA-256 一致；没有应用信号 abort、
  reader/body cancel 或服务器提前关闭。但是 Playwright `requestfailed` 和
  CDP `Network.loadingFailed` 均报告 `net::ERR_ABORTED`。
- 真实 AbortController 取消、合法 JSON 前缀但 Content-Length 不足的截断、
  超过 8 MiB 的流式上限三个负例均通过。取消导致的 `done=true` 不被当成 EOF。
- 原始字节 SHA-256：`7735cf598e92e05b8e1bba54dc863c14448aefb0509c896f80e1298f6ce282cf`。
  生产读取器源码 SHA-256：`fc4912f23e4b0c5a24decca2b050b94fda09413cc47b4b36afa61a4ed875060d`；
  实际 Vite IIFE SHA-256：`09f29ffaf40a64551bbb6f2a8d01ea5d5349ddd3dda9611e3ae69a8d51ffbf32`。

独立的回环 HTTP 服务在原生下一次 read 已发出后才释放第二块数据或 EOF；
不依赖睡眠、LDAP、数据库、会话撤销、route.fulfill、响应克隆或替代解析器。
这重现了浏览器的 EOF 与网络完成事件不一致，不能据此把任意取消归类为成功。

## 上游修复与固定对照构建

Chromium 官方修复
[`62473ad0f747e245e514c93e741bb8e168a2a207`](https://github.com/chromium/chromium/commit/62473ad0f747e245e514c93e741bb8e168a2a207)
在通知消费方之前记录终态，避免已完成流的重入取消被误报为网络失败；
上游同时增加完成/失败与 DevTools 网络事件回归测试。

2026-10-09 的 [Google 官方桌面 Dev 发布说明](https://chromereleases.googleblog.com/2026/10/chrome-dev-for-desktop-update_0380080721.html)
列出 Windows、Mac、Linux 版本 `157.0.8092.0`。
其 [固定源码标签](https://chromium.googlesource.com/chromium/src/+/refs/tags/157.0.8092.0)
指向 `2b9f0645d8f651a36f74683f22a3746379f13bc6`；
该提交的 [response_body_loader.cc](https://chromium.googlesource.com/chromium/src/+/2b9f0645d8f651a36f74683f22a3746379f13bc6/third_party/blink/renderer/platform/loader/fetch/response_body_loader.cc)
包含上述先记录终态、再通知消费方的修复。

独立 `native-no-store-fixed` job 仅为对照运行固定的官方 Chrome for Testing Linux64：

- 下载地址：[157.0.8092.0 官方归档](https://storage.googleapis.com/chrome-for-testing-public/157.0.8092.0/linux64/chrome-linux64.zip)
- 长度：201751736 字节
- SHA-256：`28d4f185c9047a91fe79ee8d31e5e0b5b858fc107129b3df361fdd095dbb2ace`
- 2026-10-10 独立下载计算的 MD5：`80e3316f94ae04aa979c592bb980165a`，
  与 Google 对象响应的 ETag 和 `x-goog-hash` 相符。SHA-256 是本次下载计算后固定的摘要，
  不声称是 Google 发布的签名或官方 SHA-256 清单。

CI 先检查长度与 SHA-256，再解压执行，并严格检查
`Google Chrome for Testing 157.0.8092.0` 版本。通过现有的
`ADTR_E2E_CHROMIUM_PATH` 仅对固定引擎探针与两套固定引擎资产 job 选择二进制，仍使用同一锁文件、
同一个五用例 spec、同一个真实生产读取器和 headed Xvfb 启动方式。
归档、spec、读取器摘要及版本写入独立 provenance artifact；每个用例的
原始字节摘要、EOF、取消状态和 Playwright/CDP 终态仍独立记录。

## 同提交固定引擎运行结果

提交 `e34e4713fa7ab68c1d88c82bf66af5f090a8fd07` 的
[CI 38028283558](https://github.com/yunpiao/adtr/actions/runs/38028283558)
在同一测试合并树运行对照：141 的两个正例再次出现完整原始字节/EOF 与原生
`ERR_ABORTED` 的分歧；157.0.8092.0 的五个用例全部通过。
固定引擎的两个正例均为相同 54 字节、相同摘要、生产解析完成、零取消，
并独立取得 Playwright `requestfinished` 和 CDP `Network.loadingFinished`。
实际源码与 bundle 摘要和旧引擎一致。原始失败记录保留，不改写为通过。

## 两类证据同时要求的修复分配

默认模式仍是 `native-finish`。固定 157 job 继续要求原始完整字节、
未取消 EOF、原生完成及三个负例；另外新增 `user-assets-fixed` 两个必需 job，
运行**完整** `user-assets-v2` 和 `user-assets-v2-readers` 真实浏览器/API/Worker/
PostgreSQL/合成 TLS LDAP 套件，包含关闭面板、搜索竞态、原生跨标签及通知失效回退、
真实 401 会话撤销和来源 404 场景。所有 held-response 正向完整读取证明
额外要求严格原生完成；401/404 保持其真实 HTTP、清空与安全断言。

原有 30 个 job 和所有旧浏览器场景仍必需。141 仅在显式 `stable-body` 模式下
使用独立的应用读取证据，且必须核对实际 `browser.version()` 恰好为
`141.0.7390.37`，不能靠环境变量声称版本。旧引擎仍运行同一个独立解析器探针，
直接检查实际生产解析 Promise、原始字节/EOF 及全部负例。

两套资产 suite 共用 held-response helper。先在 `route.fulfill({response})`
之前取得真实 APIResponse 原始字节，再与应用**原有 reader** 观察的字节数、
SHA-256、完整 JSON 和元数据逐项匹配；不以 JSON 重编码摘要代替原始字节。
原始响应不改写，不 clone/tee、补读或另发 GET。观察器原子提取终态时核验
同一个 Request、文档 epoch、method/完整 URL/序号、当前主机/页面计数、
状态、no-store、actor、一次 reader 获取/释放、未取消 EOF 和原始 signal。
隔离 abort 的 token 也绑定相同文档和调用序号，必须真实发生过 abort 抑制。

应用读取事实与原生网络事实分开返回：`complete-consumed` 不等于
`requestfinished`。独立 CDP 会话按相同主框架、method/完整 URL 和本次监听序号
匹配精确请求；必须有唯一 Playwright 和 CDP 终态且两者一致。
仅当全部应用原始字节证据成立、版本精确匹配，并且两路终态均为
`ERR_ABORTED`（CDP canceled=true），141 才记录“应用已完整读取、原生 requestfailed”。
缺少证据、未知错误、重复/矛盾终态、hash/身份/状态不匹配均失败；
没有将取消当作 delivered 的通用白名单。157 的相同分支始终失败。

观察器证明的是实际应用 reader 完整读取，并非直接钩住业务 API Promise 的最终返回；
业务 schema 校验成功仍由已知真实数据、未改动的控制流和 UI/安全断言支持。
独立探针则直接等待生产解析器 Promise，二者的证明范围明确区分。

普通历史 JSON 与严格实时 witness 的资格不同，且不由 HTTP 成功/失败状态划分。
`directoryV2ResponseJSON` 仅在原始 reader 已取得有效 UTF-8/JSON 的真实 EOF、
全程无实际 cancel，并且原生 `releaseLock()` 成功返回时 signal 仍未 abort，
才锁定一次已完成读取的历史事实。随后成功回调引起组件卸载，或错误处理清空来源/详情，
都可能再 abort 控制器；历史 JSON 可保留该有界完整响应及精确身份/元数据，
同时如实记录后来 signalAborted=true。摘要尚在计算时也必须先具备上述 release-time 证明，
不能只凭 done=true 保留响应。

`directoryV2ResponseWitness` 每次都检查实时状态；即使普通 JSON 已读取或缓存，
任何后来 abort 仍使严格 witness 失败。两者共用一次原子 body take，不能借用别的请求，
也不能把历史 JSON 当成业务 API Promise 成功或原生网络完成证明。EOF 前 abort、
实际 reader/body cancel、失败或缺失的原生释放、无效内容及身份不一致仍失败；
所有正向 held 原始字节与严格原生完成要求不变。

`verify` 同时要求全部旧 suite、两套完整 fixed-engine suite、旧引擎应用读取探针和
固定引擎严格原生探针成功。没有删除场景、continue-on-error、跳过或增加超时。
Dev 是官方预发布渠道，不是稳定版；新增兼容性证据不能证明所有稳定版浏览器已修复。
默认浏览器、包锁、生产代码、缓存策略和 8 MiB 上限不变。上述最终分配还需本次精确提交
CI 与独立审查；不能沿用较早五用例对照的成功声称新完整套件已通过。

## 完整套件的 grant 清理回归与本地修正

提交 `999ba5fab0655f79a5307eb1930727ac426b6f31` 的
[CI 38030405086](https://github.com/yunpiao/adtr/actions/runs/38030405086)
中，旧引擎与固定引擎的两个独立探针均通过，但四个完整资产 job 均失败：

- 141：[普通资产](https://github.com/yunpiao/adtr/actions/runs/38030405086/job/114150012228)、
  [只读/跨标签资产](https://github.com/yunpiao/adtr/actions/runs/38030405086/job/114150012193)
- 157：[普通资产](https://github.com/yunpiao/adtr/actions/runs/38030405086/job/114150012060)、
  [只读/跨标签资产](https://github.com/yunpiao/adtr/actions/runs/38030405086/job/114150012216)

四者均在目录凭据 grant 设置阶段的普通响应验证中失败，尚未执行 held-response
正向证明，不能把独立探针通过当作完整套件通过，也不能把这些失败记为 held 原生完成失败。
同一 head 的 [directory-v2](https://github.com/yunpiao/adtr/actions/runs/38030405086/job/114150012195)
与 [directory-v2-controls](https://github.com/yunpiao/adtr/actions/runs/38030405086/job/114150012252)
也在成功 grant 提交后的观察器验证位置失败。旧日志没有 signal 等内部标记，
只证明这些验证位置失败；下面的同生产路径回归独立复现清理 abort 的缺口，
不能据此声称每个旧 CI 运行的 abort 状态已被实际记录。
旧提交的历史 JSON 仅允许完成错误响应后的清理 abort，遗漏了成功响应后的同类生命周期。

`DirectoryResponseObserverLifecycle.test.tsx` 使用实际生产
`directoryV2CredentialUseAPI.mutate` 和 `useTaskMutation`，在成功回调中改变 React key
触发真实 hook 卸载清理，并明确延迟摘要计算。两个调用顺序（先普通 JSON、先严格 witness）
在旧 head999 上均稳定失败；修正后普通 JSON 返回原始完整 grant 响应，严格 witness
在两种顺序和缓存重读中仍拒绝后来的 abort，且 body 只原子提取一次。
这是实际生产 API/React hook 配合合成 Fetch 响应的本地回归，不是新的真实浏览器/数据库运行。

当前本地修正只去除已完成历史读取的 HTTP 状态限制；完成标记仍在成功原生释放之后锁定，
其余字节、身份、取消与实时 witness 门禁保留。验证失败新增固定枚举和布尔标记，
区分完成状态、EOF、signal、释放次数、取消、字节/摘要合法性及身份/元数据匹配；
不输出响应正文、头部、URL、请求参数、原始异常文本或认证证明。
旧 head999 的失败日志与 red 回归保留。当前修正提交仍须独立审查和精确 head 的完整 CI；
本地 red→green 与较早固定引擎五用例成功均不替代这个门禁。

## 独立的只读 fixture GET 恢复

[旧 CI job 114141522177](https://github.com/yunpiao/adtr/actions/runs/38027555775/job/114141522177)
另有 APIRequestContext GET 的 `ECONNRESET`。实际 TOTP 等待与 Go 30 秒
IdleTimeout 的边界支持空闲连接关闭竞态这一可能解释，但没有证明原事件根因。
这不是上述浏览器 no-store 完成缺陷。

本 PR 的只读 fixture GET helper（`web/e2e/fixture-get.ts`）
仅用于两套资产 suite 的 fixture-side `readJSON` GET，在连接重置时至多重试一次，
保持原 Cookie jar，
不重试状态码响应或任何写操作。53 项测试包含 10 项真实 Node 24.19.0 /
Playwright 1.56.1 回环连接实验，证明复用 socket 重置后单次新 socket 重试、
Cookie 与 503 不重试行为；这些实验不能倒推原 CI 根因。held-response 的
`route.fetch` 仍禁用重试，原始字节与原生事件证据不经过这个恢复 helper。

提交 `ddb5d1ca7ee4ff5373d06c875336c70a88043ba4` 的
[固定引擎普通资产 job 114153652806](https://github.com/yunpiao/adtr/actions/runs/38031626256/job/114153652806)
在 grant 成功后另一次 fixture GET 报出 `transport_error`，没有重试记录；旧诊断未保留
原始原因，不能据此判定为 ECONNRESET 或空闲连接关闭。新增诊断仅给出固定类别和尝试次数，
仍只匹配完整首行、剥离原始错误/cause，不扩大重试资格或宣称修复这个历史传输故障。
真实回环负例另覆盖 socket hangup、部分响应后延迟中止、上下文关闭/释放及连接拒绝，
这些终态均不重试；恶意首行和调用日志内容不能获取重试资格或泄露进诊断。

## 结构化读取器证据留存

同一 ddb5d1c 提交的两套完整 reader job 均通过，日志中的 held 原始字节/原生完成与
会话撤销断言成立；但下载归档只保留截图（固定引擎另有 provenance），缺少两个 JSON。
锁定的 Playwright 1.56.1 将 `testInfo.attach({body})` 留在内存，文件上传 glob 不会收取它。
因此新增显式 `outputPath`/`writeFile` 后按路径附加同一份实际 JSON，命名为
`user-assets-v2-native-transport-outcomes.json` 和 `user-assets-v2-revocation-native-evidence.json`。
内容表达式、断言及旧/固定引擎上传规则不变；新增源契约在旧实现上失败。
新精确 head 的实际归档仍需下载核验，不能把已通过的旧 reader job 说成已留存这些 JSON。
