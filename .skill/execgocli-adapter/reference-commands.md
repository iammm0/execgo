# execgocli 子命令、参数、退出码

**源码：** `cmd/execgocli/main.go`、`internal/execgocli/*`

## 子命令与含义

| 子命令 | 作用 |
| --- | --- |
| `help` / `-h` / `--help` | 打印内嵌 usage（到 stderr） |
| `capabilities` / `cap` | `GET /adapters/capabilities` |
| `tools` | `GET /adapters/tools` |
| `act` | `POST /adapters/actions`；`-file` 或 stdin |
| `translate` | `POST /adapters/translate`；`-file` 或 stdin |
| `submit` | `POST /tasks`；`-file` 或 stdin（模式 B） |
| `wait` | 轮询 `GET /tasks/{id}` 直至终态或超时 |
| `health` | `GET /health` |
| `ensure-running` | 探活 ExecGo；可选 compose / runtime |

## 各命令参数

- **`act` / `translate` / `submit`**
  - `-file <path>`：JSON 文件；**不设则从 stdin 读**。

- **`wait`**
  - `-task-ids`：**必填**，逗号分隔，无空格或自行 trim
  - `-timeout`：默认 `2m`；`0` 可表示不额外加 `context` 超时（依实现，通常仍受客户端 HTTP 超时约束）
  - `-interval`：轮询间隔，默认 `500ms`

- **`ensure-running`**
  - `-with-runtime`：同时探活 `execgo-runtime` 的 `GET {EXECGO_RUNTIME_URL}/readyz`
  - `-execgo-compose-dir`：覆盖 `EXECGO_COMPOSE_DIR`
  - `-runtime-image`：覆盖 `EXECGO_RUNTIME_IMAGE`
  - `-runtime-source`：覆盖 `EXECGO_RUNTIME_SOURCE`

## 环境变量（再次列出）

- `EXECGO_URL`（默认 `http://127.0.0.1:8080`）
- `EXECGO_RUNTIME_URL`（默认 `http://127.0.0.1:18080`，用于 `ensure` 与运行手册）
- `EXECGO_COMPOSE_DIR`、`EXECGO_RUNTIME_IMAGE`、`EXECGO_RUNTIME_SOURCE`

## 进程退出码

| 码 | 场景 |
| --- | --- |
| 0 | 成功；`wait` 且全终态 |
| 1 | 一般错误（I/O、HTTP 非 2xx、等） |
| 2 | 参数/未知子命令（如 `wait` 缺 `-task-ids`、全局未知子命令） |
| 3 | `wait` 结束但 **未** 全终态（含超时） |
| 4 | `ensure-running`：ExecGo 仍不可达 |
| 5 | `ensure-running` 且带 `-with-runtime`：runtime 仍不可达 |

## 构建与自测

```bash
go build -o execgocli ./cmd/execgocli
./scripts/validate-execgo-cli.sh
```
