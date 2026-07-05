---
name: setup-matt-pocock-skills-lqy
description: 为 Matt Pocock 工程 skills 配置当前仓库：issue tracker、triage label 词汇和领域文档布局。首次使用其他工程
  skills 前运行一次。
---

# 设置 Matt Pocock 的技能

搭建工程技能假设的每个仓库的配置：

- **issue tracker** — 问题所在（默认为 GitHub；也支持开箱即用的本地 Markdown）
- **triage 标签** — 用于五个规范 triage role的字符串
- **域文档** — `CONTEXT.md` 和 ADR 所在的位置，以及阅读它们的消费者规则

这是一种提示驱动的技能，而不是确定性的脚本。探索，展示您发现的内容，与用户确认，然后编写。

## 流程

### 1. 探索

查看当前的仓库以了解其起始状态。阅读存在的一切；不要假设：

- `git remote -v` 和 `.git/config` — 这是 GitHub 仓库吗？哪一个？
- 仓库根目录下的 `AGENTS.md` 和 `CLAUDE.md` 是否存在？其中是否已经有`## Agent skills` 部分？
- 仓库根目录下的 `CONTEXT.md` 和 `CONTEXT-MAP.md`
- `docs/adr/` 和任何 `src/*/docs/adr/` 目录
- `docs/agents/` — 该技能之前的输出是否已经存在？
- `.scratch/` — 表明本地 Markdown issue tracker 约定已在使用中

### 2. 展示调查结果并提问

总结一下现有的和缺失的。然后引导用户完成三个决定**一次一个** - 呈现一个部分，获得用户的答案，然后转到下一个。不要同时丢弃所有三个。

假设用户不知道这些术语的含义。每个部分都以一个简短的解释开始（它是什么，为什么这些技能需要它，如果他们选择不同会发生什么变化）。然后显示选项和默认值。

**A 部分 — issue tracker。**

> 说明：“issue tracker”是此仓库实际跟踪工作的地方。`to-issues`、`triage`、`to-prd` 和 `qa` 等 skill 会读取和写入它，所以它们需要知道应该调用 `gh issue create`、在 `.scratch/` 下写 Markdown 文件，还是遵循你描述的其他工作流。请选择你真实使用的工作跟踪位置。

默认姿势：这些技能是为 GitHub 设计的。如果“git Remote”指向 GitHub，请提出该建议。如果 `git remote` 指向 GitLab（`gitlab.com` 或自托管主机），则建议使用 GitLab。否则（或者如果用户愿意），提供：

- **GitHub** — 问题存在于仓库的 GitHub 问题中（使用 `gh` CLI）
- **GitLab** — 问题存在于仓库的 GitLab 问题中（使用 [`glab`](https://gitlab.com/gitlab-org/cli) CLI）
- **本地 Markdown** - 问题以文件形式存在于该仓库中的 `.scratch/<feature>/` 下（适用于单独项目或没有远程的仓库）
- **其他**（Jira、Linear 等）— 要求用户在一个段落中描述工作流；该技能会将其记录为自由散文

如果且仅当用户选择 **GitHub** 或 **GitLab** 时，询问一个后续问题：

> 说明：开源仓库收到的功能请求不一定只来自 issue，也可能来自 PR。可以把 PR 理解为“带着代码一起提交的请求”。如果开启这个选项，`/triage-lqy` 会把*外部* PR 拉入同一个队列，并用与 issue 相同的标签和状态处理它们（协作者正在推进的 PR 会保留原状）。如果你不把 PR 当作请求入口，请关闭它。

- **PR 作为请求表面** — 是/否（默认值：否）。将答案记录在“docs/agents/issue-tracker.md”中。对于本地 Markdown 和其他跟踪器，请跳过这个问题 - 没有 PR。

**B 部分 — triage 标签词汇。**

> 说明：当 `/triage-lqy` skill 处理新进入的 issue 时，它会把 issue 推进到不同状态：需要评估、等待报告者补充信息、已准备好让 AFK agent 接手、需要人类处理，或者不会处理。为此，它需要应用与你*实际配置*一致的标签（或 issue tracker 中的等价状态）。如果你的仓库已经使用不同标签名（例如 `bug:triage` 而不是 `needs-triage`），请在这里建立映射，避免 skill 创建重复标签。

五个典型 role：

- `needs-triage` — 维护者需要评估
- `needs-info` — 等待报告者
- `ready-for-agent` — 完全指定，AFK 就绪（agent 可以在没有额外人类上下文时接手）
- `ready-for-human` — 需要人类实施
- `wontfix` — 不会被执行

默认值：每个 role的字符串等于其名称。询问用户是否想要覆盖任何内容。如果他们的 issue tracker 没有现有标签，则默认值就可以。

**C 部分 — 领域文档。**

> 说明：一些 skill（`improve-codebase-architecture`、`diagnosing-bugs`、`tdd`）会读取 `CONTEXT.md` 来学习项目的领域语言，并读取 `docs/adr/` 来了解过去的架构决策。它们需要知道仓库是单一领域上下文，还是包含多个领域上下文（例如前端/后端各有上下文的 monorepo），这样才能去正确的位置查找。

确认布局：

- **单上下文** — 仓库根目录下的一个 `CONTEXT.md` + `docs/adr/`。大多数仓库都是这样的。
- **多上下文** — 根目录中的“CONTEXT-MAP.md”指向每个上下文的“CONTEXT.md”文件（通常是 monorepo）。

### 3.确认并编辑

向用户显示草稿：

- 添加到正在编辑的`CLAUDE.md`/`AGENTS.md`中的`## Agent skills`块（有关选择规则，请参阅步骤 4）
- `docs/agents/issue-tracker.md`、`docs/agents/triage-labels.md`、`docs/agents/domain.md` 的内容

让他们在写作之前进行编辑。

### 4. 写

**选择要编辑的文件：**

- 如果 `CLAUDE.md` 存在，编辑它。
- 否则，如果 `AGENTS.md` 存在，则编辑它。
- 如果两者都不存在，请询问用户要创建哪一个 — 不要替他们选择。

当`CLAUDE.md`已经存在时，切勿创建`AGENTS.md`（反之亦然）——始终编辑已经存在的那个。

如果所选文件中已存在`## Agent skills`块，请就地更新其内容，而不是附加重复项。不要覆盖用户对周围部分的编辑。

区块：
```markdown
## Agent skills

### Issue tracker

[one-line summary of where issues are tracked, plus whether external PRs are a triage surface]. See `docs/agents/issue-tracker.md`.

### Triage labels

[one-line summary of the label vocabulary]. See `docs/agents/triage-labels.md`.

### Domain docs

[one-line summary of layout — "single-context" or "multi-context"]. See `docs/agents/domain.md`.
```
然后使用此技能文件夹中的种子模板作为起点编写三个文档文件：

- [issue-tracker-github.md](./issue-tracker-github.md) — GitHub issue tracker
- [issue-tracker-gitlab.md](./issue-tracker-gitlab.md) — GitLab issue tracker
- [issue-tracker-local.md](./issue-tracker-local.md) — 本地 Markdown issue tracker
- [triage-labels.md](./triage-labels.md) — 标签映射
- [domain.md](./domain.md) — 域文档消费者规则+布局

对于“其他”issue tracker，请使用用户的描述从头开始编写“docs/agents/issue-tracker.md”。

### 5.完成

告诉用户设置已完成，并且现在将从这些文件中读取哪些工程技能。提及他们稍后可以直接编辑“docs/agents/*.md”——只有当他们想要切换 issue tracker 或从头开始时才需要重新运行此技能。
