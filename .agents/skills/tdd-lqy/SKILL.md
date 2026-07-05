---
name: tdd-lqy
description: 测试驱动开发。用于用户想以 test-first 方式构建功能或修复 bug，提到 red-green-refactor，或需要集成测试时。
---

# 测试驱动开发

## 理念

**核心原则**：测试应该通过公共接口验证行为，而不是实现细节。代码可以完全改变；测试不应该。

**好的测试**是集成式的：它们通过公共 API 执行真实的代码路径。它们描述系统“做什么”，而不是“如何”做到这一点。一个好的测试读起来就像一个规范——“用户可以使用有效的购物车结帐”告诉您到底存在什么功能。这些测试能够在重构中幸存下来，因为它们不关心内部结构。

**糟糕的测试**与实施相关。他们模拟内部协作者、测试私有方法或通过外部手段进行验证（例如直接查询数据库而不是使用接口）。警告信号：重构时测试会中断，但行为并没有改变。如果您重命名内部函数并且测试失败，那么这些测试是在测试实现，而不是行为。

有关示例，请参阅 [tests.md](tests.md)；有关模拟指南，请参阅 [mocking.md](mocking.md)。

## 反模式：horizontal slice

**不要先把所有测试写完，再一次性写所有实现。** 这是 horizontal slice：把 RED 理解成“批量写测试”，把 GREEN 理解成“批量补实现”。

这样容易写出**低价值测试**：

- 批量写出来的测试往往验证 _imagined behavior_，而不是刚刚落地的 _actual behavior_
- 容易测试 _shape_（数据结构、函数签名、mock 返回结构），而不是 user-visible behavior
- 测试对真实行为变化不敏感：行为坏了测试还过，行为没坏测试却挂
- 你会超出当前反馈范围，在还没理解实现之前就承诺一套测试结构

**正确方法**：用 tracer bullet / vertical slice 推进。一个测试 → 一个最小实现 → 重复。每个测试都回应上一个 RED/GREEN cycle 中学到的东西。因为代码刚刚写出来，你更清楚哪些行为重要、应该从哪个 public interface 验证。
```
WRONG (horizontal):
  RED:   test1, test2, test3, test4, test5
  GREEN: impl1, impl2, impl3, impl4, impl5

RIGHT (vertical):
  RED→GREEN: test1→impl1
  RED→GREEN: test2→impl2
  RED→GREEN: test3→impl3
  ...
```
## 工作流

### 1. 规划

在探索代码库时，请阅读“CONTEXT.md”（如果存在），以便测试名称和界面词汇与项目的领域语言相匹配，并尊重您所接触的区域中的 ADR。

在编写任何代码之前：

- [ ] 与用户确认需要进行哪些界面更改
- [ ] 与用户确认要测试哪些行为（优先）
- [ ] 识别深模块的机会（小接口，深度实现）——运行“/codebase-design-lqy”技能进行词汇和可测试性检查
- [ ] 列出要测试的行为（不是实施步骤）
- [ ] 获得用户对计划的批准

问：“公共界面应该是什么样子？哪些行为最需要测试？”

**您无法测试所有内容。** 与用户确认哪些行为最重要。将测试工作重点放在关键路径和复杂逻辑上，而不是每种可能的边缘情况。

### 2. Tracer bullet

编写一项测试来确认系统的一件事：
```
RED:   Write test for first behavior → test fails
GREEN: Write minimal code to pass → test passes
```
这是你的 tracer bullet：证明这条路径端到端有效。

### 3.增量循环

对于每个剩余的行为：
```
RED:   Write next test → fails
GREEN: Minimal code to pass → passes
```
规则：

- 一次进行一项测试
- 只有足够的代码来通过当前测试
- 不要预期未来的测试
- 让测试集中在可观察的行为上

### 4.重构

所有测试通过后，寻找[重构候选项](refactoring.md)：

- [ ] 提取重复项
- [ ] 模块加深（将复杂性置于简单界面背后）
- [ ] 在自然的情况下应用 SOLID 原则
- [ ] 考虑新代码揭示了现有代码的哪些内容
- [ ] 在每个重构步骤后运行测试

**永远不要在红色时重构。**首先转向绿色。

## 每个周期的清单
```
[ ] Test describes behavior, not implementation
[ ] Test uses public interface only
[ ] Test would survive internal refactor
[ ] Code is minimal for this test
[ ] No speculative features added
```

