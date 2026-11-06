# 系统两候选原生证据准备

本改动为 #14 AD-F-196 / FLD-3154 的概览磁盘观测和 AD-F-199 / UI-0646
的存储本页序号补充既有 `system` 浏览器链路断言。它是源码与离线契约准备，
没有执行真实浏览器、PostgreSQL 或实际 PNG 验收。两个产品候选契约分别见
`system-disk-overview-contract.md`、`system-storage-row-numbers-contract.md`。

## 真实来源与原链路保持

在原首次系统导航和原首次存储点击期间监听实际浏览器 GET，绑定新 Request 身份、
主 frame、同源固定路径与默认查询；只在原生 requestfinished 之后读取一次对应
HTTP 200 Response。没有额外 GET、路由替换、重放或合成原生完成事件。
`nativeFinished` 只能由完成事件消费者写入，字节解码自身不能作该证明。

实际后端 `internal/systemhealth/sampler.go` 的磁盘来源是 `statfs`，scope 是
`runtime_filesystem`，目标为 `runtime-root`。旧 DOM 夹具中的 `linux_statfs`
不能充当原生真值；候选透传真实来源，不改写服务端数据。

响应只保留闭集元数据与字节 SHA256，不记录认证数据、响应头或原始正文。
64 KiB 限制仅用于这两个固定小响应的解码和投影，超出时明确失败；删除会失去
该输入边界。Response.body() 已先缓冲正文，所以它不是网络或峰值内存限制，
也不代表产品支持的容量上限。15秒单次截止时间
覆盖现有动作、响应完成与正文读取，不重置、不延长原450秒套件预算；到期失败并
清理监听器，迟到正文不能补写证据。删除该截止时间会使丢失事件长期悬挂。

严格 inverse 契约将4个新块逆变换，逐字节恢复70,294字节前身，SHA256
`062b82b3e3c351e6749ce815df136a2ab460532d805435c8a553894666470227`。
原导航守卫、所有权限/CAS/写入/MFA/worker证明、6状态12张PNG和旧manifest均保留。
未修改 runner、workflow、选择器、门禁阈值、采样或fixture。

## 新增成功证据

两种状态分别在1440×900和390×844采集，共4张 `system-soc-candidate-*` PNG。
采集前后都将候选UI字段与同一已观察响应比较；轮询改变这些字段时失败。
未断言的字段或相同候选值的后续响应不由此识别，不能声称整个UI仍来自该唯一响应。
保留无横向文档溢出和密码/MFA/设置密钥输入为零的保护，不创建失败自动截图。
单独 `system-soc-candidates-evidence.json` 绑定原构建/运行/提交信息、实际响应摘要、
源文件散列、构建产物和4张图，不覆盖旧12图清单。既有上传路径的前缀可容纳新文件，
本切片没有更改上传配置。

实际fixture只有一个 runtime-root。原生准备覆盖第一页序号1和稳定ID/操作目标，
不能宣称真实第二页、多个文件系统、注入旧观测或时钟偏移已验收。相关异常与多页
仅有DOM/字节/事件单元测试。截图函数准备完成也不等于像素检查完成。

## 离线验证与剩余门禁

使用现存同锁文件依赖直接执行 Node 工具、源码 inverse/mutation、观察器事件单元
及实际 sidecar writer 合成契约；不安装、不运行 npm audit 或包含它的 make check/CI。
仍需独立代码复审、授权后的精确head全检查、真实隔离PG/browser执行、实际PNG检查
及main兼容验证。产品验收0/209和原Issue开放范围不变。
