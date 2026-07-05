# CONTEXT.md 格式

## 结构

```md
# {Context Name}

{用一到两句话说明这个上下文是什么，以及它为什么存在。}

## Language

**Order**:
{用一到两句话定义这个术语。}
_Avoid_: Purchase, transaction

**Invoice**:
A request for payment sent to a customer after delivery.
_Avoid_: Bill, payment request

**Customer**:
A person or organization that places orders.
_Avoid_: Client, buyer, account
```

## 规则

- **明确取舍。** 如果同一概念有多个叫法，选定一个最合适的名称，并把其他叫法列到 `_Avoid_` 下。
- **定义要紧凑。** 最多一到两句话。定义它“是什么”，不要描述它“做什么”。
- **只记录这个项目上下文特有的术语。** 通用编程概念（timeout、error type、utility pattern 等）即使在项目里大量出现，也不应放进这里。添加术语前先判断：这是本上下文独有的领域概念，还是通用编程概念？只有前者应该进入 `CONTEXT.md`。
- **自然成组时再分组。** 如果术语自然形成几个主题，可以用小标题分组；如果都属于同一个连贯领域，平铺列表即可。

## 单上下文仓库与多上下文仓库

**单上下文（大多数仓库）：** 仓库根目录有一个 `CONTEXT.md`。

**多上下文：** 仓库根目录有一个 `CONTEXT-MAP.md`，列出各个上下文、它们的位置，以及它们之间的关系：

```md
# Context Map

## Contexts

- [Ordering](./src/ordering/CONTEXT.md) — receives and tracks customer orders
- [Billing](./src/billing/CONTEXT.md) — generates invoices and processes payments
- [Fulfillment](./src/fulfillment/CONTEXT.md) — manages warehouse picking and shipping

## Relationships

- **Ordering → Fulfillment**: Ordering emits `OrderPlaced` events; Fulfillment consumes them to start picking
- **Fulfillment → Billing**: Fulfillment emits `ShipmentDispatched` events; Billing consumes them to generate invoices
- **Ordering ↔ Billing**: Shared types for `CustomerId` and `Money`
```

skill 根据现有文件判断适用哪种结构：

- 如果存在 `CONTEXT-MAP.md`，读取它来找到各个上下文。
- 如果只存在根目录 `CONTEXT.md`，按单上下文处理。
- 如果两者都不存在，就在第一个术语被明确时按需创建根目录 `CONTEXT.md`。

当存在多个上下文时，先推断当前主题属于哪一个上下文；如果无法判断，再询问用户。
