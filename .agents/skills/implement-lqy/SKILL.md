---
name: implement-lqy
description: 根据 PRD 或一组 issues 实现一项工作。
---

实施用户指定的 PRD 或 issue 中描述的工作。

PRD 可以作为实现规格和验收上下文；如果用户只传入一个 PRD 父 issue，且它明显还需要拆成多个独立任务，不要直接把整个 PRD 当作一个实现任务。先使用 /to-issues-lqy 拆分，或要求用户指定要实现的子 issue。

尽可能在预先商定的接缝处使用 /tdd-lqy。

定期运行类型检查，定期运行单个测试文件，最后运行一次完整的测试套件。

完成后，使用 /review-lqy 来检查工作。

写 issue 评论、完成摘要或关闭说明前，遵守 `docs/agents/issue-tracker.md` 的语言约定；没有约定时，LQY 默认使用中文。

多行 `gh issue comment` 使用 heredoc 或 `--body-file`，避免 shell 展开 `$(...)`。

收尾：

- 完成：提交到当前分支；评论 commit hash、验证结果和摘要；关闭 issue。
- 未完成但有完整增量：先提交；评论已完成、剩余工作和下一步。
