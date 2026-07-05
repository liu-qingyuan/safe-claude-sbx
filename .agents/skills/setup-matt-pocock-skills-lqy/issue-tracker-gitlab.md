# issue tracker：GitLab

此仓库的issue 和 PRD 作为 GitLab 问题存在。使用 [`glab`](https://gitlab.com/gitlab-org/cli) CLI 进行所有操作。

## 惯例

- **创建问题**：`glab issues create --title "..." --description "..."`。使用heredoc进行多行描述。通过 `--description -` 打开编辑器。
- **阅读问题**：`glab 问题视图 <number> --comments`。使用“-F json”获得机器可读的输出。
- **列出问题**：带有适当的“--label”过滤器的“glab 问题列表 -F json”。
- **对问题发表评论**：`glab 问题注释 <number> --message "..."`。 GitLab 将注释称为“注释”。
- **应用/删除标签**：`glab issues update <number> --label "..."` / `--unlabel "..."`。多个标签可以用逗号分隔或通过重复标志来分隔。
- **关闭**：`glab 问题关闭 <number>`。 `glab issues close` 不接受结束评论，因此首先使用 `glab issues note <number> --message "..."` 发布解释，然后关闭。
- **合并请求**：GitLab 将 PR 称为“合并请求”。使用`glab mr create`、`glab mr view`、`glab mr note`等——与`gh pr ...`形状相同，用`mr`代替`pr`，用`note`/`--message`代替`comment`/`--body`。

从 `git remote -v` 推断仓库 — `glab` 在克隆内运行时会自动执行此操作。

## 合并请求作为 triage 表面

**MR 作为请求表面：否。** _（如果此仓库将外部合并请求视为功能请求，则设置为“yes”；“/triage-lqy”读取此标志。）_

当设置为“yes”时，MR 会使用“glab mr”等效项运行与问题相同的标签和状态：

- **阅读 MR**：`glab mr view <number> --comments` 和 `glab mr diff <number>` 进行比较。
- **列出外部 MR 进行 triage**：`glab mr list -F json`，然后仅保留作者不是项目成员/所有者的 MR（贡献者的 MR，而不是维护者的正在进行的工作）。
- **评论/标签/关闭**：`glab mr note`、`glab mr update --label`/`--unlabel`、`glab mr close`。

与 GitHub 不同，GitLab 分别对问题和 MR 进行编号，因此一旦您知道维护者指的是哪个表面，“#42”就明确无误。

## 当技能说“发布到 issue tracker”时

创建一个 GitLab 问题。

## 当技能说“获取相关 ticket”时

运行 `glab issues view <number> --comments`。
