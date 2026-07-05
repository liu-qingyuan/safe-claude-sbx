# 超出范围的知识库

仓库中的“.out-of-scope/”目录存储被拒绝的功能请求的持久记录。它有两个目的：

1. **机构记忆** - 为什么某个功能被拒绝，因此当问题结束时推理不会丢失
2. **重复数据删除**——当出现与之前的拒绝相匹配的新 issue时，该技能可以浮现之前的决定，而不是重新提起诉讼

## 目录结构
```
.out-of-scope/
├── dark-mode.md
├── plugin-system.md
└── graphql-api.md
```
每个**概念**一个文件，而不是每个问题。请求同一件事的多个问题被分组在一个文件下。

## 文件格式

该文件应该以轻松、可读的风格编写——更像是一个简短的设计文档，而不是数据库条目。使用段落、代码示例和示例来使推理清晰并对第一次遇到它的人有用。
```markdown
# Dark Mode

This project does not support dark mode or user-facing theming.

## Why this is out of scope

The rendering pipeline assumes a single color palette defined in
`ThemeConfig`. Supporting multiple themes would require:

- A theme context provider wrapping the entire component tree
- Per-component theme-aware style resolution
- A persistence layer for user theme preferences

This is a significant architectural change that doesn't align with the
project's focus on content authoring. Theming is a concern for downstream
consumers who embed or redistribute the output.

```
ts
// 当前的 ThemeConfig 接口不是为运行时切换而设计的：
接口主题配置{
  颜色：调色板； // 单一调色板，在构建时解决
  字体：FontStack；
}
```

## Prior requests

- #42 — "Add dark mode support"
- #87 — "Night theme for accessibility"
- #134 — "Dark theme option"
```
### 命名文件

对概念使用简短的、描述性的短横线命名：“dark-mode.md”、“plugin-system.md”、“graphql-api.md”。该名称应该足够容易识别，以便浏览目录的人无需打开文件即可了解被拒绝的内容。

### 写出原因

原因应该是实质性的——不是“我们不想要这个”，而是为什么。好理由参考：

- 项目范围或理念（“该项目专注于 X；主题是下游关注点”）
- 技术限制（“支持这一点需要 Y，这与我们的 Z 架构相冲突”）
- 战略决策（“我们选择使用 A 而不是 B，因为......”）

理由应该是持久的。避免提及临时情况（“我们现在太忙了”）——这些并不是真正的拒绝，而是推迟。

## 何时检查 `.out-of-scope/`

在 triage 期间（步骤 1：收集上下文），读取 `.out-of-scope/` 中的所有文件。评估新 issue时：

- 检查请求是否与现有的超出范围的概念匹配
- 匹配是根据概念相似度，而不是关键字——“夜间主题”匹配 `dark-mode.md`
- 如果有匹配，请将其呈现给维护者：“这类似于`.out-of-scope/dark-mode.md` - 我们之前拒绝了这个，因为[原因]。你仍然有同样的感觉吗？”

维护者可以：

- **确认** — 新 issue将添加到现有文件的“先前请求”列表中，然后关闭
- **重新考虑** — 超出范围的文件被删除或更新，并且问题通过正常 triage继续进行
- **不同意** — 问题相关但不同，继续正常 triage

## 何时写入 `.out-of-scope/`

仅当**增强**（不是错误）因“wontfix”而被“拒绝”时。这适用于增强 PR，就像适用于问题一样 — 被拒绝的 PR 会记录在此处，因此相同的请求不会作为新代码返回。

当某些内容被关闭为“wontfix”时，不要**写在这里，因为它**已经实现了**。这是一项内置功能，而不是被拒绝的功能；记录它会因错误拒绝而毒害重复数据删除检查。相反，结束语指出了该功能已经存在的位置。

流程：

1. 维护者认为功能请求超出范围
2.检查匹配的`.out-of-scope/`文件是否已经存在
3. 如果是：将新 issue追加到“优先请求”列表中
4. 如果否：创建一个新文件，其中包含概念名称、决策、原因和第一个先前请求
5. 对问题发表评论，解释该决定并提及“.out-of-scope/”文件
6. 使用“wontfix”标签关闭问题

## 更新或删除超出范围的文件

如果维护者改变了对之前拒绝的概念的想法：

- 删除`.out-of-scope/`文件
- 该技能不需要重新打开旧问题 - 它们是历史记录
- 触发复议的新 issue通过正常 triage进行
