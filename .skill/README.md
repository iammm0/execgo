# ExecGo `.skill` 包（给 Codex / Claude Code / 自动化代理）

本目录为**仓库内嵌技能说明**：将本路径或仓库地址交给 Codex、Claude Code、或其他支持「读仓库内文档/技能」的代理时，请让对方**从下列顺序阅读**。

## 请从此开始（必读顺序）

1. **[execgocli-adapter/SKILL.md](./execgocli-adapter/SKILL.md)** — 主技能文件（YAML 头 + 全量操作说明：环境、子命令、JSON 外壳、集成方式、排错、验收）。
2. **[execgocli-adapter/reference-json-contract.md](./execgocli-adapter/reference-json-contract.md)** — 请求/响应与 `AgentActionRequest` 字段级说明。
3. **[execgocli-adapter/reference-commands.md](./execgocli-adapter/reference-commands.md)** — 子命令、参数、环境变量、退出码速查表。
4. **[execgocli-adapter/integration-codex.md](./execgocli-adapter/integration-codex.md)** — 如何把 `execgocli` 接到 Codex 类工具/流水线。
5. **[execgocli-adapter/integration-claude-code.md](./execgocli-adapter/integration-claude-code.md)** — 如何把本仓库与 Claude Code、Cursor 项目技能联用。

## 一句话定位

- **控制面**：`cmd/execgo` 提供 `GET/POST /adapters/*` 与 `GET /tasks/{id}`。
- **薄 CLI**：`cmd/execgocli` 仅标准库 HTTP，stdout 为稳定 JSON 外壳 `{ "ok", "data", "error" }`。
- **模式 A**（推荐）：`tools` → `act` → `wait`；**模式 B**：`translate` / `submit`（`POST /tasks`）。

## 与官方文档的关系

- 更正式的英文/中文分册在 `docs/en/`、`docs/zh/`（见 SKILL 内「仓库内链接」表）。
- 本 `.skill` 目录侧重 **代理可执行、可复制的 SOP**；不替代 `AGENT.md` 的仓库约定。

## 机器可定位标记

- 本包关键词：`execgocli`, `adapter.v1`, `EXECGO_URL`, `POST /adapters/actions`.
