# ralph-plan-lqy localization

- Origin: 自建 skill（2026-07-03），用于 Open Ralph + GitHub issue backlog
- References: [open-ralph-wiggum](https://github.com/Th0rgal/open-ralph-wiggum) CLI 文档；Matt Pocock AFK Ralph 模式（[mattpocock/ai-engineer-workshop-2026-project](https://github.com/mattpocock/ai-engineer-workshop-2026-project) 的 `ralph/afk.sh` + `ralph/prompt.md`）
- 语言：SKILL.md 与 `templates/issue-backlog-prompt.md` 均为中文本地化版本；frontmatter description 保留中英混合以兼顾触发匹配
- 标签词汇与 `$triage-lqy` 对齐：`ready-for-agent` / `ready-for-human`
- 自带脚本：`scripts/check_ready_issue_unblocked.py`，用于 Ralph 领取 issue 前检查 `## 被阻止` / `## Blocked`
- Policy: self-contained；上游 CLI flag 变化时同步更新参数说明
