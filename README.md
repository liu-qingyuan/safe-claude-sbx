# safe-claude-sbx

`safe-claude-sbx` 是一个 macOS 优先的 Claude Code 启动器和运行期 watchdog。它只会在本机网络和 Docker Sandbox 出口 IP 都符合配置策略时，才允许 Claude Code 在 Docker Sandbox 中运行。

`safe-claude-sbx` is a macOS-first launcher and runtime watchdog for Claude Code. It runs Claude Code inside Docker Sandbox only when the local network and Docker Sandbox egress IP match the configured safety policy.

本项目用于合规的本地开发流程。它不绕过平台规则、账号控制、付款要求、组织策略或服务端风控系统。

This project is intended for compliant local development workflows. It does not bypass platform rules, account controls, payment requirements, organization policies, or service-side risk systems.

## 目标 / Goals

- 当 macOS 默认路由没有经过 TUN interface 时，拒绝启动 Claude Code。
- Refuse to start Claude Code when the macOS default route is not going through a TUN interface.
- 验证 host 和 Docker Sandbox 观察到的出口 IP 都等于配置中的期望 IP。
- Verify that both the host and Docker Sandbox egress IP match the configured expected IP.
- 允许 Docker 管理的 sandbox proxy 值和 credential placeholders，同时拒绝 host/未知 proxy target 和原始敏感环境变量值。
- Allow Docker-managed sandbox proxy values and credential placeholders, while rejecting host/unknown proxy targets and raw sensitive environment values.
- 只有显式配置时才接受 Docker Sandbox SSH agent forwarding。
- Require explicit configuration before accepting Docker Sandbox SSH agent forwarding.
- 将 sandbox workspace mount 限制在当前项目目录。
- Restrict the sandbox workspace mount to the current project directory.
- 当运行期 route 或 Clash Verge app-home metadata 事件显示启动时的 TUN interface 消失、默认路由改变，或 host 出口 IP 偏离配置 IP 时，停止 sandbox。
- Stop the sandbox when runtime route or Clash Verge app-home metadata events show that the startup TUN interface disappeared, the default route changed, or host egress drifted from the configured expected IP.

## 非目标 / Non-Goals

- 绕过任何平台策略或风控。
- Bypassing any platform policy or risk control.
- 隐藏或伪装远端服务的网络分类。
- Hiding from remote service network classification.
- 管理 Claude、Anthropic、Docker 或组织凭据。
- Managing Claude, Anthropic, Docker, or organization credentials.
- 提供超出 Docker Sandbox 和 macOS 网络能力之外的完整安全边界。
- Providing a complete security boundary beyond what Docker Sandbox and macOS networking actually enforce.

## 当前状态 / Current Status

本仓库正在实现 Docker Sandbox / `sbx` MVP，相关设计文档如下：

This repository is implementing the Docker Sandbox / `sbx` MVP described in:

- `docs/prds/safe-claude-sbx-prd.md`
- `docs/decision-maps/safe-claude-sbx.md`
- `tests/manual-test-plan.md`

## 日常命令 / Daily Commands

示例配置默认将 `network.egress_ip.expected_ip` 设为团队批准的出口 IP：`34.68.40.236`。只有当你的批准路由使用不同公网 IP 时才需要修改。

The example config defaults `network.egress_ip.expected_ip` to the team's approved egress IP, `34.68.40.236`. Change it only when your approved route uses a different public IP.

日常使用建议先安装二进制，再运行 `safe-claude-sbx` 或 `safe-herdr`：

For daily use, install the binaries first, then run `safe-claude-sbx` or `safe-herdr`:

```bash
go install ./cmd/safe-claude-sbx ./cmd/safe-herdr
```

`go run` 会先 compile 再执行，适合开发验证；它不是最快的日常启动方式。

`go run` compiles before running, which is useful for development checks but is not the fastest daily startup path.

构建包含 sandbox-local Herdr 和 `/usr/local/bin/cc` 的 Docker Sandbox template：

Build the Docker Sandbox template that contains sandbox-local Herdr and `/usr/local/bin/cc`:

```bash
docker build -t safe-claude-sbx-herdr:latest sandbox/claude-herdr-template
docker image save safe-claude-sbx-herdr:latest -o safe-claude-sbx-herdr.tar
sbx template load safe-claude-sbx-herdr.tar
```

设置 `sandbox.template: "safe-claude-sbx-herdr:latest"`，并保持 `sandbox.supervision.herdr.install_if_missing: false`。

Set `sandbox.template: "safe-claude-sbx-herdr:latest"` and keep `sandbox.supervision.herdr.install_if_missing: false`.

验证当前网络和 sandbox 策略：

Validate the current network and sandbox policy:

```bash
safe-claude-sbx doctor --config config.yaml
```

日常入口建议使用带策略检查的 sandbox-local Herdr TUI：

Use the policy-gated sandbox-local Herdr TUI as the daily entry point:

```bash
safe-herdr --config config.yaml
```

`safe-herdr` 会验证策略，直接检查 configured main sandbox，在需要时从 `sandbox.template` 创建主 sandbox，验证 sandbox 内的 `herdr` 和 `cc`，然后通过 `sbx exec -it <main_name> herdr` attach。它不会创建临时 probe sandbox、在启动时下载 Herdr，也不会重写 sandbox-local wrapper。

`safe-herdr` validates policy, directly inspects the configured main sandbox, creates it from `sandbox.template` when needed, verifies `herdr` and `cc` inside that sandbox, and then attaches with `sbx exec -it <main_name> herdr`. It does not create a temporary probe sandbox, download Herdr during startup, or rewrite sandbox-local wrappers.

在 Herdr TUI 内，用 sandbox-local shortcut 启动 Claude：

Inside the Herdr TUI, start Claude with the sandbox-local shortcut:

```bash
cc
```

`cc` 是 template 内置在 `/usr/local/bin/cc` 的 sandbox-local wrapper；它不应该安装在 host 上，也不应该读取 host Claude 凭据。

`cc` is a sandbox-local wrapper at `/usr/local/bin/cc`; it should not be installed on the host or read host Claude credentials.

如果不使用 Herdr，只想直接启动普通 Docker Sandbox Claude 流程，请设置 `sandbox.supervision.mode: "direct-claude"`，然后通过 guarded launcher 启动：

For plain Docker Sandbox usage without Herdr, set `sandbox.supervision.mode: "direct-claude"` and start Claude through the guarded launcher:

```bash
safe-claude-sbx --config config.yaml
```

## 配置 / Configuration

复制 `config.example.yaml` 为 `config.yaml`。示例配置默认使用团队出口 IP `34.68.40.236`；只有当你的本地批准路由不同，才需要在运行 `doctor` 或 `safe-herdr` 前更新 `network.egress_ip.expected_ip`。

Copy `config.example.yaml` to `config.yaml`. The example defaults to the team egress IP `34.68.40.236`; update `network.egress_ip.expected_ip` only if your local approved route differs before running `doctor` or `safe-herdr`.

```bash
cp config.example.yaml config.yaml
```

每个项目通常只需要调整 `workspace.mount`：

For each project, usually only `workspace.mount` needs to be adjusted:

```yaml
workspace:
  mount: "/Users/liuqingyuan/work/your-project"
```

不要把 workspace 设置为 Home、SSH、Claude 配置、Clash 配置、Keychain，或过大的父目录。

Do not set the workspace to Home, SSH, Claude config, Clash config, Keychain, or an overly broad parent directory.

## 安全提示 / Safety Notice

不要将 token、OAuth session、API key、SSH key、Claude 用户配置、Clash 配置或 Keychain 材料提交到本仓库，也不要放进会挂载到 sandbox 的 workspace。

Do not commit tokens, OAuth sessions, API keys, SSH keys, Claude user configuration, Clash configuration, or Keychain material into this repository or into any workspace mounted into a sandbox.
