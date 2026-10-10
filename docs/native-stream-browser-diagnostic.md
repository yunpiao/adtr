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
`ADTR_E2E_CHROMIUM_PATH` 只对这个 job 选择二进制，仍使用同一锁文件、
同一个五用例 spec、同一个真实生产读取器和 headed Xvfb 启动方式。
归档、spec、读取器摘要及版本写入独立 provenance artifact；每个用例的
原始字节摘要、EOF、取消状态和 Playwright/CDP 终态仍独立记录。

## 对照结果与取舍边界

本次新增对照 job 尚待精确提交 CI 执行，不能提前宣称固定构建通过。
Dev 是官方预发布渠道，不是稳定版；较新的引擎与现有 Playwright 版本的兼容性
也要实际验证。固定源码含修复与下载摘要校验并不等于运行证明。

原来的 `native-no-store` 141 基线及此前全部 30 个 job 保留；
`verify` 同时要求基线与固定构建对照成功，没有 expected-failure、
continue-on-error、重试掩盖或阈值放宽。正例仍必须同时取得原始完整字节/EOF
和真正 `requestfinished` / `Network.loadingFinished`；任何 `ERR_ABORTED`
仍失败。新增对照不更换默认浏览器、包锁、现有业务测试或生产代码，
也不据此选择最终修复方案或宣布整个流水线已恢复。
