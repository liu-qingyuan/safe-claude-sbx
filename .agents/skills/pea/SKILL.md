---
name: pea
description: PEA，Project Engineering Architect。用于项目工程架构、模块边界、接口设计、seam/adapter/contract、依赖方向、重构、C4/类图/时序图必要性和 CI/CD 质量门禁判断。默认输出精简架构判断，目标是低耦合、高内聚、深模块、不过度工程。
---

# pea

用 PEA 做架构判断和最小改动建议。目标是保护边界、契约和可维护性，不把小问题扩成大型架构流程。

## 默认输出

优先用短清单或小表格：

- 结论：当前最小架构判断。
- 关键边界：Module / Boundary / Contract / Governance 中真正相关的项。
- 建议：最小必要改动；能不新增抽象就不新增。
- 验证：应运行或已运行的 lint/type/test/contract/architecture check；不能运行就说明缺口。
- 停止规则：如果需要改变公共 API、持久化格式、跨服务契约、依赖策略或大范围职责划分，先报告风险。

## 核心判断

- Module：模块是隐藏复杂性的单元；优先 deep module、small interface、high cohesion、low coupling。
- Boundary：domain/application 不依赖 infrastructure；UI/API/CLI 是入口层，不放核心业务规则；外部系统、协议、存储、provider、tool、event、framework 放 Adapter。
- Contract：API/event/tool/provider/service 边界要有显式 contract 或 typed model；不要让调用方猜 payload、错误形状或 provider 行为。
- Governance：依赖方向、循环依赖、架构规则、安全/权限/发布质量门禁应尽量可自动验证。


## 渐进式加载 / 奥卡姆剃刀

`SKILL.md` 是小接口；references/scripts 是深实现。默认不新增资源、文件、抽象或流程。只有当某类细节反复需要、可条件加载、且放在主文件会干扰常见判断时，才新增 reference、script、图或流程。

## 改动策略

- 先删除或合并浅模块，再加深已有模块，最后才新增 interface/factory/registry/plugin/adapter。
- Seam 必须是真实变化点、替换点或集成边界；不要为假想未来抽象。
- 一个实现通常不需要 Adapter；真实外部系统/协议/provider/存储/模型边界才需要。
- 避免耦合泄漏：业务代码不应知道 provider、transport、schema、配置文件、tool event 或框架内部细节。
- 用 locality 判断好坏：理解、修改、测试一个行为不应到处跳转。

## 图和文档

先看项目已有 `docs/architecture/`。只有边界、职责、契约、协作顺序或治理规则会影响改动时，才补 C4、类图或时序图。

- 总体 C4：可用 `c4-architecture`。
- 类图/时序图/Mermaid：可用 `mermaid-visualizer`。
- 小改动不要为了画图而画图；不要凭空覆盖已有架构约定。

## 与 TEA 分工

PEA 定义模块、边界、契约和治理形态；TEA 决定用 unit/integration/contract/E2E/gate 如何验证这些形态。

## 禁止事项

不要无真实变化点新增抽象；不要把配置、协议、provider、schema、tool 细节泄漏到上层；不要把 event/API/tool call 当无类型字符串乱传；不要新增依赖前跳过标准库、平台能力和已有依赖；不要在未验证或未说明验证缺口时声称架构完成。
