---
name: handoff-out
description: Generate a compact copy-paste handoff prompt for another AI or fresh session. Use when the user invokes $handoff-out or asks to hand off, delegate, transfer context, or prepare a prompt for another agent. Do not write files and do not execute the handed-off task.
---

# Handoff

生成可复制给另一个 AI / 新会话的最小 prompt。只输出到聊天；不要保存文件、写 `/tmp`、修改 workspace，或执行被交接任务。

## 默认输出

1. `可复制 prompt`：一个 fenced Markdown code block，直接对接手 AI 说话。
2. `这个 handoff 写了什么`：一句话或极短 bullets，说明包含了哪些信息。

## Prompt 规则

只写足够接手的信息，不写计划、PRD、checklist 或实施脚本。默认包含：

- 目标：用户明确要求的结果；不要替用户扩展意图。
- 参考：必要路径、artifact、URL、commit 或当前会话事实；不要粘贴长内容。
- 边界：已知范围、非目标和不确定点；不确定就写“未明确”。
- 验收：需要的证据或验证类别；不要写长命令配方。
- 停止规则：如果需要越过目标/边界，先向用户确认或报告风险。

## 默认行为

- 普通 `$handoff-out` 不追问；把不确定性转成接手 AI 的确认/停止边界。
- 实现细节默认交给接手 AI 判断。
- 公共 API、持久化格式、新依赖、破坏性命令、隐私/安全弱化、无关改动默认需要先确认。
- 匹配用户语言；保留代码标识、路径、命令、JSON 字段和 skill 名称。

## 维护验证

修改后至少运行 `quick_validate.py`。未做真实 `$handoff-out ...` smoke 时，不要声称运行时行为已验证。
