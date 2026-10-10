# ADTR 总体方案评审入口

本目录提交 ADTR 的产品与技术总体方案评审稿，供确认产品结构、安全边界、交互覆盖和分阶段交付顺序。对应 [方案 Issue #86](https://github.com/yunpiao/adtr/issues/86)，后续 [原型 Issue #87](https://github.com/yunpiao/adtr/issues/87) 依赖方案确认。

当前版本是提案，不代表用户已批准或 G01/G02 已关闭。它不修改生产行为，也不重新定义既有接口。

## 阅读顺序

1. [总体技术方案](overall-design.md)：定位、角色、导航、架构、数据、接口、执行安全及验收。
2. [全量需求追溯](requirements-traceability.md)：209 项需求逐项映射到功能组、原 Issue、计划导航与已有证据状态。
3. [评审与决策清单](review-decisions.md)：需要用户或专项负责人决定的事项，以及文档冲突的处理方式。

## 基线与证据规则

- 核查基线：[`26f1463e98d2064182ad3f89e8663af7a0d9ef24`](https://github.com/yunpiao/adtr/commit/26f1463e98d2064182ad3f89e8663af7a0d9ef24)，2026-10-10 读取。
- 范围权威：[catalog.json](../../requirements/catalog.json) 与[路线图 #1](https://github.com/yunpiao/adtr/issues/1)。实施状态沿用基线台账，不把本文补充设计转为实现证据。
- 核查时台账：209 项、57 组、7 门禁；19 项有经验证切片，26 项实施中，164 项未记录实现证据；产品验收 0/209。切片状态具有明确的剩余范围，不等于整项通过。
- 历史 ADR/早期契约与当前实现可能有时间差。本文逐项指出差异，不通过删除旧契约掩盖问题；规范变更须另行评审。
- 原型只使用合成数据，不能产生 AD 连接、封禁、通知或真实权限授予。方案确认、原型确认、代码验证、产品验收分别留证。

## 编排参考

参考 BaoCut 的[产品设计](https://github.com/JimLiu/baocut/blob/main/docs/product/product-design.md)、[架构设计](https://github.com/JimLiu/baocut/blob/main/docs/architecture/architecture-design.md)和[开发流程](https://github.com/JimLiu/baocut/blob/main/docs/development-workflow.md)，借鉴其产品行为、状态所有权、验证证据分开组织的方式，以及原型先行的评审流程。ADTR 的角色、业务、技术栈和安全约束均来自本仓库；未移植 BaoCut 的媒体产品内容或技术选型。
