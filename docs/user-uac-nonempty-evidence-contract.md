# 用户 UAC 非空证据：mixed-33-v1

## 范围与结论

`user-asset-export-v3-uac-mixed33` 是独立、只使用合成数据的验收套件。
它补齐 `set`、`clear`、`not_returned` 三个非空分区，每个分区11行、列表10+1，
从第2页发起完整11行导出。未指定禁用位的列表还需覆盖33行四页。
默认30对象/12用户/两LDAP页、旧两次导出及旧六件证据不变。
本文件描述门禁，不把源码、离线单元样本或类型检查当成真实浏览器验收。
真实 AD/Windows、容量及完整产品验收仍不在此切片内。

## 必需开关与代价

- 仅专用runner设置 `ADTR_E2E_USER_ASSET_EXPORT_V3_UAC=true`；fixture profile固定
  `mixed-33-v1`。其作用是选择51对象、四LDAP页，默认不启用；漏设、混合其它模式、
  非法profile均拒绝，不自动退回默认。副作用是该suite需独立运行和独立证据路径。
- `ADTR_LDAP_FIXTURE_UAC_RUN_NONCE` 由runner每次生成一次32位小写hex，同值传给
  fixture和Node。它关联本次candidate/seal/cleanup，并非授权token；缺失或不匹配即拒绝。
- `ADTR_E2E_LDAP_UAC_EVIDENCE_DIR` 只指向launcher拥有的私有目录；移除后无法读取真实
  wire candidate，故C失败；不允许任意上传目录或URI。默认launcher清除继承的C标记。
- C沿用StartTLS/389、64连接、8请求、15秒连接界及既有运行时限；不新增预算。
  owner敏感动作7→8→9→10，producer保持9，仅一次采集/use/observation。
  三次真实create后余量为0；失败不得自动重放或拿第4份proof重试。

## 三个独立证据域

1. fixture只记录实际完整写出的51条Entry BER；四个Done只将cookie值octets原位清零。
   request/Bind凭据不存储。`candidate-wire.json`的原始bytes SHA绑定nonce与首次完整exchange。
2. 所有监听器关闭、owned连接关闭、acceptor/handler join后才封存 `terminal-seal.json`。
   sticky poison、late request、重绑、部分写、未join、非零退出均拒绝。早期candidate不证明终态。
3. Node的私有candidate持有真实PG source/task payload、十页GET pairs+响应、三次安全POST投影、
   原key GET回执、detail、chunk、两次下载与三张PNG；不伪造cleanup成功。
   runner在Worker/API、LDAP、诊断、DB等全部cleanup成功后才调用Python finalizer。
   首错误保留，全部清理仍尝试，任一失败均不得发布成功目录。

## 独立校验

完整literal oracle固定SHA256
`833245587fd68cca1f32f3b140703b8b18a13332a94f04e7f8c83db30a1d210b`。
它来自审查过的手写33用户、18完整decoy、四模式分页和三套工作簿单元格，
不是fixture或生产predicate的运行输出。fixture不得读取它生成实际LDAP数据。

validator用标准库独立解码BER/UUID/SID/时间/uint32/null及原始字段，与全部51对象和literal
逐项比较，再绑定真实列表全部12公共字段及三份完整工作簿。不得先筛user再漏验decoy。
保重复query entries；unknown/duplicate JSON键、BOM、非UTF8、未配对surrogate、nonfinite、
bool/浮点冒充整数均拒绝。合法emoji保留。

两个hash域不能混用：task payload hash是`SHA256("1::" + 按ASCII键序canonicalJSON(payload))`；
安全POST hash是11业务键按客户端顺序的紧凑JSON SHA，不含prefix、idem或proof。
producer/export的payload、version、parent和hash都来自本次PG `adtr.tasks`；
PG只计算并返回idempotency key的SHA，不读取/输出raw key。
create/receipt/detail/original readback/request/PG payload必须绑定同一case。

## 出站请求与截图隐私

全context监听器在导出操作前安装，request事件同步计数并冻结case归属，然后异步解析body、
读取固定page已有sessionStorage locator、关联response；`settleCase(taskID)`完成前不能换case、
清locator或导航。wrong page/frame/origin、重复create、cancel、未知写、requestfailed或缺response
均sticky失败。真实双激活只允许一个POST；关闭/Escape/放弃恢复不允许POST。

app在submit里生成UUID、先同步写locator再发POST。Node不声称点击前已预知key。
11业务expected在点击前冻结；request后只读现有locator，检查精确5键、actor/domain/kind/version
与UUIDv4，再与出站key及PG SHA交叉绑定。缺失storage不使用memory fallback或hook。
原key GET由`readUACOriginalReceipt`短时私有拥有，只返回安全回执；raw key、proof、body、
headers、完整URL/query不落文件/日志/断言diff。C生成验证码时先加入私有本次扫描列表；
POST parser还独立将实际出站proof记入WeakMap私有扫描上下文，覆盖其后全部response、
candidate、console和PNG检查，不把proof加入SafeCreateRequest或证据。已验证idem只把SHA存于
私有run域，后续response/candidate/console的decoded键和值以及PNG文本/value都提取UUID候选
逐个比较所有历史case的SHA；反射raw key在任何candidate落盘前拒绝，raw key不延长保存。
JS字符串和Playwright缓存不能保证物理擦除。

主导出面板会显示raw key，因此**禁止截图该面板**。三张PNG只来自`context.newPage()`创建、
`opener()===null`的后台任务“原导出只读详情”。进入前和拍摄前locator必须自然为null，
不能先removeItem掩盖继承。二次下载按字节及header绑定原件；拍摄前全页password input为0、
主面板不存在、knownSecrets不存在，所有可见文本与input/textarea内canonical UUID候选SHA
均不等于当前keySHA。固定1440×900，无横向溢出，无mask/DOM/CSS隐藏。
trace/video/自动失败截图显式off；三张PNG仍须真正像素独审。

## caps与发布

同时执行：source body256KiB、source SQL stdout1MiB、export SQL stdout2MiB、
create body32768B、Entry单条2KiB、Done合计4KiB、raw frame合计128KiB、
wire candidate160KiB、seal8KiB、final wire256KiB、candidate/final evidence8MiB、
provenance2MiB、单XLSX32MiB、单PNG10MiB。不能用宽cap覆盖窄cap。
SQL沿用2秒statement/5秒命令限制。安全文件读取拒绝symlink/FIFO/路径逃逸和超cap。

finalizer验证所有原件及13键cleanup receipt，原子发布至唯一
`web/playwright-report/user-asset-export-v3-uac-mixed33-validated`，拒绝覆盖既存目录。
最终exact9：provenance、final evidence、嵌入原candidate与seal的raw-wire、三taskID命名XLSX、
`set-ready.png`、`clear-ready.png`、`not_returned-ready.png`。没有第十个manifest。
CLI仅有`capture --repo .`与`validate --repo . [--github-output]`；不提供可伪造success的finalize CLI。
workflow复验同九件成功后，才输出一次`artifact_paths`九个排序精确路径并上传，禁止目录/glob兜底。
Python 校验/发布失败仍只输出固定 `uac_evidence_invalid:<code>`。
浏览器最外层失败只输出 `uac_evidence_invalid:<code>;stage=<stage>`，safeResponse 失败可追加固定 `;check=<check>`：stage/check 为源码固定枚举，
code 仅从完整匹配既有固定错误码的 Error 自有 message 数据属性映射；其他异常统一 unexpected。
不转发原异常、message 后缀、stack、cause、DOM、URL、key 或 proof；getter 不执行。
阶段表示最后进入的操作区间，不证明该区间已完成，也不将未知错误认定为 candidate schema 错误。
这项诊断只为下次真实失败定位；删除会恢复不可定位的泛化失败，不影响验收判定。
它不新增 artifact/schema/path/预算，不修改 validator、成功证据、重试或生产行为。


## C响应原字节等价读取与固定诊断（新增共享范围34路径）

C的 `safeResponse` 使用新增只读 `directoryUACResponseBytes`，不依赖 Playwright inspector 的
`Response.body()` 副本，也不降级到 legacy JSON。既有observer只记录应用实际读取的native chunks，
完整读取/释放后保存原文及原chunk字节数/SHA；完成后没有持久raw buffer可直接取出。
新host接口复用原请求epoch/method/URL/ordinal/count、metadata和atomic take，不改原app fetch/read、
promise/response/reader身份、原观察器捕获或旧JSON/witness API。只允许注册legacy模式的C四对endpoint/method，
不refetch/clone/tee/route，不接收别页/别请求。

在任何新Buffer分配前，native字节数与原文UTF8字节数必须都是1..1MiB且精确相等；
host只读取一次原文/count/hash为同一局部快照，测量与编码必须使用同一string，防止getter换值绕开预分配上限。
然后UTF8编码**未parse的原文**，SHA必须等于原native chunks的SHA。这是受hash绑定的原字节等价，
不是把已parse的JSON重新stringify。TextDecoder吞掉前缀BOM（包括跨chunk）会使长度/hash不等，立即拒绝；
原重复/escaped重复键仍保留给同一 `parseDirectoryV4UniqueJSON` 校验。E继续独立执行1MiB、BOM、
fatal UTF8、unique JSON、object/scalar、最新proof/secret/raw-key扫描，不以共享接口代替这些检查。

此读取沿用observer的15秒完成等待及15秒count-parity deadline；没有提高任何timeout或cap，
C总体900秒不变。它采用历史completedRead政策：应用已完整解析并释放后允许UI清理abort留下的历史内容，
不声称这是live未abort witness；原严格witness行为不改。

同一Response的两个C消费者只作一次take：E内部cache的ready Promise仅resolve void，
另存严验JSON；每caller返回独立clone，按当前actor/status/cache header及最新secret集合复核，
最后metadata/secret检查到return之间无await。entry同时绑定最初actor，不能用变更后的header跨身份命中。
任何失败都粘性poison并丢cached value；异步producer不得恢复ready，后续caller即使用空secret集合也拒绝。
cache只存固定失败category，每次构造新safe Error，不能用改写旧Error污染另一caller。
legacy/strict混用竞争原atomic take：最多一次成功转移/清理，失败方不使用legacy parsed值或inspector兜底。

共享API只返回短时Buffer，不cache raw text/bytes；host私有原文在finally释放，失败buffer清零，
E解析/扫描后在finally清零自身buffer。原观察器已有response原文可能含反射proof，B不新增request-body捕获或
延长browser保留，不向日志/产物输出它；JavaScript不可变字符串及框架缓存仍不承诺物理擦除。

固定check仅区分status、actor_header、cache_header、observed_body_read、observed_byte_binding、body_size、
body_bom、body_utf8、body_json、body_object、body_privacy。它表示失败发生的检查区间，不输出实际值，
不把post_binding名称解释成一定存在POST。旧run的具体失败分支仍未知；本变更修复已离线复现的inspector-copy依赖机制，
不改写旧失败结论，真实C与九件发布仍必须由新精确head CI证明。
