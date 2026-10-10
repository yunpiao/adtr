# ADTR 全量需求追溯

固定基线及状态解释见[方案入口](README.md)。本表逐项复制需求 ID、名称、优先级、原 Issue 和台账状态，只新增拟议导航映射，不修改需求范围或实现状态。字段/UI 明细与验收要求仍以 [catalog](../../requirements/catalog.json) 的 field_records/ui_records 和原 Issue 为准。

## 完整性

- 需求：209；唯一 ID：209；功能组：57；产品验收：0/209。
- 已验证切片 implemented_verified_slice：19；实施中 in_progress：26；未记录实现证据 not_started：164。
- 每个功能组的证据会继续变化；本表是该基线的快照，不能替代最新交付台账。优先级不替代依赖。

## 功能组与需求

### F01 AD 接入

| 需求 | 名称 | 顺序 | 台账状态 | 原 Issue |
| --- | --- | --- | --- | --- |
| AD-F-001 | 域连接列表与详情 | P0 | in_progress | [#16](https://github.com/yunpiao/adtr/issues/16) |
| AD-F-002 | 新增域连接 | P0 | in_progress | [#16](https://github.com/yunpiao/adtr/issues/16) |
| AD-F-003 | 编辑域连接 | P0 | in_progress | [#16](https://github.com/yunpiao/adtr/issues/16) |
| AD-F-004 | 删除域连接 | P0 | in_progress | [#16](https://github.com/yunpiao/adtr/issues/16) |
| AD-F-005 | 域连接测试 | P0 | in_progress | [#16](https://github.com/yunpiao/adtr/issues/16) |

### F02 AD 接入

| 需求 | 名称 | 顺序 | 台账状态 | 原 Issue |
| --- | --- | --- | --- | --- |
| AD-F-006 | 域与数据源选择 | P0 | in_progress | [#18](https://github.com/yunpiao/adtr/issues/18) |
| AD-F-007 | 域控查看与选择 | P0 | not_started | [#18](https://github.com/yunpiao/adtr/issues/18) |

### F03 AD 接入

| 需求 | 名称 | 顺序 | 台账状态 | 原 Issue |
| --- | --- | --- | --- | --- |
| AD-F-008 | 管理操作账户列表 | P0 | in_progress | [#17](https://github.com/yunpiao/adtr/issues/17) |
| AD-F-009 | 新增管理操作账户 | P0 | in_progress | [#17](https://github.com/yunpiao/adtr/issues/17) |
| AD-F-010 | 编辑管理操作账户 | P0 | in_progress | [#17](https://github.com/yunpiao/adtr/issues/17) |
| AD-F-011 | 删除管理操作账户 | P0 | in_progress | [#17](https://github.com/yunpiao/adtr/issues/17) |

### F04 AD 接入

| 需求 | 名称 | 顺序 | 台账状态 | 原 Issue |
| --- | --- | --- | --- | --- |
| AD-F-012 | 一键初始化域接入 | P0 | not_started | [#23](https://github.com/yunpiao/adtr/issues/23) |
| AD-F-013 | 域审计与组策略部署 | P0 | not_started | [#23](https://github.com/yunpiao/adtr/issues/23) |
| AD-F-014 | 对象审计配置部署 | P0 | not_started | [#23](https://github.com/yunpiao/adtr/issues/23) |

### F05 AD 接入

| 需求 | 名称 | 顺序 | 台账状态 | 原 Issue |
| --- | --- | --- | --- | --- |
| AD-F-015 | 域控采集器自动安装 | P0 | not_started | [#19](https://github.com/yunpiao/adtr/issues/19) |
| AD-F-017 | 下载采集器安装包 | P0 | not_started | [#19](https://github.com/yunpiao/adtr/issues/19) |

### F06 AD 接入

| 需求 | 名称 | 顺序 | 台账状态 | 原 Issue |
| --- | --- | --- | --- | --- |
| AD-F-016 | 采集器列表与详情 | P0 | not_started | [#20](https://github.com/yunpiao/adtr/issues/20) |
| AD-F-018 | 采集器注册 | P0 | not_started | [#20](https://github.com/yunpiao/adtr/issues/20) |
| AD-F-019 | 编辑采集器配置 | P0 | not_started | [#20](https://github.com/yunpiao/adtr/issues/20) |
| AD-F-020 | 删除采集器 | P0 | not_started | [#20](https://github.com/yunpiao/adtr/issues/20) |
| AD-F-021 | 采集器单个与批量升级 | P0 | not_started | [#20](https://github.com/yunpiao/adtr/issues/20) |
| AD-F-022 | 忽略与恢复运行异常提醒 | P0 | not_started | [#20](https://github.com/yunpiao/adtr/issues/20) |
| AD-F-027 | 采集器心跳与资源状态 | P0 | not_started | [#20](https://github.com/yunpiao/adtr/issues/20) |

### F07 AD 接入

| 需求 | 名称 | 顺序 | 台账状态 | 原 Issue |
| --- | --- | --- | --- | --- |
| AD-F-023 | 域控安全日志采集 | P0 | not_started | [#24](https://github.com/yunpiao/adtr/issues/24) |
| AD-F-025 | 日志与流量采集开关 | P0 | not_started | [#24](https://github.com/yunpiao/adtr/issues/24) |
| AD-F-026 | 采集运行详情查看 | P0 | not_started | [#24](https://github.com/yunpiao/adtr/issues/24) |

### F08 AD 接入

| 需求 | 名称 | 顺序 | 台账状态 | 原 Issue |
| --- | --- | --- | --- | --- |
| AD-F-024 | AD 网络流量采集 | P1 | not_started | [#27](https://github.com/yunpiao/adtr/issues/27) |

### F09 AD 接入

| 需求 | 名称 | 顺序 | 台账状态 | 原 Issue |
| --- | --- | --- | --- | --- |
| AD-F-028 | 采集进程管理与异常恢复 | P0 | not_started | [#21](https://github.com/yunpiao/adtr/issues/21) |
| AD-F-029 | 资源超限采集保护 | P0 | not_started | [#21](https://github.com/yunpiao/adtr/issues/21) |

### F10 AD 接入

| 需求 | 名称 | 顺序 | 台账状态 | 原 Issue |
| --- | --- | --- | --- | --- |
| AD-F-031 | 远程操作执行与结果查看 | P0 | not_started | [#22](https://github.com/yunpiao/adtr/issues/22) |
| AD-F-032 | 远程管理通道控制 | P0 | not_started | [#22](https://github.com/yunpiao/adtr/issues/22) |

### F11 AD 接入

| 需求 | 名称 | 顺序 | 台账状态 | 原 Issue |
| --- | --- | --- | --- | --- |
| AD-F-030 | 域控变化与采集配置同步 | P1 | not_started | [#28](https://github.com/yunpiao/adtr/issues/28) |
| AD-F-033 | 代理网络连接支持 | P1 | not_started | [#28](https://github.com/yunpiao/adtr/issues/28) |

### F12 目录资产

| 需求 | 名称 | 顺序 | 台账状态 | 原 Issue |
| --- | --- | --- | --- | --- |
| AD-F-034 | AD 资产全量同步 | P0 | in_progress | [#25](https://github.com/yunpiao/adtr/issues/25) |
| AD-F-035 | AD 资产增量同步 | P0 | not_started | [#25](https://github.com/yunpiao/adtr/issues/25) |
| AD-F-041 | 设备名称与地址同步 | P0 | not_started | [#25](https://github.com/yunpiao/adtr/issues/25) |

### F13 目录资产

| 需求 | 名称 | 顺序 | 台账状态 | 原 Issue |
| --- | --- | --- | --- | --- |
| AD-F-036 | 目录权限全量同步 | P1 | not_started | [#29](https://github.com/yunpiao/adtr/issues/29) |
| AD-F-037 | 目录权限变更同步 | P1 | not_started | [#29](https://github.com/yunpiao/adtr/issues/29) |
| AD-F-044 | 组成员与授权关系分析 | P1 | not_started | [#29](https://github.com/yunpiao/adtr/issues/29) |
| AD-F-054 | 目录对象与权限变更记录 | P1 | not_started | [#29](https://github.com/yunpiao/adtr/issues/29) |

### F14 目录资产

| 需求 | 名称 | 顺序 | 台账状态 | 原 Issue |
| --- | --- | --- | --- | --- |
| AD-F-038 | 用户资产列表与筛选 | P1 | not_started | [#30](https://github.com/yunpiao/adtr/issues/30) |
| AD-F-039 | 用户资产详情 | P1 | not_started | [#30](https://github.com/yunpiao/adtr/issues/30) |
| AD-F-040 | 计算机资产列表 | P1 | not_started | [#30](https://github.com/yunpiao/adtr/issues/30) |
| AD-F-042 | 组资产列表 | P1 | not_started | [#30](https://github.com/yunpiao/adtr/issues/30) |
| AD-F-043 | 所属组与成员查看 | P1 | not_started | [#30](https://github.com/yunpiao/adtr/issues/30) |
| AD-F-048 | 域信任关系查看 | P1 | not_started | [#30](https://github.com/yunpiao/adtr/issues/30) |
| AD-F-049 | 关联身份与设备查看 | P1 | not_started | [#30](https://github.com/yunpiao/adtr/issues/30) |
| AD-F-052 | 资产安全概览与分类统计 | P1 | not_started | [#30](https://github.com/yunpiao/adtr/issues/30) |
| AD-F-053 | 资产列表导出 | P1 | not_started | [#30](https://github.com/yunpiao/adtr/issues/30) |

### F15 目录资产

| 需求 | 名称 | 顺序 | 台账状态 | 原 Issue |
| --- | --- | --- | --- | --- |
| AD-F-045 | 敏感资产识别与标记 | P1 | not_started | [#38](https://github.com/yunpiao/adtr/issues/38) |
| AD-F-046 | 敏感资产标签查看 | P1 | not_started | [#38](https://github.com/yunpiao/adtr/issues/38) |
| AD-F-047 | 敏感资产变化告警 | P1 | not_started | [#38](https://github.com/yunpiao/adtr/issues/38) |

### F16 目录资产

| 需求 | 名称 | 顺序 | 台账状态 | 原 Issue |
| --- | --- | --- | --- | --- |
| AD-F-050 | 登录与服务访问关系分析 | P1 | not_started | [#48](https://github.com/yunpiao/adtr/issues/48) |
| AD-F-051 | 资产行为查看 | P1 | not_started | [#48](https://github.com/yunpiao/adtr/issues/48) |

### F17 目录资产

| 需求 | 名称 | 顺序 | 台账状态 | 原 Issue |
| --- | --- | --- | --- | --- |
| AD-F-055 | 风险事件分布 | P1 | not_started | [#39](https://github.com/yunpiao/adtr/issues/39) |
| AD-F-056 | 风险分数趋势 | P1 | not_started | [#39](https://github.com/yunpiao/adtr/issues/39) |
| AD-F-057 | 风险分数记录 | P1 | not_started | [#39](https://github.com/yunpiao/adtr/issues/39) |
| AD-F-058 | 风险分数重置 | P1 | not_started | [#39](https://github.com/yunpiao/adtr/issues/39) |
| AD-F-059 | 检测结果更新风险分数 | P1 | not_started | [#39](https://github.com/yunpiao/adtr/issues/39) |

### F18 检测与规则

| 需求 | 名称 | 顺序 | 台账状态 | 原 Issue |
| --- | --- | --- | --- | --- |
| AD-F-060 | 发起主动检测 | P1 | not_started | [#31](https://github.com/yunpiao/adtr/issues/31) |
| AD-F-061 | 单项复查 | P1 | not_started | [#31](https://github.com/yunpiao/adtr/issues/31) |
| AD-F-062 | 检测配置与上次结果查看 | P1 | not_started | [#31](https://github.com/yunpiao/adtr/issues/31) |
| AD-F-078 | 耗时检测能力查看 | P1 | not_started | [#31](https://github.com/yunpiao/adtr/issues/31) |
| AD-F-081 | 检测任务列表与详情 | P1 | not_started | [#31](https://github.com/yunpiao/adtr/issues/31) |
| AD-F-082 | 删除检测任务记录 | P1 | not_started | [#31](https://github.com/yunpiao/adtr/issues/31) |

### F19 检测与规则

| 需求 | 名称 | 顺序 | 台账状态 | 原 Issue |
| --- | --- | --- | --- | --- |
| AD-F-063 | AD 安全基线检测 | P1 | not_started | [#32](https://github.com/yunpiao/adtr/issues/32) |
| AD-F-064 | 基线结果列表与详情 | P1 | not_started | [#32](https://github.com/yunpiao/adtr/issues/32) |
| AD-F-065 | 基线结果导出 | P1 | not_started | [#32](https://github.com/yunpiao/adtr/issues/32) |

### F20 检测与规则

| 需求 | 名称 | 顺序 | 台账状态 | 原 Issue |
| --- | --- | --- | --- | --- |
| AD-F-066 | AD 漏洞检测 | P1 | not_started | [#33](https://github.com/yunpiao/adtr/issues/33) |
| AD-F-067 | 漏洞结果查看与导出 | P1 | not_started | [#33](https://github.com/yunpiao/adtr/issues/33) |

### F21 检测与规则

| 需求 | 名称 | 顺序 | 台账状态 | 原 Issue |
| --- | --- | --- | --- | --- |
| AD-F-083 | AD 行为与威胁检测 | P1 | not_started | [#37](https://github.com/yunpiao/adtr/issues/37) |
| AD-F-084 | 登录会话与身份关联 | P1 | not_started | [#37](https://github.com/yunpiao/adtr/issues/37) |
| AD-F-085 | 跨事件关联与时间窗口检测 | P1 | not_started | [#37](https://github.com/yunpiao/adtr/issues/37) |

### F22 检测与规则

| 需求 | 名称 | 顺序 | 台账状态 | 原 Issue |
| --- | --- | --- | --- | --- |
| AD-F-068 | 域用户弱口令检测 | P1 | not_started | [#34](https://github.com/yunpiao/adtr/issues/34) |
| AD-F-069 | 弱口令结果查看与导出 | P1 | not_started | [#34](https://github.com/yunpiao/adtr/issues/34) |
| AD-F-070 | 弱口令模板导出 | P1 | not_started | [#34](https://github.com/yunpiao/adtr/issues/34) |
| AD-F-071 | 弱口令检测数据采集 | P1 | not_started | [#34](https://github.com/yunpiao/adtr/issues/34) |

### F23 检测与规则

| 需求 | 名称 | 顺序 | 台账状态 | 原 Issue |
| --- | --- | --- | --- | --- |
| AD-F-072 | 用户密码泄露检测 | P1 | not_started | [#35](https://github.com/yunpiao/adtr/issues/35) |
| AD-F-073 | 用户邮箱泄露检查 | P1 | not_started | [#35](https://github.com/yunpiao/adtr/issues/35) |

### F24 检测与规则

| 需求 | 名称 | 顺序 | 台账状态 | 原 Issue |
| --- | --- | --- | --- | --- |
| AD-F-086 | 规则列表、筛选与详情 | P1 | not_started | [#36](https://github.com/yunpiao/adtr/issues/36) |
| AD-F-087 | 规则新增、编辑与删除 | P1 | not_started | [#36](https://github.com/yunpiao/adtr/issues/36) |
| AD-F-088 | 规则测试 | P1 | not_started | [#36](https://github.com/yunpiao/adtr/issues/36) |
| AD-F-089 | 规则激活 | P1 | not_started | [#36](https://github.com/yunpiao/adtr/issues/36) |
| AD-F-090 | 规则导出 | P1 | not_started | [#36](https://github.com/yunpiao/adtr/issues/36) |
| AD-F-091 | 告警与行为规则列表和详情 | P1 | not_started | [#36](https://github.com/yunpiao/adtr/issues/36) |
| AD-F-092 | 告警与行为检测配置编辑 | P1 | not_started | [#36](https://github.com/yunpiao/adtr/issues/36) |
| AD-F-093 | 规则启用与停用 | P1 | not_started | [#36](https://github.com/yunpiao/adtr/issues/36) |
| AD-F-094 | 删除检测配置 | P1 | not_started | [#36](https://github.com/yunpiao/adtr/issues/36) |

### F25 检测与规则

| 需求 | 名称 | 顺序 | 台账状态 | 原 Issue |
| --- | --- | --- | --- | --- |
| AD-F-096 | 学习型检测配置管理 | P2 | not_started | [#57](https://github.com/yunpiao/adtr/issues/57) |

### F26 检测与规则

| 需求 | 名称 | 顺序 | 台账状态 | 原 Issue |
| --- | --- | --- | --- | --- |
| AD-F-097 | 在界面新建规则 | P1 | not_started | [#40](https://github.com/yunpiao/adtr/issues/40) |
| AD-F-098 | 在界面编辑规则 | P1 | not_started | [#40](https://github.com/yunpiao/adtr/issues/40) |
| AD-F-099 | 规则条件配置 | P1 | not_started | [#40](https://github.com/yunpiao/adtr/issues/40) |
| AD-F-100 | 根据界面配置生成规则 | P1 | not_started | [#40](https://github.com/yunpiao/adtr/issues/40) |
| AD-F-101 | 规则保存与生效管理 | P1 | not_started | [#40](https://github.com/yunpiao/adtr/issues/40) |
| AD-F-102 | 规则配置检查与测试 | P1 | not_started | [#40](https://github.com/yunpiao/adtr/issues/40) |

### F27 检测与规则

| 需求 | 名称 | 顺序 | 台账状态 | 原 Issue |
| --- | --- | --- | --- | --- |
| AD-F-074 | 检测模板列表与详情 | P1 | not_started | [#41](https://github.com/yunpiao/adtr/issues/41) |
| AD-F-075 | 新增检测模板 | P1 | not_started | [#41](https://github.com/yunpiao/adtr/issues/41) |
| AD-F-076 | 编辑检测模板 | P1 | not_started | [#41](https://github.com/yunpiao/adtr/issues/41) |
| AD-F-077 | 删除检测模板 | P1 | not_started | [#41](https://github.com/yunpiao/adtr/issues/41) |
| AD-F-079 | 定时检测配置列表 | P1 | not_started | [#41](https://github.com/yunpiao/adtr/issues/41) |
| AD-F-080 | 启用与编辑定时检测 | P1 | not_started | [#41](https://github.com/yunpiao/adtr/issues/41) |

### F28 检测与规则

| 需求 | 名称 | 顺序 | 台账状态 | 原 Issue |
| --- | --- | --- | --- | --- |
| AD-F-103 | 规则白名单列表与详情 | P1 | not_started | [#43](https://github.com/yunpiao/adtr/issues/43) |
| AD-F-104 | 新增规则白名单 | P1 | not_started | [#43](https://github.com/yunpiao/adtr/issues/43) |
| AD-F-105 | 编辑与删除规则白名单 | P1 | not_started | [#43](https://github.com/yunpiao/adtr/issues/43) |
| AD-F-106 | 从事件创建白名单 | P1 | not_started | [#43](https://github.com/yunpiao/adtr/issues/43) |
| AD-F-107 | 白名单导入与导出 | P1 | not_started | [#43](https://github.com/yunpiao/adtr/issues/43) |
| AD-F-108 | 全局白名单列表与筛选 | P1 | not_started | [#43](https://github.com/yunpiao/adtr/issues/43) |
| AD-F-109 | 新增全局白名单 | P1 | not_started | [#43](https://github.com/yunpiao/adtr/issues/43) |
| AD-F-110 | 编辑与删除全局白名单 | P1 | not_started | [#43](https://github.com/yunpiao/adtr/issues/43) |

### F29 威胁调查

| 需求 | 名称 | 顺序 | 台账状态 | 原 Issue |
| --- | --- | --- | --- | --- |
| AD-F-111 | 威胁事件列表与筛选 | P1 | not_started | [#42](https://github.com/yunpiao/adtr/issues/42) |
| AD-F-112 | 威胁事件详情与状态 | P1 | not_started | [#42](https://github.com/yunpiao/adtr/issues/42) |
| AD-F-113 | 事件证据查看 | P1 | not_started | [#42](https://github.com/yunpiao/adtr/issues/42) |
| AD-F-114 | 原始日志与检测原因查看 | P1 | not_started | [#42](https://github.com/yunpiao/adtr/issues/42) |
| AD-F-115 | 关闭威胁事件 | P1 | not_started | [#42](https://github.com/yunpiao/adtr/issues/42) |
| AD-F-116 | 威胁事件标记 | P1 | not_started | [#42](https://github.com/yunpiao/adtr/issues/42) |
| AD-F-119 | 历史攻击趋势 | P1 | not_started | [#42](https://github.com/yunpiao/adtr/issues/42) |
| AD-F-120 | 事件与证据导出 | P1 | not_started | [#42](https://github.com/yunpiao/adtr/issues/42) |

### F30 威胁调查

| 需求 | 名称 | 顺序 | 台账状态 | 原 Issue |
| --- | --- | --- | --- | --- |
| AD-F-095 | 攻击路径规则信息查看 | P1 | not_started | [#44](https://github.com/yunpiao/adtr/issues/44) |
| AD-F-117 | 攻击路径查看 | P1 | not_started | [#44](https://github.com/yunpiao/adtr/issues/44) |
| AD-F-118 | 攻击路径导出 | P1 | not_started | [#44](https://github.com/yunpiao/adtr/issues/44) |

### F31 响应处置

| 需求 | 名称 | 顺序 | 台账状态 | 原 Issue |
| --- | --- | --- | --- | --- |
| AD-F-121 | 阻断策略列表与详情 | P1 | not_started | [#46](https://github.com/yunpiao/adtr/issues/46) |
| AD-F-122 | 新增阻断策略 | P1 | not_started | [#46](https://github.com/yunpiao/adtr/issues/46) |
| AD-F-123 | 立即执行阻断 | P1 | not_started | [#46](https://github.com/yunpiao/adtr/issues/46) |
| AD-F-124 | 删除阻断策略 | P1 | not_started | [#46](https://github.com/yunpiao/adtr/issues/46) |
| AD-F-125 | 恢复用户或设备状态 | P1 | not_started | [#46](https://github.com/yunpiao/adtr/issues/46) |
| AD-F-126 | 告警关联阻断执行 | P1 | not_started | [#46](https://github.com/yunpiao/adtr/issues/46) |

### F32 响应处置

| 需求 | 名称 | 顺序 | 台账状态 | 原 Issue |
| --- | --- | --- | --- | --- |
| AD-F-127 | 阻断白名单列表与详情 | P1 | not_started | [#45](https://github.com/yunpiao/adtr/issues/45) |
| AD-F-128 | 新增阻断白名单 | P1 | not_started | [#45](https://github.com/yunpiao/adtr/issues/45) |
| AD-F-129 | 编辑与删除阻断白名单 | P1 | not_started | [#45](https://github.com/yunpiao/adtr/issues/45) |

### F33 威胁调查

| 需求 | 名称 | 顺序 | 台账状态 | 原 Issue |
| --- | --- | --- | --- | --- |
| AD-F-130 | 行为事件列表与筛选 | P1 | not_started | [#47](https://github.com/yunpiao/adtr/issues/47) |
| AD-F-131 | 行为事件详情 | P1 | not_started | [#47](https://github.com/yunpiao/adtr/issues/47) |
| AD-F-132 | 设备认证活动查看 | P1 | not_started | [#47](https://github.com/yunpiao/adtr/issues/47) |
| AD-F-133 | 行为事件导出 | P1 | not_started | [#47](https://github.com/yunpiao/adtr/issues/47) |

### F34 威胁调查

| 需求 | 名称 | 顺序 | 台账状态 | 原 Issue |
| --- | --- | --- | --- | --- |
| AD-F-134 | 查询模板列表 | P2 | not_started | [#58](https://github.com/yunpiao/adtr/issues/58) |
| AD-F-135 | 新增与删除查询模板 | P2 | not_started | [#58](https://github.com/yunpiao/adtr/issues/58) |
| AD-F-136 | 行为数据保留期限查看 | P2 | not_started | [#58](https://github.com/yunpiao/adtr/issues/58) |
| AD-F-137 | 高级数据检索 | P2 | not_started | [#58](https://github.com/yunpiao/adtr/issues/58) |

### F35 威胁调查

| 需求 | 名称 | 顺序 | 台账状态 | 原 Issue |
| --- | --- | --- | --- | --- |
| AD-F-138 | 蜜罐与特权账户列表 | P2 | not_started | [#59](https://github.com/yunpiao/adtr/issues/59) |
| AD-F-139 | 添加与删除蜜罐或特权配置 | P2 | not_started | [#59](https://github.com/yunpiao/adtr/issues/59) |
| AD-F-140 | 账户可用性检查 | P2 | not_started | [#59](https://github.com/yunpiao/adtr/issues/59) |
| AD-F-141 | 蜜罐账户属性设置 | P2 | not_started | [#59](https://github.com/yunpiao/adtr/issues/59) |

### F36 安全总览

| 需求 | 名称 | 顺序 | 台账状态 | 原 Issue |
| --- | --- | --- | --- | --- |
| AD-F-142 | 资产与接入健康概览 | P2 | not_started | [#60](https://github.com/yunpiao/adtr/issues/60) |
| AD-F-143 | 威胁风险趋势 | P2 | not_started | [#60](https://github.com/yunpiao/adtr/issues/60) |
| AD-F-144 | 待处理威胁概览 | P2 | not_started | [#60](https://github.com/yunpiao/adtr/issues/60) |
| AD-F-145 | 事件处理速率展示 | P2 | not_started | [#60](https://github.com/yunpiao/adtr/issues/60) |
| AD-F-146 | 最近检测结果概览 | P2 | not_started | [#60](https://github.com/yunpiao/adtr/issues/60) |
| AD-F-147 | 攻击目标与阻断统计 | P2 | not_started | [#60](https://github.com/yunpiao/adtr/issues/60) |
| AD-F-148 | 攻击次数与高危事件热度 | P2 | not_started | [#60](https://github.com/yunpiao/adtr/issues/60) |

### F37 报告与导出

| 需求 | 名称 | 顺序 | 台账状态 | 原 Issue |
| --- | --- | --- | --- | --- |
| AD-F-149 | 安全评估与加固报告 | P1 | not_started | [#49](https://github.com/yunpiao/adtr/issues/49) |
| AD-F-150 | 事件与巡检报告 | P1 | not_started | [#49](https://github.com/yunpiao/adtr/issues/49) |

### F38 报告与导出

| 需求 | 名称 | 顺序 | 台账状态 | 原 Issue |
| --- | --- | --- | --- | --- |
| AD-F-151 | 导出任务列表与进度 | P0 | in_progress | [#26](https://github.com/yunpiao/adtr/issues/26) |
| AD-F-152 | 导出文件下载 | P0 | in_progress | [#26](https://github.com/yunpiao/adtr/issues/26) |

### F39 平台管理

| 需求 | 名称 | 顺序 | 台账状态 | 原 Issue |
| --- | --- | --- | --- | --- |
| AD-F-153 | 平台操作记录 | P0 | in_progress | [#13](https://github.com/yunpiao/adtr/issues/13) |
| AD-F-154 | 操作审计列表与筛选 | P0 | in_progress | [#13](https://github.com/yunpiao/adtr/issues/13) |
| AD-F-155 | 操作审计导出 | P0 | in_progress | [#13](https://github.com/yunpiao/adtr/issues/13) |
| AD-F-156 | 操作审计记录删除 | P0 | in_progress | [#13](https://github.com/yunpiao/adtr/issues/13) |

### F40 任务与消息

| 需求 | 名称 | 顺序 | 台账状态 | 原 Issue |
| --- | --- | --- | --- | --- |
| AD-F-157 | 消息列表与时间线 | P1 | not_started | [#50](https://github.com/yunpiao/adtr/issues/50) |
| AD-F-158 | 消息已读管理与定位 | P1 | not_started | [#50](https://github.com/yunpiao/adtr/issues/50) |
| AD-F-159 | 消息数量统计 | P1 | not_started | [#50](https://github.com/yunpiao/adtr/issues/50) |

### F41 任务与消息

| 需求 | 名称 | 顺序 | 台账状态 | 原 Issue |
| --- | --- | --- | --- | --- |
| AD-F-160 | 通知配置列表与详情 | P1 | not_started | [#51](https://github.com/yunpiao/adtr/issues/51) |
| AD-F-161 | 新增、编辑与删除通知配置 | P1 | not_started | [#51](https://github.com/yunpiao/adtr/issues/51) |
| AD-F-162 | 通知规则与检测模板关联 | P1 | not_started | [#51](https://github.com/yunpiao/adtr/issues/51) |
| AD-F-163 | 通知发送与测试 | P1 | not_started | [#51](https://github.com/yunpiao/adtr/issues/51) |
| AD-F-164 | 延迟与定时通知 | P1 | not_started | [#51](https://github.com/yunpiao/adtr/issues/51) |
| AD-F-165 | 通知接收人列表与详情 | P1 | not_started | [#51](https://github.com/yunpiao/adtr/issues/51) |
| AD-F-166 | 新增、编辑与删除接收人 | P1 | not_started | [#51](https://github.com/yunpiao/adtr/issues/51) |

### F42 平台管理

| 需求 | 名称 | 顺序 | 台账状态 | 原 Issue |
| --- | --- | --- | --- | --- |
| AD-F-170 | 个人资料与头像 | P2 | in_progress | [#61](https://github.com/yunpiao/adtr/issues/61) |

### F43 平台管理

| 需求 | 名称 | 顺序 | 台账状态 | 原 Issue |
| --- | --- | --- | --- | --- |
| AD-F-167 | 登录与退出 | P0 | implemented_verified_slice | [#7](https://github.com/yunpiao/adtr/issues/7) |
| AD-F-168 | 首次登录与密码过期处理 | P0 | implemented_verified_slice | [#7](https://github.com/yunpiao/adtr/issues/7) |
| AD-F-173 | 密码修改与重置 | P0 | implemented_verified_slice | [#7](https://github.com/yunpiao/adtr/issues/7) |

### F44 平台管理

| 需求 | 名称 | 顺序 | 台账状态 | 原 Issue |
| --- | --- | --- | --- | --- |
| AD-F-169 | 多因素认证管理 | P0 | implemented_verified_slice | [#8](https://github.com/yunpiao/adtr/issues/8) |

### F45 平台管理

| 需求 | 名称 | 顺序 | 台账状态 | 原 Issue |
| --- | --- | --- | --- | --- |
| AD-F-171 | 用户列表 | P0 | implemented_verified_slice | [#9](https://github.com/yunpiao/adtr/issues/9) |
| AD-F-172 | 新增、编辑与删除用户 | P0 | implemented_verified_slice | [#9](https://github.com/yunpiao/adtr/issues/9) |
| AD-F-174 | 角色列表与详情 | P0 | implemented_verified_slice | [#9](https://github.com/yunpiao/adtr/issues/9) |
| AD-F-175 | 角色保存与删除 | P0 | implemented_verified_slice | [#9](https://github.com/yunpiao/adtr/issues/9) |
| AD-F-176 | 用户角色单个与批量分配 | P0 | implemented_verified_slice | [#9](https://github.com/yunpiao/adtr/issues/9) |

### F46 平台管理

| 需求 | 名称 | 顺序 | 台账状态 | 原 Issue |
| --- | --- | --- | --- | --- |
| AD-F-177 | 权限配置管理 | P0 | implemented_verified_slice | [#10](https://github.com/yunpiao/adtr/issues/10) |
| AD-F-178 | 菜单、页面及操作权限控制 | P0 | implemented_verified_slice | [#10](https://github.com/yunpiao/adtr/issues/10) |

### F47 平台管理

| 需求 | 名称 | 顺序 | 台账状态 | 原 Issue |
| --- | --- | --- | --- | --- |
| AD-F-179 | AD 数据资源访问控制 | P0 | implemented_verified_slice | [#11](https://github.com/yunpiao/adtr/issues/11) |
| AD-F-180 | 资源组列表与详情 | P0 | implemented_verified_slice | [#11](https://github.com/yunpiao/adtr/issues/11) |
| AD-F-181 | 新增、编辑与删除资源组 | P0 | implemented_verified_slice | [#11](https://github.com/yunpiao/adtr/issues/11) |
| AD-F-182 | 角色与资源组关联 | P0 | implemented_verified_slice | [#11](https://github.com/yunpiao/adtr/issues/11) |
| AD-F-183 | 租户配置 | P0 | implemented_verified_slice | [#11](https://github.com/yunpiao/adtr/issues/11) |

### F48 任务与消息

| 需求 | 名称 | 顺序 | 台账状态 | 原 Issue |
| --- | --- | --- | --- | --- |
| AD-F-189 | 后台任务状态查看 | P0 | implemented_verified_slice | [#12](https://github.com/yunpiao/adtr/issues/12) |
| AD-F-191 | 跨域任务并行与失败隔离 | P0 | implemented_verified_slice | [#12](https://github.com/yunpiao/adtr/issues/12) |
| AD-F-192 | 重复任务控制 | P0 | implemented_verified_slice | [#12](https://github.com/yunpiao/adtr/issues/12) |

### F49 任务与消息

| 需求 | 名称 | 顺序 | 台账状态 | 原 Issue |
| --- | --- | --- | --- | --- |
| AD-F-190 | 周期任务调度 | P0 | in_progress | [#15](https://github.com/yunpiao/adtr/issues/15) |
| AD-F-193 | 过期任务状态与消息清理 | P0 | in_progress | [#15](https://github.com/yunpiao/adtr/issues/15) |

### F50 任务与消息

| 需求 | 名称 | 顺序 | 台账状态 | 原 Issue |
| --- | --- | --- | --- | --- |
| AD-F-194 | 域连接与采集器异常通知 | P1 | not_started | [#52](https://github.com/yunpiao/adtr/issues/52) |
| AD-F-195 | 检测与同步运行健康查看 | P1 | not_started | [#52](https://github.com/yunpiao/adtr/issues/52) |

### F51 平台管理

| 需求 | 名称 | 顺序 | 台账状态 | 原 Issue |
| --- | --- | --- | --- | --- |
| AD-F-184 | 第三方访问授权 | P2 | not_started | [#62](https://github.com/yunpiao/adtr/issues/62) |

### F52 平台管理

| 需求 | 名称 | 顺序 | 台账状态 | 原 Issue |
| --- | --- | --- | --- | --- |
| AD-F-196 | 系统信息查看 | P0 | in_progress | [#14](https://github.com/yunpiao/adtr/issues/14) |
| AD-F-199 | 存储管理 | P0 | in_progress | [#14](https://github.com/yunpiao/adtr/issues/14) |
| AD-F-200 | 系统资源监控 | P0 | in_progress | [#14](https://github.com/yunpiao/adtr/issues/14) |
| AD-F-201 | 服务与运行状态查看 | P0 | in_progress | [#14](https://github.com/yunpiao/adtr/issues/14) |
| AD-F-206 | 平台与依赖健康查看 | P0 | in_progress | [#14](https://github.com/yunpiao/adtr/issues/14) |

### F53 平台管理

| 需求 | 名称 | 顺序 | 台账状态 | 原 Issue |
| --- | --- | --- | --- | --- |
| AD-F-185 | 许可信息与有效期查看 | P1 | not_started | [#53](https://github.com/yunpiao/adtr/issues/53) |
| AD-F-186 | 许可更新与激活 | P1 | not_started | [#53](https://github.com/yunpiao/adtr/issues/53) |
| AD-F-187 | 授权预览 | P1 | not_started | [#53](https://github.com/yunpiao/adtr/issues/53) |
| AD-F-188 | 设备绑定与授权校验 | P1 | not_started | [#53](https://github.com/yunpiao/adtr/issues/53) |

### F54 平台管理

| 需求 | 名称 | 顺序 | 台账状态 | 原 Issue |
| --- | --- | --- | --- | --- |
| AD-F-197 | 系统标识设置 | P2 | not_started | [#63](https://github.com/yunpiao/adtr/issues/63) |
| AD-F-198 | 系统时间与同步设置 | P2 | not_started | [#63](https://github.com/yunpiao/adtr/issues/63) |
| AD-F-208 | 平台管理项配置 | P2 | not_started | [#63](https://github.com/yunpiao/adtr/issues/63) |

### F55 平台管理

| 需求 | 名称 | 顺序 | 台账状态 | 原 Issue |
| --- | --- | --- | --- | --- |
| AD-F-202 | 服务启动、停止与重启 | P1 | not_started | [#54](https://github.com/yunpiao/adtr/issues/54) |
| AD-F-203 | 平台重启与恢复状态查看 | P1 | not_started | [#54](https://github.com/yunpiao/adtr/issues/54) |
| AD-F-207 | 运维登录与退出 | P1 | not_started | [#54](https://github.com/yunpiao/adtr/issues/54) |

### F56 平台管理

| 需求 | 名称 | 顺序 | 台账状态 | 原 Issue |
| --- | --- | --- | --- | --- |
| AD-F-204 | 网络诊断 | P1 | not_started | [#55](https://github.com/yunpiao/adtr/issues/55) |
| AD-F-205 | 平台日志查询、打包与下载 | P1 | in_progress | [#55](https://github.com/yunpiao/adtr/issues/55) |

### F57 平台管理

| 需求 | 名称 | 顺序 | 台账状态 | 原 Issue |
| --- | --- | --- | --- | --- |
| AD-F-209 | AD 平台安装与部署 | P1 | not_started | [#56](https://github.com/yunpiao/adtr/issues/56) |

## 证据读取方式

1. 从需求 ID 找到 catalog 中该项的 evidence、verified、remaining、local_contract 及 runtime_suites。
2. 读取原 Issue 的源字段、UI、异常与验收清单；共享控件引用不表示每个页面已出现该控件。
3. 逐项确认精确提交的真实套件覆盖何种切片；不把同一套件的通过外推到未覆盖字段。
4. 对照总体方案的阶段门禁，真实 AD/Windows、算法输入、容量和生产验收未完成时不能改为 accepted。
