# 与 Claude Code、Cursor 项目技能联用

## 1. 仓库内自描述入口

- 先读 [`.skill/README.md`](../README.md) 与 [`.skill/execgocli-adapter/SKILL.md`](./SKILL.md)。
- 若代理仅扫描英文入口：[`../AGENTS_START_HERE.md`](../AGENTS_START_HERE.md)。

## 2. Cursor 项目技能（`.cursor/skills`）

本仓库在 **`.skill/execgocli-adapter/SKILL.md`** 已按「目录 + `SKILL.md`」组织。在 Cursor 中若希望**自动加载**为项目技能，可二选一：

- **复制**：将本目录复制到项目 `.cursor/skills/execgocli-adapter/`（与 Cursor 约定一致），或
- **符号链接**（需仓库允许）：`.cursor/skills/execgocli-adapter` → 仓库内 `.skill/execgocli-adapter`（在支持 symlink 的 clone 下可用）。

**注意**：具体 Cursor 版本对项目技能的发现路径以官方文档为准；本说明不替代 Cursor 设置。

## 3. 操作步骤（与 Codex 相同）

- 在仓库根 `go build -o execgocli ./cmd/execgocli`。
- 设置 `EXECGO_URL`。
- 用 `tools` / `act` / `wait` 流水线（见主 `SKILL.md`）。

## 4. 与 `AGENT.md` 的关系

- 本仓库 `AGENT.md` 给**写代码的代理**看（测试位置、不破坏 core 等约定）。
- `.skill` 给**要操作 ExecGo/adapter 的代理或人**看（运行时命令与 JSON）。

## 5. 引用官方文档的相对路径

从仓库根：

- `docs/zh/integration/mode-a-cli.md`
- `docs/zh/reference/execgo-cli-contract.md`
- `docs/examples/execgocli-agent-wrappers.md`
