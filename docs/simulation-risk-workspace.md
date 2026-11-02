# 合成风险工作台（#257）

本切片复用不可变合成事件数据集及 riskscore 模拟评分内核，提供持久重置账本、
HTTP、分布/趋势/变更记录和网页交互。真实AD威胁效力不在模拟权重证明范围。

映射是明确的业务假设：login_failure→low(5)，service_access→medium(15)，
login_success不增加风险。一个已存在且只有成功事件的subject可得0；未知subject返回未找到，
不能填0冒充已验证安全。数据集最多4096事件、每subject最多1024次重置，超限拒绝，不截断成功。
每次结果带simulation、契约、数据集及fixture来源，显示固定映射和预算。

`ADTR_SIMULATION_RISK_ENABLED=true` 是路由可用性的显式开关，默认关闭；仅隔离CI启用。
它不授予权限或导入数据。删除将失去独立关闭此尚未真实验收功能的能力；开启只提供受保护路由。
独立simulation_risk权限默认拒绝包括内置admin，schema25迁移不写现有角色授权数据。
读取必须经过真实会话与当前tenant/domain资源授权；重置还需写权限、Origin和CSRF。
login/me的simulationRiskReadable仅为菜单提示，旧响应无此字段默认为拒绝。

网页在授权域选择后指定合成数据集和subject。重置需要原因、expectedRevision与唯一operationId。
服务器提供操作者和时间，锁定subject，状态账本、审计和回执同一事务提交。
同操作ID同请求优先返回原回执，即使之后又发生重置，原回执仍稳定；不同请求复用ID冲突。
请求结果不确定时先读取原回执，核验原作用域后才同ID同payload重放，不换ID盲重试。

数据库显式迁移25；已有事件handler继续使用当前schema，事件菜单提示兼容24/25，未知未来版本拒绝。本版本二进制仍要求数据库迁移到25才提供业务API；提示兼容不等于用25二进制服务旧24数据库。
新风险suite真实执行PG/认证TCP/浏览器，旧事件suite也必须继续通过；本地没有Docker的阶段明确不算通过。
unit/DOM/编译、远程CI、合并及main验证分别记账，产品验收仍0/209。
