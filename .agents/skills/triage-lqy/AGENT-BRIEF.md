# 撰写agent brief

agent brief是在 GitHub issue 或 PR 转到“ready-for-agent”时发布的结构化评论。这是 AFK agent 工作所依据的权威规范。原始正文和讨论是上下文——agent brief是合同。

简报指出了**agent 应该做什么**，这延伸到两个层面：对于一个问题，这是从无到有的改变；对于 PR 来说，剩下要做的就是*现有的差异*——完成它，缩小差距，解决审查点。无论哪种方式，原则都是相同的；下面的 PR 示例显示了差异。

## 原则

### 耐用性高于精度

该问题可能会处于“ready-for-agent”状态数天或数周。与此同时，代码库也会发生变化。编写摘要，以便即使文件被重命名、移动或重构，它仍然有用。

- **做**描述接口、类型和行为契约
- **Do** 命名agent 应查找或修改的特定类型、函数签名或配置形状
- **不要**引用文件路径 - 它们会过时
- **不要**参考行号
- **不要**假设当前的实现结构将保持不变

### 行为上的，而不是程序上的

描述系统应该做什么，而不是如何实现它。agent 将重新探索代码库并做出自己的实现决策。

- **好：**“‘SkillConfig’类型应该接受‘CronExpression’类型的可选‘schedule’字段”
- **不好：** “打开 src/types/skill.ts 并在第 42 行添加计划字段”
- **好：**“当用户不带参数运行`/triage-lqy`时，他们应该看到需要注意的问题的摘要”
- **坏：**“在主处理函数中添加 switch 语句”

### 完整的验收标准

agent 需要知道什么时候完成。每个agent brief都必须有具体的、可测试的验收标准。每个标准都应该是可独立验证的。

- **好：** “运行 `gh issues list --label need-triage` 返回已通过初始 triage 的 issue”
- **不好：**“triage 应该正常工作”

### 显式范围边界

说明哪些内容超出了范围。这可以防止 agent 镀金或对相邻特征做出假设。

＃＃ 模板
```markdown
## Agent Brief

**Category:** bug / enhancement
**Summary:** one-line description of what needs to happen

**Current behavior:**
Describe what happens now. For bugs, this is the broken behavior.
For enhancements, this is the status quo the feature builds on.

**Desired behavior:**
Describe what should happen after the agent's work is complete.
Be specific about edge cases and error conditions.

**Key interfaces:**
- `TypeName` — what needs to change and why
- `functionName()` return type — what it currently returns vs what it should return
- Config shape — any new configuration options needed

**Acceptance criteria:**
- [ ] Specific, testable criterion 1
- [ ] Specific, testable criterion 2
- [ ] Specific, testable criterion 3

**Out of scope:**
- Thing that should NOT be changed or addressed in this issue
- Adjacent feature that might seem related but is separate
```
## 示例

### 好agent brief（错误）
```markdown
## Agent Brief

**Category:** bug
**Summary:** Skill description truncation drops mid-word, producing broken output

**Current behavior:**
When a skill description exceeds 1024 characters, it is truncated at exactly
1024 characters regardless of word boundaries. This produces descriptions
that end mid-word (e.g. "Use when the user wants to confi").

**Desired behavior:**
Truncation should break at the last word boundary before 1024 characters
and append "..." to indicate truncation.

**Key interfaces:**
- The `SkillMetadata` type's `description` field — no type change needed,
  but the validation/processing logic that populates it needs to respect
  word boundaries
- Any function that reads SKILL.md frontmatter and extracts the description

**Acceptance criteria:**
- [ ] Descriptions under 1024 chars are unchanged
- [ ] Descriptions over 1024 chars are truncated at the last word boundary
      before 1024 chars
- [ ] Truncated descriptions end with "..."
- [ ] The total length including "..." does not exceed 1024 chars

**Out of scope:**
- Changing the 1024 char limit itself
- Multi-line description support
```
### 好agent brief（增强）
```markdown
## Agent Brief

**Category:** enhancement
**Summary:** Add `.out-of-scope/` directory support for tracking rejected feature requests

**Current behavior:**
When a feature request is rejected, the issue is closed with a `wontfix` label
and a comment. There is no persistent record of the decision or reasoning.
Future similar requests require the maintainer to recall or search for the
prior discussion.

**Desired behavior:**
Rejected feature requests should be documented in `.out-of-scope/<concept>.md`
files that capture the decision, reasoning, and links to all issues that
requested the feature. When triaging new issues, these files should be
checked for matches.

**Key interfaces:**
- Markdown file format in `.out-of-scope/` — each file should have a
  `# Concept Name` heading, a `**Decision:**` line, a `**Reason:**` line,
  and a `**Prior requests:**` list with issue links
- The triage workflow should read all `.out-of-scope/*.md` files early
  and match incoming issues against them by concept similarity

**Acceptance criteria:**
- [ ] Closing a feature as wontfix creates/updates a file in `.out-of-scope/`
- [ ] The file includes the decision, reasoning, and link to the closed issue
- [ ] If a matching `.out-of-scope/` file already exists, the new issue is
      appended to its "Prior requests" list rather than creating a duplicate
- [ ] During triage, existing `.out-of-scope/` files are checked and surfaced
      when a new issue matches a prior rejection

**Out of scope:**
- Automated matching (human confirms the match)
- Reopening previously rejected features
- Bug reports (only enhancement rejections go to `.out-of-scope/`)
```
### 好 agent brief（PR）

对于 PR，“当前行为”描述了差异的状态，摘要要求 agent 完成或修复它，而不是从头开始构建。
```markdown
## Agent Brief

**Category:** enhancement
**Summary:** Finish the contributor's `--json` output flag for `triage list`

**Current behavior:**
The PR adds a `--json` flag that serializes the issue list to JSON. The happy
path works and the diff matches the project's command structure. Two gaps
remain: errors are still printed as human text (not JSON), and the new flag has
no test coverage.

**Desired behavior:**
With `--json`, all output — including errors — is well-formed JSON on stdout,
and the command's exit codes are unchanged. The existing human-readable output
is untouched when the flag is absent.

**Key interfaces:**
- The command's error path should emit `{ "error": string }` under `--json`
  instead of the plain-text error
- Reuse the existing serializer the PR already added; don't introduce a second

**Acceptance criteria:**
- [ ] `triage list --json` emits valid JSON for both success and error cases
- [ ] Exit codes match the non-JSON command
- [ ] A test covers the `--json` success output and one error case
- [ ] Default (non-JSON) output is byte-for-byte unchanged

**Out of scope:**
- Adding `--json` to any other command
- Changing the JSON shape of the success payload the PR already defined
```
### 坏agent brief
```markdown
## Agent Brief

**Summary:** Fix the triage bug

**What to do:**
The triage thing is broken. Look at the main file and fix it.
The function around line 150 has the issue.

**Files to change:**
- src/triage/handler.ts (line 150)
- src/types.ts (line 42)
```
这很糟糕，因为：
- 没有类别
- 模糊的描述（“triage 问题坏了”）
- 引用文件路径和行号将过时
- 无接受标准
- 无范围界限
- 没有描述当前行为与期望行为
