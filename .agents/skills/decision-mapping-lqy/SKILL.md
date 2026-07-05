---
name: decision-mapping-lqy
description: 把松散想法转成一组有顺序的调研 tickets，并逐个推进到结论。
---

当一个松散想法需要多个 agent 会话才能转化为计划时，调用此 skill。它会在 Markdown 文件中创建一个有状态的决策图，并推动用户按顺序解决一组 ticket；这些 ticket 可能需要原型、调研或讨论。

## 决策图

决策图是一个紧凑的 Markdown 文件，每个规划工作一个，并随项目一起由 git 跟踪。它是规范工件：**每个会话都会把整张图作为上下文加载**，所以必须保持紧凑。

在 ticket 中创建的资产应从决策图链接出去，不要重复粘贴到图里。

### 结构

编号条目（“tickets”），每个条目是自己的 section，并由编号索引：

```markdown
## #1: Relational Or Non-Relational Database?

Blocked by: #<ticket-number>, #<ticket-number>
Type: Research | Prototype | Grilling

### Question

<question-here>

### Answer

<answer-here>
```

每个 ticket 都必须能放进一个 100K token 的 agent 会话。

## Ticket 类型

有三类 ticket：

- **Research**：阅读文档、第三方 API，或知识库等本地资源。创建 Markdown 摘要作为资产。当需要当前工作目录之外的知识时使用。
- **Prototype**：编写 UI 或逻辑代码来测试假设或探索设计空间。使用 `/prototype-lqy` skill。创建原型作为资产。当关键问题是“它应该看起来如何”或“它应该如何表现”时使用。
- **Grilling**：与 agent 对话。使用 `/grilling-lqy` 和 `/domain-modeling-lqy` skills。一次问一个问题。默认使用此类型。

## 战争迷雾

决策图在边界之外是_故意_不完整的。你的工作是调查边界，并按顺序解决 ticket，从而把边界向前推进。一次推进一个节点，拨开战争迷雾。

到某个时刻，战争迷雾应该已经被推开到足够远，通向终点的路径变得清晰。那时不再需要更多 ticket，决策图可以视为“完成”。

## 调用

此 skill 有两种调用方式：**引导**和**恢复**。

### 引导

用户带着松散想法调用。

1. 运行 `/grilling-lqy` + `/domain-modeling-lqy` 会话，暴露未决决策。一次问一个问题。
2. 编写新的决策图：主要记录迷雾、已确定的边界，以及可以内联解决的简单条目。
3. 停止。构建决策图就是一个会话的工作；不要同时解决 ticket。

### 恢复

用户用现有决策图路径和 ticket 编号调用。

1. 加载**整张决策图**作为上下文。
2. 运行会话来解决该 ticket，并按需调用 skills。如果不确定，使用 `/grilling-lqy` 和 `/domain-modeling-lqy`。
3. 在 ticket 正文中记录本次会话解决的问题。
4. 添加新发现的 ticket（带正确的 `blocked_by` 边）。
5. 停止。

如果已做出的决策让决策图其他部分失效，更新或删除那些节点。

## 并行性

用户可以选择并行运行 ticket，所以要预期其他 agent 会修改决策图。

## 跳过决策图

很多时候，初始追问不会产生战争迷雾：没有未解决的 ticket，除了实现以外无事可做。

在这些情况下，应让用户选择跳过决策图。因为只有需要多会话决策时，决策图才有价值。

如果用户跳过它，建议直接实现，或使用 `/to-prd-lqy` 安排多会话实现。
