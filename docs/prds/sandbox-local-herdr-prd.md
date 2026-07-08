# PRD: sandbox-local Herdr supervision

GitHub issue: #13

## 问题陈述

用户已经可以通过 `safe-claude-sbx` 在 Docker Sandbox 中安全启动 Claude Code，并让启动器在 TUN、host egress、sandbox egress、mount 和 environment policy 不满足时 fail closed。用户平时使用 Herdr 管理本机终端，但 host Herdr 无法可靠识别 Docker Sandbox 内 Claude Code 的内部状态。

直接把 host Herdr socket 或 host `HERDR_*` 环境变量传进 sandbox 会扩大隔离边界：sandbox 内进程可能拿到 host pane、socket、workspace 或控制接口。用户不要求 host Herdr 显示 sandbox 内 Claude 状态，只希望可以在 sandbox 内使用独立 Herdr 来管理和观察 sandbox 内 Claude Code，同时继续保持 host 与 sandbox 的 Herdr 边界隔离。

## 解决方案

新增一个 sandbox-local Herdr supervision 模式。该模式在 Docker Sandbox 内安装或确认 Herdr 可用，安装 Claude Code 的 Herdr integration hook，并在 sandbox 内使用 sandbox-local Herdr socket、sandbox-local pane/session identity 和 sandbox-local environment 启动 Claude Code。

host Herdr 不参与此模式。`safe-claude-sbx` 不把 host `HERDR_SOCKET_PATH`、`HERDR_PANE_ID`、`HERDR_TAB_ID`、`HERDR_WORKSPACE_ID` 或其他 host `HERDR_*` 传入 sandbox。sandbox 内的 Herdr 状态留在 sandbox 内。

该模式必须继承现有安全启动和运行期行为：启动前必须通过 TUN、host egress、sandbox egress、workspace mount 和 environment inspection；运行中如果当前事件触发的 host-centered watchdog 发现默认路由、TUN 接口或 host egress 不符合策略，仍然停止 sandbox。此 PRD 不新增定时轮询 watchdog。

## 用户故事

1. 作为使用 Herdr 的 macOS 开发者，我想在 Docker Sandbox 内运行独立 Herdr，这样我可以让 sandbox 内 Herdr 管理 sandbox 内 Claude Code，而不暴露 host Herdr socket。
2. 作为使用 Claude Code 的开发者，我想保留 `safe-claude-sbx` 的 TUN 和 egress preflight，这样 sandbox-local Herdr 模式不会绕过现有网络安全检查。
3. 作为使用 Claude Code 的开发者，我想在 TUN 或 egress 失效时仍然停止 sandbox，这样 sandbox-local Herdr 不会让 Claude Code 在错误网络路径下继续运行。
4. 作为注重隔离的用户，我想确保 host `HERDR_*` 环境变量不会进入 sandbox，这样 sandbox 内进程无法直接连接 host Herdr socket。
5. 作为注重隔离的用户，我想 sandbox-local Herdr 使用 sandbox 内自己的 socket 和 pane/session identity，这样 Herdr 状态不会依赖 host Herdr。
6. 作为开发者，我想在 sandbox-local Herdr 不可安装或不可启动时 fail closed，这样不会退回到不受监督的 Claude Code 启动路径。
7. 作为开发者，我想明确知道 shell probe sandbox 与 Claude template sandbox 的区别，这样不会在缺少 `/home/agent/.claude` 的 shell probe 里错误安装 Claude integration。
8. 作为开发者，我想 `doctor` 或等价验证能确认 sandbox-local Herdr prerequisites，这样我能在启动前知道 Herdr supervision 模式是否可用。
9. 作为开发者，我想 sandbox-local Herdr 的安装和 hook 配置可重复执行，这样重复启动不会破坏 sandbox。
10. 作为开发者，我想 sandbox-local Herdr 模式仍然能正确 cleanup，这样 `safe-claude-sbx` 停止 sandbox 时不会留下 host 进程或 probe sandbox。
11. 作为维护者，我想用清晰配置开关启用 sandbox-local Herdr，这样默认 Claude Code 启动路径保持简单。
12. 作为维护者，我想 Herdr 相关失败有明确诊断，这样我可以区分 Herdr 安装失败、hook 缺失、socket 未启动、TTY attach 问题和网络 policy 失败。

## 实现决策

- 新增 sandbox-local Herdr supervision 作为可选模式，而不是替换默认 `claude` agent 启动路径。
- 默认行为保持当前 `sbx run claude --name <main-name> <workspace>`。
- Herdr 模式不得暴露 host Herdr socket 或 host `HERDR_*` environment。
- Herdr 模式使用 sandbox-local `HERDR_ENV`、`HERDR_SOCKET_PATH` 和 `HERDR_PANE_ID`。这些值必须指向 sandbox 内资源。
- Herdr 模式必须在 Claude template sandbox 中验证，因为 shell template 不包含 Claude Code 或 `/home/agent/.claude`。
- 当前已验证事实：
  - Herdr can install inside the Claude Docker Sandbox as `/home/agent/.local/bin/herdr`, version `0.7.1`.
  - `herdr integration install claude` succeeds inside `claude-sbx`.
  - The hook installs at `/home/agent/.claude/hooks/herdr-agent-state.sh`.
  - The hook exits unless `HERDR_ENV`, `HERDR_SOCKET_PATH`, and `HERDR_PANE_ID` are set.
  - Host `HERDR_*` values are absent inside sandbox under the current launcher policy.
- Existing `environment` policy must continue to reject host Herdr values. If sandbox-local Herdr values are allowed, the policy must distinguish sandbox-local paths from host Herdr paths.
- The launch path may need an in-sandbox supervisor command, such as starting a shell/entrypoint that starts sandbox-local Herdr and then starts Claude Code under Herdr. This must preserve real TTY behavior.
- The existing event-triggered watchdog remains the runtime network enforcement
  mechanism through route monitor and Clash app-home metadata events. This PRD
  does not add periodic polling.
- If sandbox-local Herdr startup fails, the launcher must fail closed or stop the sandbox rather than falling back silently.
- The PRD parent issue should be decomposed with `$to-issues-lqy` into prototype and implementation slices before AFK implementation.

## 测试决策

- Highest test seam: CLI behavior. Given config and fake platform/backend state, launch either starts the correct supervised sandbox command or fails closed with safe diagnostics.
- Existing backend adapter seam should be reused to fake `sbx` process behavior and verify command contracts without testing private functions.
- Existing policy tests should be extended to distinguish host Herdr variables from sandbox-local Herdr variables.
- Existing launch tests should ensure host `HERDR_*` values are not inherited by the main sandbox command.
- New behavior tests should focus on externally visible outcomes:
  - Herdr mode rejects missing sandbox-local Herdr prerequisites.
  - Herdr mode uses sandbox-local Herdr socket values, not host values.
  - Herdr mode preserves TTY attachment for interactive Claude Code.
  - Herdr mode still runs preflight before any main agent session is attached.
  - Herdr mode still stops the sandbox through existing cleanup/watchdog paths.
- Manual validation remains required because Docker Sandbox, Herdr terminal behavior, and Claude Code TTY behavior depend on the local macOS environment.
- Good tests should assert safe diagnostics and state transitions, not exact implementation details of shell scripts or private helper functions.

## 超出范围

- Host Herdr displaying sandbox Claude Code state.
- Exposing host `HERDR_SOCKET_PATH` or host Herdr socket into sandbox.
- Building a host-to-sandbox or sandbox-to-host Herdr bridge.
- Parsing Claude Code UI output for busy/idle/login state.
- Expanding watchdog to periodic polling.
- Managing Claude, Anthropic, Docker, Herdr, account, organization, payment, or service-side risk controls.
- Bypassing platform rules, account controls, organization policies, or service-side detection.
- Supporting non-Docker-Sandbox backends for Herdr supervision.

## 进一步说明

This PRD intentionally chooses sandbox-local Herdr state over host Herdr integration. The security posture is that Herdr may run inside the sandbox, but host Herdr identity and socket material must remain outside.

The next step is to use `$to-issues-lqy` to create vertical slices. The first slice should likely be a prototype that proves sandbox-local Herdr can start, create a sandbox-local socket, install the Claude hook, and launch Claude Code in the same interactive TTY without weakening the existing preflight and cleanup contract.
