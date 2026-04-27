---
name: execgocli-adapter
description: >-
  Operate ExecGo's mature-agent adapter via the stdlib CLI execgocli: discover tools (GET /adapters/tools),
  submit actions (POST /adapters/actions), poll tasks, optional ensure-running for Docker/runtime.
  Use when the repo is github.com/iammm0/execgo or a fork containing cmd/execgocli and the user mentions
  Codex, Claude Code, EXECGO_URL, adapter.v1, or /adapters/actions.
---

# ExecGo `execgocli` Adapter Skill（详细 SOP）

本技能描述如何在**本仓库根目录**（`execgo` 模块根）构建并使用 `execgocli`，通过 **模式 A**（adapter 翻译并执行）或 **模式 B**（直传 TaskGraph）驱动 ExecGo。**不要**在 skill 内重复实现 HTTP；只配置环境、拼 JSON、调用 CLI 或等价 HTTP。

## 1. 你在本仓库里要认出的东西

| 路径 | 作用 |
| --- | --- |
| `cmd/execgo` | 控制面 HTTP 服务（`/adapters/*`, `/tasks`, `/health`） |
| `cmd/execgocli` | 给 Codex / CC 用的薄 CLI，仅标准库 |
| `internal/execgocli` | CLI 的 HTTP/轮询/ensure 实现 |
| `pkg/adapter` | `adapter.v1` 与 `AgentActionRequest` 结构 |
| `docs/zh/reference/execgo-cli-contract.md` | CLI JSON 契约（中） |
| `docs/en/reference/execgo-cli-contract.md` | 同上（英） |
| `docs/zh/integration/mode-a-cli.md` | 模式 A 快速开始（中） |
| `docs/examples/execgocli-agent-wrappers.md` | 薄封装思路 |

## 2. 前置条件

- Go 工具链可构建本仓库（见根目录 `go.mod`）。
- 运行 `execgocli` 前，**通常**需要已可访问的 ExecGo 进程，除非你只用 `ensure-running` 去尝试本机 `docker compose` 拉起（见第 7 节）。

## 3. 构建

在仓库根（含 `go.mod`）执行：

```bash
go build -o execgocli ./cmd/execgocli
go build -o execgo ./cmd/execgo
```

将 `execgocli` 放入 `PATH` 或技能里写**绝对路径**。

## 4. 启动控制面（execgo）

```bash
./execgo
# 或指定端口
./execgo -addr :8080
```

- 环境变量以 `pkg/config` 为准：常见 `EXECGO_ADDR`、数据目录 `EXECGO_DATA_DIR` 等。
- **使用 `runtime.*` 类 action 时**，ExecGo 进程需能访问 `execgo-runtime`：在**运行 execgo 的 shell** 中设置 `EXECGO_RUNTIME_URL`（与下文 CLI 的 `EXECGO_RUNTIME_URL` 探活目标一致，常见本机为 `http://127.0.0.1:18080` 以免与 8080 控制面抢端口）。

## 5. 环境变量（execgocli）

| 变量 | 典型值 | 含义 |
| --- | --- | --- |
| `EXECGO_URL` | `http://127.0.0.1:8080` | 控制面基址，**必填语义上强烈建议**显式设置 |
| `EXECGO_RUNTIME_URL` | `http://127.0.0.1:18080` | 仅 `ensure-running -with-runtime` 或文档默认；**须与 execgo 进程内 runtime 地址一致** |
| `EXECGO_COMPOSE_DIR` | 本仓库根绝对路径 | `ensure-running` 可尝试 `docker compose -f docker-compose.yml up -d` |
| `EXECGO_RUNTIME_IMAGE` | 例如本地 build 的 tag | 非空时 `ensure-running` 或尝试 `docker run` 拉 runtime（见实现与文档） |
| `EXECGO_RUNTIME_SOURCE` | `execgo-runtime` 克隆路径 | 失败提示中的 **cargo** 说明会引用 |

## 6. 稳定 JSON 外壳（所有子命令 stdout）

成功：

```json
{ "ok": true, "data": { } }
```

失败：

```json
{ "ok": false, "error": { "message": "…", "status_code": 400, "body": "…" } }
```

**解析规则：** 先判断 `ok`；`false` 时向用户展示 `error.message` 与 `error.body`（若有）。

## 7. 子命令与 HTTP 映射（必背）

| CLI | HTTP |
| --- | --- |
| `execgocli capabilities` | `GET /adapters/capabilities` |
| `execgocli tools` | `GET /adapters/tools` |
| `execgocli act` | `POST /adapters/actions`；body 为 **stdin** 或 `-file` |
| `execgocli translate` | `POST /adapters/translate` |
| `execgocli submit` | `POST /tasks`（模式 B，TaskGraph JSON） |
| `execgocli wait -task-ids id1,id2` | 轮询 `GET /tasks/{id}` 直至终态 |
| `execgocli health` | `GET /health` |
| `execgocli ensure-running` | 本地探活/可选 docker；`--with-runtime` 时探 `{EXECGO_RUNTIME_URL}/readyz` |

`act` / `translate` / `submit` 的 JSON 从 **stdin** 读：管道或 here-doc 均可。

## 8. 模式 A：推荐流水线（给代理逐步执行）

1. `export EXECGO_URL=http://127.0.0.1:8080`（或实际地址）。
2. `execgocli tools` → 从 `data.tools[]` 取 `action_kind` / 名称，生成 **AgentAction**。
3. 构造 **AgentActionRequest**（见本目录 `reference-json-contract.md`），`action_id` 建议与任务 id 规则一致且唯一。
4. `echo '<JSON>' \| execgocli act` → 从 `data.task_ids` 取 id。
5. `execgocli wait -task-ids <id> -timeout 2m` → 确认 `all_terminal` 为 true 或查 `data.tasks[].status`（`success` / `failed` / `skipped`）。

## 9. 模式 B（简讯）

- `execgocli translate`：只拿 `task_graph`，不执行。
- `execgocli submit`：直传 `TaskGraph` 到 `POST /tasks`。

## 10. 最小可运行 JSON 示例（`act`）

```json
{
  "adapter": "codex",
  "agent_id": "agent-1",
  "session_id": "sess-1",
  "action_id": "demo-1",
  "action": {
    "kind": "os.noop",
    "input": {}
  },
  "metadata": {
    "source": "skill-example"
  }
}
```

`action_id` 常映射为图内 task id，以便 `wait` 使用同一 id（与引擎行为一致，详见 adapter 与集成测试）。

## 11. 与 Codex / Claude Code 的接法（原则）

- **不要**在 skill 里再写一版 REST 客户端。
- Skill **只做**：设置 `EXECGO_URL`、把上述 JSON 交给 `execgocli act` 的 **stdin**、读 stdout 的 JSON、错误时回显、可选把 `translation_trace` 落盘。
- 具体命令行包装样本见同目录 `integration-codex.md` 与 `integration-claude-code.md`。

## 12. 验收脚本（本仓库）

```bash
./scripts/validate-execgo-cli.sh
# 对**已运行**的 ExecGo 可设置 EXECGO_URL 多跑一段 smoke
EXECGO_URL=http://127.0.0.1:8080 ./scripts/validate-execgo-cli.sh
```

## 13. 安全与治理（推广期默认）

- `os.shell` 使用白名单/允许列表为默认更安全的姿态；开放策略需显式环境开关（以仓库实现与 `docs/zh/reference/promotion-security.md` 为准）。
- 请求中尽量带齐 `agent_id` / `session_id` / `action_id` / `metadata` 以便审计与 `translation_trace` 对齐。
- 需更强隔离时用 `runtime.command` / `runtime.script` 并保证 `execgo-runtime` 可达。

## 14. 退出码（`execgocli`）

- `0` 成功。`wait` 在超时且未全终态时可能非 0（见 `reference-commands.md`）。
- `2` 用法/未知子命令。
- 其他非零见参考页或 `docs/.../execgo-cli-contract.md`。

## 15. 故障排查速查

| 现象 | 处理 |
| --- | --- |
| `ok: false` 且 4xx | 读 `error.body`；多为 JSON 字段或 `action.kind` 非法 |
| `connection refused` | ExecGo 未起或 `EXECGO_URL` 错 |
| `runtime` 类一直 pending / 失败 | 检查 `EXECGO_RUNTIME_URL` 在 **execgo 进程** 与 **runtime 实际监听** 一致；`curl …/readyz` |
| `ensure-running` 失败 | 看返回 JSON 的 `manual_hints`；无 Docker/无权限/端口占用 |

更细表见同目录 `../` 下可扩展的 troubleshooting（若你 fork 可自建）；当前以主文档与上表为准。

---

**Maintainers:** 更新 CLI 行为时请同步修改 `reference-commands.md` 与本 SKILL 的表格；并更新 `docs/en|zh/reference/execgo-cli-contract.md`。
