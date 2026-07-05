# PRD: safe-claude-sbx MVP

## 问题陈述

macOS 本机开发者在使用 Claude Code 的 Docker Sandbox 时，容易因为忘记开启 Clash Verge TUN 模式、默认路由变化、出口 IP 变化或误挂载敏感路径，导致 sandbox 在不符合预期的网络和隔离条件下运行。

用户需要一个启动器，在 sandbox 启动前强制验证网络和挂载策略，在运行期间监听 macOS 路由变化，并在环境不再符合策略时自动停止 sandbox。

## 解决方案

构建一个名为 `safe-claude-sbx` 的 macOS CLI。它读取面向对象组织的 YAML 配置，把运行策略拆成网络设置、sandbox 设置、workspace mount 设置、时区语言设置和运行期守护设置等明确对象。网络设置必须继续拆成两类独立对象：Clash Verge TUN 检测设置和出口 IP 检测设置。

CLI 执行 preflight 检查，只有在 Clash Verge TUN 路由有效、宿主机和 sandbox 出口 IP 都等于配置值、sandbox 内没有显式代理环境变量、workspace mount 安全时，才启动 Docker Sandbox / `sbx` 中的 Claude Code。

启动后，CLI 进入事件驱动的 watchdog。watchdog 使用 `route -n monitor` 监听 macOS 路由变化，把路由事件转换成重新验证动作。每次相关事件触发后，它重新检查启动时记录的 TUN 接口、默认路由、接口可用性和 sandbox 出口 IP。一旦任一对象的状态不再满足策略，CLI 立即停止 sandbox 并执行统一 cleanup。

## 用户故事

1. 作为 macOS 开发者，我想在启动 Claude Code 前自动确认 TUN 已开启，这样我不会在错误网络路径下启动 sandbox。
2. 作为 macOS 开发者，我想验证宿主机公网出口 IP，这样我能确认当前透明代理出口符合预期。
3. 作为 macOS 开发者，我想验证 Docker Sandbox 内部公网出口 IP，这样我能确认 sandbox 实际流量路径符合预期。
4. 作为 macOS 开发者，我想禁止 sandbox 内出现显式代理环境变量，这样 sandbox 内程序不会读取宿主机代理配置。
5. 作为 macOS 开发者，我想只挂载当前项目目录，这样 Home、SSH、Claude 配置、Clash 配置和 Keychain 不会被误暴露。
6. 作为 macOS 开发者，我想在 TUN 关闭时自动停止 sandbox，这样异常网络状态不会持续。
7. 作为 macOS 开发者，我想在默认路由切走启动时的 TUN 接口后自动停止 sandbox，这样路由漂移不会被忽略。
8. 作为 macOS 开发者，我想在 sandbox 出口 IP 变化后自动停止 sandbox，这样节点切换或网络异常不会继续运行任务。
9. 作为 macOS 开发者，我想在 Docker Sandbox 自己退出时让 watchdog 正常退出，这样本地终端不会残留无意义进程。
10. 作为 macOS 开发者，我想按 Ctrl+C 时优雅停止 sandbox 和 watcher，这样资源不会残留。
11. 作为 macOS 开发者，我想用结构化配置分别声明网络、时区、语言、sandbox 和 mount 策略，这样我能清楚知道每类设置影响什么行为。
12. 作为维护者，我想把 Clash Verge TUN 检测和出口 IP 检测建模为网络设置下的两个独立对象，这样路由问题和出口问题可以被独立验证、记录和测试。
13. 作为维护者，我想把 macOS route 检查、egress 检查、mount policy、backend 控制和运行期事件监听分开，这样后续可以扩展普通 Docker 或 Apple container backend。
14. 作为维护者，我想有清晰错误码和日志，这样用户能定位是 TUN、出口 IP、`sbx`、Docker Sandbox、时区语言配置还是挂载策略失败。

## 实现决策

- 第一阶段只支持 macOS 和 Docker Sandbox / `sbx` backend。
- CLI 是唯一用户入口，提供正常运行命令和 `doctor` 检查命令。
- 实现和研究 issue 应把缺失的本机开发工具视为可安装前置条件，而不是立即阻塞。若目标 macOS 环境缺少 `sbx`，agent 应先按 Docker 官方 macOS 流程尝试安装并登录，再确认 backend contract；只有账号登录、浏览器 OAuth、网络策略选择或系统权限交互无法自动完成时，才请求用户补充信息。
- 配置使用 YAML，但不是扁平字段集合。MVP 配置应按职责建模，至少包含：
  - `network.clash_verge`：Clash Verge / TUN 相关设置，包括是否要求 TUN、路由检查目标、允许的 TUN 接口模式和启动时接口锁定策略。
  - `network.egress_ip`：出口 IP 检测设置，包括期望出口 IP、宿主机 IP 检查 URL、sandbox IP 检查 URL、超时和比较策略。
  - `sandbox`：Docker Sandbox / `sbx` backend、sandbox 名称、probe sandbox 命名和 backend 行为策略。
  - `workspace`：workspace mount 根目录、允许挂载的路径和禁止挂载的敏感路径。
  - `environment`：timezone、locale 以及 sandbox proxy environment policy。
  - `watchdog`：route 事件监听、重新验证动作、失败后的 stop/cleanup 策略。
- preflight 必须 fail closed。任何 network、backend、egress、environment 或 mount policy 对象验证失败都不得启动主 sandbox。
- Clash Verge TUN 检测负责验证 macOS 默认路由是否通过 `utunX`，并确认启动时使用的 TUN 接口处于可用状态。默认路由检查使用 `route get <route_check_target>`。
- 出口 IP 检测负责验证宿主机和 sandbox 内观察到的公网 IP 是否都等于配置值。它不负责判断 TUN 是否存在，也不推断 Clash Verge 状态。
- 启动时记录 `startup_tun_interface`。运行期间要求默认路由继续使用该接口；如果接口消失、路由切走或事件后无法确认接口有效，应视为策略失败。
- 运行期 route watcher 是事件驱动组件，使用 `route -n monitor` 监听路由变化。事件触发后重新验证 Clash Verge TUN 状态和 sandbox egress。第一版默认不使用 5 到 10 秒低频兜底轮询。
- watchdog 应把“事件来源”和“验证失败原因”分开记录。例如 route event 触发了检查，但最终失败原因可能是 TUN interface missing、default route changed、host egress mismatch 或 sandbox egress mismatch。
- backend policy、platform/network policy、configuration model 和 mount policy 分离。Docker Sandbox / `sbx` 是 adapter，不承载核心安全策略。
- launcher 不得主动向 sandbox 注入 Clash 或本机代理端口。Docker Sandbox / `sbx` 官方网络模型会在 sandbox 内通过 proxy 环境变量把流量送到 Docker-managed proxy，例如 `gateway.docker.internal:3128`。MVP 应允许这种 Docker-managed proxy env，但必须拒绝 host/Clash 代理值（如 `127.0.0.1:7897`）或未知 proxy 目标。网络一致性仍由 TUN 路由和 host/sandbox egress IP 验证承担。
- timezone 和 locale 不能只通过 host/daemon 日志判断。`sbx` 日志中的 `+08:00` 是 host/daemon 时区；MVP 必须通过 sandbox 内部命令或主 agent 启动 contract 验证 timezone/locale 是否真正生效。
- workspace mount 默认是当前项目目录，并拒绝 Home、SSH、Claude 配置、Clash 配置、Keychain 等敏感路径。
- cleanup 必须幂等。默认停止主 sandbox，删除 probe sandbox，但不删除主 sandbox，避免误删用户状态。
- TUN 检测应优先判断本机可观察事实，而不是只相信 Clash Verge UI 开关。MVP 检测顺序应是：Clash Verge 是否声明开启 TUN、mihomo 最终配置是否包含 `tun.enable: true`、macOS 是否存在可用 `utunX` 接口、默认路由是否走启动时记录的 `utunX`、宿主机和 sandbox 出口 IP 是否符合配置。

## 测试决策

- 最高价值测试接缝是 launcher 行为：给定配置对象、route/backend/egress/mount 的可观察结果，验证 CLI 是否启动、拒绝或 cleanup。
- 单元测试应覆盖配置对象校验、Clash Verge TUN 检测、route 输出解析、egress IP 比较、mount policy、timezone/locale 传递和 cleanup 幂等行为。
- 集成测试应通过 fake backend 和 fake macOS command runner 模拟 `route get`、`route -n monitor`、`ifconfig`、`sbx` 和 `curl`。
- 事件驱动测试应覆盖：TUN interface 消失、默认路由切走、route event 到达但状态仍有效、sandbox egress 变化、backend 自行退出和 Ctrl+C。
- 真实 TUN、Clash Verge、Docker Sandbox 和公网 IP 依赖本机环境，第一阶段必须维护手动测试计划。
- 测试不得依赖真实 token、Claude 凭证、SSH key、Clash 配置或 Keychain。

## 超出范围

- 普通 Docker、Apple container、Linux netlink 或其他 microVM backend 的完整实现。
- 证明所有 macOS 网络工具都会产生完全一致的 route event。

## 进一步说明

第一阶段实现前，需要确认当前本机安装的 `sbx` CLI 的实际命令和 flag。Docker 官方文档显示 Docker Sandbox 支持 `sbx run claude`、`sbx ls`、`sbx stop` 和 `sbx rm` 等基础操作，但最终实现应以目标开发机上的 `sbx --help` 和 `sbx run --help` 为准。

如果目标开发机没有 `sbx`，确认 contract 的 agent 应先尝试安装：`brew trust docker/tap`、`brew install docker/tap/sbx`。安装后运行 `sbx login`。若登录打开浏览器、要求 Docker 账号、要求选择默认 network policy 或需要系统权限且无法由 agent 完成，应把具体阻塞点写入 issue comment 并标记 `needs-info`；不能仅因为 `sbx` 缺失就停止。

Clash Verge Rev 的 TUN 开关不是单一系统事实。源码中前端会更新 `enable_tun_mode`，增强配置流程会把它映射到最终 mihomo 配置的 `tun.enable`，mihomo 的 TUN 配置还包含 `device`、`auto-route`、`auto-detect-interface`、`strict-route` 等字段。MVP 不应只读取一个开关值来判断可用性，而应把“配置上已开启”和“系统路由实际可用”分开检测。

本 PRD 对应的初始决策图位于 `docs/decision-maps/safe-claude-sbx.md`。
