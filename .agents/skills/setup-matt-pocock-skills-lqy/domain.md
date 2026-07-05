# 域文档

在探索代码库时，工程技能应如何使用此仓库的域文档。

## 在探索之前，请阅读这些

- **`CONTEXT.md`** 位于仓库根目录，或者
- **`CONTEXT-MAP.md`** 位于仓库根目录（如果存在） — 它指向每个上下文一个 `CONTEXT.md`。阅读与主题相关的每一篇。
- **`docs/adr/`** — 阅读涉及您将要工作的区域的 ADR。在多上下文仓库中，还请检查 `src/<context>/docs/adr/` 以了解上下文范围内的决策。

如果这些文件不存在，**安静地继续**。不要把缺失当作问题；也不要建议预先创建它们。当术语或决策真正明确时，`/domain-modeling-lqy` skill（通常通过 `/grill-with-docs-lqy` 和 `/improve-codebase-architecture-lqy` 触发）会按需创建这些文件。

## 文件结构

单上下文仓库（大多数仓库）：
```
/
├── CONTEXT.md
├── docs/adr/
│   ├── 0001-event-sourced-orders.md
│   └── 0002-postgres-for-write-model.md
└── src/
```
多上下文仓库（根目录中存在“CONTEXT-MAP.md”）：
```
/
├── CONTEXT-MAP.md
├── docs/adr/                          ← system-wide decisions
└── src/
    ├── ordering/
    │   ├── CONTEXT.md
    │   └── docs/adr/                  ← context-specific decisions
    └── billing/
        ├── CONTEXT.md
        └── docs/adr/
```
## 使用词汇表的词汇

当您的输出命名域概念时（在问题标题、重构提案、假设、测试名称中），请使用“CONTEXT.md”中定义的术语。不要转向词汇表明确避免的同义词。

如果您需要的概念尚未出现在术语表中，那么这是一个信号 - 要么您正在发明项目不使用的语言（重新考虑），要么存在真正的差距（请注意“/domain-modeling-lqy”）。

## 标记 ADR 冲突

如果您的输出与现有 ADR 相矛盾，请明确显示它，而不是默默地覆盖：

> _Contradicts ADR-0007（事件源订单）——但值得重新打开，因为……_