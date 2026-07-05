---
name: tea
description: TEA，Testing Engineering Architect。用于决定测什么、在哪层测、哪些边界不能 mock、如何用测试保护公开行为、Interface、Seam、Adapter、Contract、回归修复和 CI 质量门禁。默认输出精简测试判断，不写完整测试理论。
---

# tea

用 TEA 做测试架构判断。目标是用最轻的可靠测试保护真实风险，而不是追求更多测试或覆盖率数字。

## 默认输出

优先用短清单或小表格：

- 行为/风险：要保护的外部行为、契约或回归点。
- 测试层级：unit / integration / contract / E2E / gate，选择理由一句话。
- 边界：什么可以 mock/fake/stub，什么不能 mock。
- 验证：应运行或已运行的命令/证据；不能运行就说明缺口。
- 停止规则：如果需要改变生产架构、公共契约、依赖或 CI 策略，先报告风险。


## 渐进式加载 / 奥卡姆剃刀

`SKILL.md` 只保留测试决策接口；reference/script 只在真实重复、可复用、且不是每次都需要时添加。不要为了整理测试理论而新增文件、工具、层级或流程。

## 决策规则

- 以公开行为和契约为测试表面；不要测试私有 helper、临时内部拆分或 mock 调用次数。
- 按风险选最低足够层级：纯业务规则用 unit；模块协作用 integration；API/event/tool/provider/schema 边界用 contract；关键用户链路才用 E2E。
- Adapter 测试应证明外部协议与内部模型的转换；业务测试不应理解 provider、transport、storage、framework、tool 细节。
- 回归测试先复现已观察 bug，再通过真实公开表面证明修复。
- 质量门禁沿用仓库已有 lint/typecheck/test/contract/smoke/architecture check；只补当前风险需要的缺口。

## Mock 边界

不要 mock：当前要验证的 contract、Adapter 转换逻辑、关键集成边界、公共行为本身。

可以 mock/fake/stub：外部不稳定或高成本系统、难构造失败场景、非当前目标边界、慢或 flaky 的 provider；但关键假设应另有 contract/integration 覆盖。

## Contract 判断

Contract definition 优先沿用项目已有格式：OpenAPI、AsyncAPI、JSON Schema、Zod/TypeScript type、Protobuf、schema YAML、custom typed contract 等。

非 HTTP 边界也算 contract：CLI/tool JSON、stdin/stdout、exit code、stderr、event/message、provider/tool protocol。默认先用 schema validation、API/CLI/integration test 或项目已有验证方式；只有需要 consumer-driven contract testing 时才考虑 `pact` skill。

## 专业 skill 路由

- Playwright 测试写法、locator、fixture、auth、trace：用 `playwright-core`。
- 终端浏览器自动化、截图/视频/trace、Electron 证据：用 `playwright-cli`。
- Playwright CI/CD 配置：仅项目已有 CI 或用户要求时用 `playwright-ci`。
- Pact consumer/provider/Broker/can-i-deploy：仅确实需要 consumer-driven contract testing 时用 `pact`。

## 禁止事项

不要为了测试方便拆浅生产模块、不要让 E2E 覆盖所有细节、不要引入无必要依赖或工具、不要在未运行或未说明验证缺口时声称完成。
