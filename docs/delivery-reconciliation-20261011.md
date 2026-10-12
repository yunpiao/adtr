# 2026-10-11 交付台账核对

## 核对口径

核对基线为 main `6f7f44a4997f78b6a50eab1ba657ffcc15769c1a`（schema18）。本次仅更新文档与机器台账；不新增功能、改变权限/开关/CI、不部署、不关闭任何 Issue。

全部209项、57组、3,258字段记录、804控件映射及源工作簿摘要保持。只有 AD-F-157/158/159 从 `not_started` 改为 `in_progress`：它们已有真实 health 工程实现，仍缺完整F40范围。结果为 **19 implemented_verified_slice / 31 in_progress / 159 not_started / 0 accepted**。19是既有有界切片状态的需求数量，不是19个完整产品功能，更不按PR数递增。

`in_progress` 表示已有实施进展但完整需求尚未验收；`implemented_verified_slice` 表示该记录的具名可运行切片和测试已经存在，剩余兼容/发布门禁仍列明；`accepted` 才表示全部验收且无remaining。本次没有新增后两种状态。

数据来源是下列PR的当前状态/正文、关联Issue原始范围，以及GitHub Actions的精确提交运行/最新job结果。PR均已merged；本次核对没有重跑这些历史测试或重新做像素审查，独立代码/像素审查引用各PR原记录。当前基线的main-push [38154440236](https://github.com/yunpiao/adtr/actions/runs/38154440236) attempt1逐项36/36成功；这是基线证据，不能替代本次文档提交自己的CI。

## 精确发布与运行链

每行分别绑定PR head与实际merge commit；运行状态均为completed/success。a为最终run attempt；历史失败仍存在，重试成功不表示原故障原因已被修复。 已从基线Git对象逐项核对14个合并提交：均为该基线祖先，第二父提交等于所列PR head，merge内容树与head相同。

| PR / 工程切片 | PR head → 精确CI | 实际merge → main-push CI | 最终job证据 |
| --- | --- | --- | --- |
| [#81](https://github.com/yunpiao/adtr/pull/81) F12/AD-F-034：schema16、独立v2任务/授权/账本、四类补充事实与API/UI；v1保留 | `d6b6063f5fa5407779f465789e652a3dc24ff7c2` → [37945689472](https://github.com/yunpiao/adtr/actions/runs/37945689472) a1 | `4faafb552a5c7887a62930f7563b22d3cbcdfbf9` → [37954766457](https://github.com/yunpiao/adtr/actions/runs/37954766457) a1 | PR 28/28，main 28/28 |
| [#92](https://github.com/yunpiao/adtr/pull/92) F14/AD-F-038/039：真实SOC导航、用户队列和固定观测详情；不借用原型风险/关系数据 | `8a0da79da17fea1807a5af2e11035e3110a07e58` → [38060682440](https://github.com/yunpiao/adtr/actions/runs/38060682440) a1 | `a8cc62aab884292e2747704968a7f9fe988c719a` → [38065832832](https://github.com/yunpiao/adtr/actions/runs/38065832832) a2 | PR 34/34，main 34/34 |
| [#94](https://github.com/yunpiao/adtr/pull/94) F48/AD-F-189：真实任务队列、执行详情、持久事件时间线及安全导航恢复 | `96cf35f5f3911c8d5df8ba36b1a16e49f0b72b75` → [38076290003](https://github.com/yunpiao/adtr/actions/runs/38076290003) a2 | `ebf6ce321b8d8e99a31c3b3ea7251352389d902c` → [38081686522](https://github.com/yunpiao/adtr/actions/runs/38081686522) a2 | PR 34/34；main见下方重复记录说明 |
| [#97](https://github.com/yunpiao/adtr/pull/97) 工程#96：同运行固定PG镜像验证/共享、npm缓存与四路浏览器并行；不新增产品能力 | `a9f4abba18108341419756a39b5a1bec451cac87` → [38083991882](https://github.com/yunpiao/adtr/actions/runs/38083991882) a1 | `d847fd099bc2301cb64c972bb6b54fee39989e56` → [38087037122](https://github.com/yunpiao/adtr/actions/runs/38087037122) a1 | PR 35/35，main 35/35 |
| [#98](https://github.com/yunpiao/adtr/pull/98) F39/AD-F-154：真实授权审计查询、字段展开、原XLSX/隐藏恢复边界 | `3c2a304fb18646df8ab09fc36454378bc82732a7` → [38087480541](https://github.com/yunpiao/adtr/actions/runs/38087480541) a1 | `f9b8ed1254c0ac0fd9183c0f0625995bec975218` → [38090139952](https://github.com/yunpiao/adtr/actions/runs/38090139952) a1 | PR 35/35，main 35/35 |
| [#102](https://github.com/yunpiao/adtr/pull/102) F52/AD-F-196/199/200/201/206：local-api健康/历史、阈值CAS和Worker暂停恢复 | `4e65d9924f0fd6e57e8b17e0e895f1dd5612e7a1` → [38110796016](https://github.com/yunpiao/adtr/actions/runs/38110796016) a1 | `48645c9d57f676dedec437b4640f1cf5f1092087` → [38113165795](https://github.com/yunpiao/adtr/actions/runs/38113165795) a2 | PR 35/35，main 35/35 |
| [#103](https://github.com/yunpiao/adtr/pull/103) F01/AD-F-001–005：域诊断SOC工作区；B2真实跨账户取消隔离/原收据恢复 | `bba2aedb9bf47f5fae5f35f487de1e84f90cfb7c` → [38113420283](https://github.com/yunpiao/adtr/actions/runs/38113420283) a1 | `d45e361ca315b7eeac41a2cb2f4f4ad38e15f9e2` → [38116147392](https://github.com/yunpiao/adtr/actions/runs/38116147392) a1 | PR 35/35，main 35/35 |
| [#104](https://github.com/yunpiao/adtr/pull/104) F40/AD-F-157/158/159：schema17真实health投影、授权快照/定位/计数、持久单条回执 | `72328c3a9dfc5ac9e659fdece3e9b02498d70847` → [38116506245](https://github.com/yunpiao/adtr/actions/runs/38116506245) a1 | `7913321590acea3d61e696bd251bd30e75e04f64` → [38118798995](https://github.com/yunpiao/adtr/actions/runs/38118798995) a2 | PR 35/35，main 35/35 |
| [#106](https://github.com/yunpiao/adtr/pull/106) F40/AD-F-157/159：真实只读消息工作区与health→Worker→outbox→UI链 | `184ec9496385f3d1809c3a75ec0b7e5cb907e4ba` → [38118978767](https://github.com/yunpiao/adtr/actions/runs/38118978767) a2 | `4aa9eedbfa0061c731031b749b350238a8337e11` → [38122183907](https://github.com/yunpiao/adtr/actions/runs/38122183907) a1 | PR 36/36，main 36/36 |
| [#109](https://github.com/yunpiao/adtr/pull/109) F40/AD-F-158/159：单条已读UI、原标识未知提交恢复，旧快照不改 | `74131eaacee8248a604b95c64c905ba764d2551d` → [38125601786](https://github.com/yunpiao/adtr/actions/runs/38125601786) a1 | `9f42ab315927323228c0f85d22d160f36f7b6034` → [38128107552](https://github.com/yunpiao/adtr/actions/runs/38128107552) a1 | PR 36/36，main 36/36 |
| [#110](https://github.com/yunpiao/adtr/pull/110) F01/F03：双通知失效回退、无域B与真实正文消费后的迟到应用交付；仅测试增量 | `e55616de2e0f0966c60bbf774394c3936c42bb1d` → [38136383831](https://github.com/yunpiao/adtr/actions/runs/38136383831) a1 | `77a6e2140eaa69e82d2d3a6e306c95fcf8bcb1d7` → [38139103844](https://github.com/yunpiao/adtr/actions/runs/38139103844) a1 | PR 36/36，main 36/36 |
| [#113](https://github.com/yunpiao/adtr/pull/113) F40/AD-F-158：schema18有界批量目标/进度/取消、授权epoch及真实PG故障恢复 | `2f87f066ca742cf7a5b2e5bf2b1634c7db167b3a` → [38143870092](https://github.com/yunpiao/adtr/actions/runs/38143870092) a1 | `acaa1e9da6f5c37c63c307a17961a0dc753db8cc` → [38146859628](https://github.com/yunpiao/adtr/actions/runs/38146859628) a1 | PR 36/36，main 36/36 |
| [#114](https://github.com/yunpiao/adtr/pull/114) F14/AD-F-038：精确SAM、whenCreated半开范围/排序；明确新增契约，不冒称旧系统语义 | `e79dec473f54ea4a8cb06a87e605180fde98d5a8` → [38147564340](https://github.com/yunpiao/adtr/actions/runs/38147564340) a1 | `de878307379aba995545c6091eb4a0196fc6ba90` → [38150511380](https://github.com/yunpiao/adtr/actions/runs/38150511380) a1 | PR 36/36，main 36/36 |
| [#116](https://github.com/yunpiao/adtr/pull/116) F40/AD-F-158：跨页/固定结果all、原操作恢复、六项进度/取消余项及真实界面 | `48eb9a3ab9c2df400454f1fe060af68e4f53b47a` → [38151288536](https://github.com/yunpiao/adtr/actions/runs/38151288536) a1 | `6f7f44a4997f78b6a50eab1ba657ffcc15769c1a` → [38154440236](https://github.com/yunpiao/adtr/actions/runs/38154440236) a1 | PR 36/36，main 36/36 |

PR94历史main运行38081686522的latest-jobs接口返回33个success及一个同名queued/null记录；attempt2明细则有34个不同job名称的成功记录，并额外保留两条operational-logs queued/null。实际成功的 [operational-logs job114314085630](https://github.com/yunpiao/adtr/actions/runs/38081686522/job/114314085630) 与 [verify job114314888353](https://github.com/yunpiao/adtr/actions/runs/38081686522/job/114314888353) 可直接核对，run为success。此处记录API结果差异，不把null改写成成功，也不把该历史异常推断成当前main故障。其余表列运行的latest-jobs均逐项success。

## 已交付切片与仍缺范围

### F12目录与F14用户资产

- AD-F-034保持in_progress。PR81已实际注册v2生产者、安装schema16、接入持久化/API/UI；旧“v2未注册”不再作为当前remaining。dictionary2四类新增事实为SID/mail/description/whenCreated，不能外推为所有资产字段
- AD-F-038/039保持in_progress。PR92的真实用户工作区/固定详情和PR114的精确SAM/创建时间范围、排序已通过各自head与main门禁；旧“精确候选CI待完成”由具名运行替换
- 旧字段/筛选枚举与完整源兼容仍缺；组/计算机业务页面、关系/风险/登录、资产导出、增量/缺席删除/IP关系、容量及真实AD/八版本Windows各自开放，不由用户切片借证
- 两个directory部署开关默认关闭；启用v2必须同时满足两开关和独立当前v2用途授权及密钥/CA/出口策略。开关限制未授权环境中的目录采集；关闭或缺配置会拒绝/禁用对应链，启用会产生实际LDAP读取和持久化，不能仅凭CI开启真实连接

### F40真实health消息

- AD-F-157：实际health终态本人消息、固定查询、稳定分页/定位和工作区已有工程证据；完整分组时间线、旧类型/appName映射与更多生产者仍缺
- AD-F-158：单条首次已读/原收据与有界批量跨页/固定结果all、六计数、取消余项、未知响应恢复、权限epoch终态已交付；all只针对冻结结果，不是实时全库
- AD-F-159：all/read/unread基础计数与阅读筛选后的total分别解释；单条/批量写后旧快照不变，显式新查询才取新状态。当前页条数不冒充总数
- PR116 messages job先保留原HTTP/MFA health与单条链，再独立库执行bulk；61任务通过内部SubmitTx/生产注册表/Worker生成，不是61次HTTP/MFA提交覆盖。受控BulkOnce提交50后取消余11证明事务/取消，不冒充自主Worker时序；另有自动选2链与后端独立测试
- 查询1000候选、批量保护预算、61条夹具、PG连接终止/容器崩溃恢复属于具名工程保护/测试，不构成历史增长、吞吐/P95、保留期或产品容量验收。完整源兼容、其他来源/渠道、真实AD/Windows联动和生产门禁仍缺
- 运行时/只读/单条/批量各契约中的“本片未含后续功能”是对应检查点的范围；当前组合状态以本表和各PR最后门禁为准，历史本地缺Docker/DSN不是当前远端CI未运行

### B2与其他SOC工作区

- PR103/110补齐B2真实跨账户、原回执、不重复写、无域B、双通知失效后的原生标签生命周期与/me复核。原生browser_cancelled与真实正文消费后的迟到应用交付分别有证据；后者不证明取消后原生网络继续送达；这是证据边界，不新增产品要求或强制补测门禁。源字段/账户所有权语义、生产密钥轮换/灾难恢复、未确认结束的opened使用及真实AD/Windows仍开放
- PR94任务详情时间线只显示真实持久事件；不借任务成功推断外部AD/通知效果。源任务状态/字段兼容、engineRestart、容量/保留期仍开放
- PR98审计工作区只展开真实已授权字段并保留原XLSX和可恢复隐藏；完整源枚举/时间/列兼容、物理删除/留存语义及100,000行容量未验收
- PR102健康工作区只覆盖local-api内核可见资源/API文件系统；Worker暂停恢复不等于退出重启。容器配额、PG数据卷、多节点、外部缓存/引擎、自动清理及生产最小权限DB仍开放
- PR97是工程CI优化。观察到的运行时长不保证未来性能；固定镜像完整性、全部门禁与失败记录保留，不改变AD-F产品完成数

## Issue与历史记录处理

产品父Issue [#12](https://github.com/yunpiao/adtr/issues/12)、[#13](https://github.com/yunpiao/adtr/issues/13)、[#14](https://github.com/yunpiao/adtr/issues/14)、[#16](https://github.com/yunpiao/adtr/issues/16)、[#17](https://github.com/yunpiao/adtr/issues/17)、[#25](https://github.com/yunpiao/adtr/issues/25)、[#30](https://github.com/yunpiao/adtr/issues/30)、[#50](https://github.com/yunpiao/adtr/issues/50)仍open。#91/93/95/96已由此前流程关闭；#99/100/101/105/107/108/111/112/115读取时仍open，工程合并证据不代替关闭授权。本次未编辑Issue复选框或状态。

catalog保留原commit/PR/CI作为历史证据，新增latest_delivery明确后续具名切片；remaining更新为当前缺口，previous_evidence保持原日期/范围。delivery.md下方原有本地检查点按历史阅读，不以删去失败记录制造连续成功。

## 本次台账校验

未修改基线先通过requirements校验与全部107项Python契约。修改后须再次运行同样检查，并比较209条的id/group/name/description/source_sheet/source_row/priority/issue/field_records/ui_records、源工作簿与scope全部不变，只允许上述3条状态变化且product_accepted全false。文档链接、PR/commit/run对应关系和实际diff独立复核后再发布草稿PR；本次精确head CI/合并记录由该PR补充，不借用上表基线。
