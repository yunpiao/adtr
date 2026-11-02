# 合成检测模板与手动任务（#259）

该切片使用显式模拟声明和不可变合成数据集，接通模板持久化、API、既有Worker任务引擎与页面。
规则字段/运算符闭集由 detectionrules 校验，不运行脚本、SQL、正则或真实AD操作。
事件字段映射和结果始终携带SIMULATED、契约、模板版本/hash、数据集fixture hash。
本切片不实现定时检测，不把旧health调度权限扩展成检测权限。

## 开关与授权

`ADTR_SIMULATION_DETECTION_ENABLED=true` 是显式入口/任务种类开关，默认关闭。
API和Worker都需要此开关才能提交并处理新种类；缺少API开关会拒绝入口，缺少Worker开关
不注册该kind，排队任务不会被假装执行成功。它在启动时读取，修改配置的部署不属于本次交付。
删除该边界会失去独立关闭未真实验收功能的能力；开启不会自动给任何角色授权或填充业务数据。
默认关闭时旧registry不变，旧kind全部委托原authorizer，不增加后台goroutine。

独立simulation_detection权限默认拒绝，包括内置管理员。服务端从当前会话确定actor/tenant，
再验证域资源授权；写操作检查Origin/CSRF。Worker在执行及成功发布时重新检查持久actor、
密码/禁用状态、当前功能/域授权和authorization epoch，不借用浏览器身份或health任务权限。
菜单提示来自login/me的可选字段，缺失为false；页面和API再次核验，提示不是权限边界。

## 模板与任务

模板create/update生成不可变版本。所有规则版本必须等于模板版本，scope/version/contract/
provenance由HTTP服务端构造；浏览器只提交有界声明字段。archive保留版本和审计。
expectedVersion是更新/归档必要的CAS值，防止并发覆盖；删除或错误版本将拒绝请求。
operationId是写入恢复必要的幂等标识，同ID同请求读取原回执，不能超时后换新ID盲重试。

手动运行固定模板ID/version/hash及dataset/hash/mappingVersion。后续编辑不能替换已入队版本。
复用任务引擎的owner scope、租约、heartbeat、fencing、取消和终态提交，不创建另一套运行时。
中间checkpoint只存空结果，当前授权复核成功才提交实际匹配结果。
完整事件上限1000、模板上限100、版本上限1024、结果上限60KiB；超限失败而不截断成功。
这些上限控制资源消耗，移除会扩大内存/CPU负担，不能作为真实生产容量承诺。

UI包含模板列表/声明编辑/归档、选择合成dataset、提交任务、查看状态/匹配结果及取消。
丢失写入响应保留原operationId并核对回执；重新进入后可用显示过的ID显式恢复。
身份、域、授权变化清除旧结果；响应必须绑定当前actor。选择控件有明确可访问名称。

## 验证边界

schema26由显式迁移原子创建；旧事件/风险API继续按当前schema运行，不能用26二进制服务旧数据库。
专用验收需实际启动API与Worker，验证模板版本固定、真实结果、重启、隔离、重放和浏览器。
排队取消在Worker启动前确定性验证；没有检测专属运行中取消竞态证明，不改旧SIGSTOP/guard机制。
本地无Docker的PG/browser阶段记not-run。独立复审、精确PR CI和新main验证仍为独立门禁。
真实AD/Windows和检测效力不由模拟匹配代替，产品验收仍0/209。
