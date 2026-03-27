# ExecGo — Minimal AI Execution Engine

> 极简 AI 执行引擎 | A production-grade single-node execution kernel for AI agents

[![Go](https://img.shields.io/badge/Go-1.22+-00ADD8?logo=go)](https://go.dev)
[![License](https://img.shields.io/badge/License-MIT-green.svg)](LICENSE)
[![Core deps](https://img.shields.io/badge/Core%20module-stdlib%20only-blue.svg)]()

---

## 简介 | Overview

**ExecGo** 的核心模块（`github.com/iammm0/execgo`）仅依赖 Go 标准库；可选能力（SQLite 持久化、Redis 读穿缓存）放在独立子模块 `contrib/*` 中，避免强绑第三方依赖。

**ExecGo**’s core module uses only the Go standard library. Optional features (SQLite persistence, Redis read-through cache) live in separate `contrib/*` submodules so consumers can opt in without pulling drivers they do not need.

### 核心特性 | Key Features

| 特性 Feature | 描述 Description |
|---|---|
| **Task DSL** | 严格的任务契约：支持 id, type, params, depends_on, retry, timeout |
| **DAG Scheduling** | 基于依赖图的任务编排，Kahn 算法环检测 |
| **Concurrent Execution** | goroutine + channel 并发模型，信号量控制最大并发 |
| **Pluggable Executors V2** | 内置 `os` / `mcp` / `cli-skills` 三大类；`os` 内含 shell/file/dns/tcp/sleep/noop/http 工具 |
| **Retry & Timeout** | 指数退避重试 + context 超时控制 |
| **State Persistence** | 内存存储 + JSON 文件定期持久化，崩溃恢复 |
| **Observability** | 结构化 JSON 日志 (slog) + traceID 追踪 + /metrics 端点 |
| **Graceful Shutdown** | 信号监听 → HTTP 关闭 → 调度器停止 → 状态持久化 |

---

## 架构 | Architecture

```
┌─────────────────────────────────────────────────────┐
│                    AI Agent (secbot)                 │
│              POST /tasks  ←→  GET /tasks/{id}       │
└─────────────────────┬───────────────────────────────┘
                      │ HTTP/JSON
┌─────────────────────▼───────────────────────────────┐
│                   API Layer (net/http)               │
│  POST /tasks │ GET /tasks/{id} │ DELETE │ /health   │
├─────────────────────┬───────────────────────────────┤
│               Scheduler (DAG)                       │
│  readyQueue(chan) │ semaphore │ dependency counter   │
├──────────┬──────────┬──────────┬────────────────────┤
│ OS       │ MCP      │ CLI+Skill│ ... (extensible)   │
│ Category │ Category │ Category │                    │
├──────────┴──────────┴──────────┴────────────────────┤
│              Store (store.Store)                    │
│   jsonfile (default) │ sqlite │ + Redis (contrib)  │
├─────────────────────────────────────────────────────┤
│            Observability                            │
│    slog/JSON │ traceID │ /metrics                   │
└─────────────────────────────────────────────────────┘
```

---

## 快速开始 | Quick Start

### 构建与运行 | Build & Run

```bash
# 构建 / Build
go build -o execgo ./cmd/execgo

# 运行 / Run (默认监听 :8080)
./execgo

# 自定义配置 / Custom config
./execgo -addr :9090 -max-concurrency 20 -data-dir ./mydata

# 环境变量 / Environment variables
EXECGO_ADDR=:9090 EXECGO_MAX_CONCURRENCY=20 ./execgo
```

### 提交任务 | Submit Tasks

**单个任务 | Single task:**

```bash
curl -X POST http://localhost:8080/tasks \
  -H "Content-Type: application/json" \
  -d '{
    "tasks": [
      {
        "id": "check-host",
        "type": "shell",
        "params": {"command": "hostname"},
        "retry": 2,
        "timeout": 5000
      }
    ]
  }'
```

**DAG 工作流 | DAG workflow:**

```bash
curl -X POST http://localhost:8080/tasks \
  -H "Content-Type: application/json" \
  -d '{
    "tasks": [
      {
        "id": "fetch-data",
        "type": "http",
        "params": {"url": "https://httpbin.org/json", "method": "GET"},
        "timeout": 10000
      },
      {
        "id": "save-result",
        "type": "file",
        "params": {"action": "write", "path": "output.txt", "content": "fetched!"},
        "depends_on": ["fetch-data"]
      },
      {
        "id": "verify",
        "type": "file",
        "params": {"action": "read", "path": "output.txt"},
        "depends_on": ["save-result"]
      }
    ]
  }'
```

### 查询任务 | Query Tasks

```bash
# 列出所有任务 / List all tasks
curl http://localhost:8080/tasks

# 查询单个任务 / Get single task
curl http://localhost:8080/tasks/fetch-data

# 删除任务 / Delete task
curl -X DELETE http://localhost:8080/tasks/fetch-data

# 健康检查 / Health check
curl http://localhost:8080/health

# 指标 / Metrics
curl http://localhost:8080/metrics
```

---
## 文档入口 | Documentation

完整文档位于仓库下的 `docs/` 目录，包含：

- 上层编排层（Orchestrator/Agent）如何把你的 DAG 映射成 ExecGo 的 `TaskGraph`
- Docker Compose 与 Kubernetes 集群部署示范
- Go/Java/Python 的 HTTP 接入示例
- API / Task DSL / 执行器参数等参考手册

快速入口：

- 中文总入口：[`docs/zh/README.md`](docs/zh/README.md)
- English total entry：[`docs/en/README.md`](docs/en/README.md)

---

## 使用者疑惑导览（中文）

1. 我是上层编排层，应该怎么把工作流映射成 ExecGo 的 `TaskGraph`？
   见：[映射：DAG -> TaskGraph](docs/zh/orchestrator/mapping-dag-to-taskgraph.md)
2. `depends_on` 到底表达什么？为什么下游不会自动拿到上游结果？
   见：[映射：DAG -> TaskGraph](docs/zh/orchestrator/mapping-dag-to-taskgraph.md)
3. 为什么任务失败后下游会变成 `skipped`？
   见：[失败语义：failed vs skipped](docs/zh/orchestrator/failure-semantics.md)
4. 提交后如何拿到最终结果？是同步还是异步？
   见：[轮询与幂等：稳定提交与读取结果](docs/zh/orchestrator/polling-and-idempotency.md)
5. 为什么会收到 `400 Bad Request`？`TaskGraph.Validate()` 校验失败是什么意思？
   见：[Task DSL 参考（索引）](docs/zh/reference/task-dsl.md)
6. 如何设置 `retry` 和 `timeout`？
   见：[任务 DSL 参考（索引）](docs/zh/reference/task-dsl.md)
7. `result`/`error` 怎么解析、数据长什么样？
   见：[HTTP API 参考（索引）](docs/zh/reference/api.md)
8. 如何部署到自己的 Docker Compose？
   见：[Docker Compose 部署示范](docs/zh/deploy/compose.md)
9. 如何部署到 Kubernetes（Deployment/Service/PVC）？
   见：[Kubernetes 部署示范](docs/zh/deploy/kubernetes.md)
10. Kubernetes 多副本能不能 `replicas > 1`？
   见：[Kubernetes 多副本注意事项](docs/zh/deploy/kubernetes.md)
11. 我想用 Go 调用 ExecGo，怎么做？
   见：[Go（HTTP）接入示例](docs/zh/integration/client-go.md)
12. 我想用 Java 调用 ExecGo，怎么做？
   见：[Java（HTTP）接入示例](docs/zh/integration/client-java.md)
13. 我想用 Python 调用 ExecGo，怎么做？
   见：[Python（HTTP）接入示例](docs/zh/integration/client-python.md)
14. 我想用 Node.js + TypeScript 调用 ExecGo，怎么做？
   见：[Node.js + TypeScript（HTTP）接入示例](docs/zh/integration/client-nodejs-ts.md)
15. shell 执行器是否安全？怎么避免任意命令执行风险？
   见：[执行器与参数参考（索引）](docs/zh/reference/executors.md)，进一步：[Shell 执行器参数](docs/zh/reference/任务%20DSL%20规范/执行参数规范/Shell%20执行器参数.md)
16. 任务状态存储在哪里？如何持久化/恢复？
   见：[数据持久化策略](docs/zh/reference/系统架构/数据持久化策略.md)
17. 怎么扩展执行器或实现自定义执行器？
   见：[执行器与参数参考（索引）](docs/zh/reference/executors.md)
18. 遇到幂等/重复提交问题怎么办？
   见：[轮询与幂等：稳定提交与读取结果](docs/zh/orchestrator/polling-and-idempotency.md)

---

## Common Questions (English)

- See [`docs/en/faqs.md`](docs/en/faqs.md)

---
## gRPC（微服务接入）/ gRPC (Microservice Integration)

ExecGo 内置的 gRPC 服务使用 proto 路径：`execgo.v1.ExecGo`，默认监听端口 `50051`（由环境变量 `EXECGO_GRPC_ADDR` 控制；默认 `:50051`）。

### 方法 / Methods
- `SubmitTasks`：提交任务 DAG（异步执行）
- `GetTask`：查询单个任务
- `ListTasks`：列出所有任务
- `DeleteTask`：删除任务
- `Health`：健康检查
- `Metrics`：指标

### protobuf JSON 字段约定 / protobuf JSON field convention
`paramsJson` 与 `resultJson` 采用“字符串形式的 JSON”（即把原本 `params/result` 的 JSON 内容用字符串包起来）。
`timeoutMs` 单位为毫秒。

### grpcurl 示例 / grpcurl examples
（需要你本地安装 `grpcurl`；下面演示使用 plaintext）

1. 提交任务
```bash
grpcurl -plaintext localhost:50051 execgo.v1.ExecGo/SubmitTasks -d '{
  "tasks": [
    {
      "id": "check-host",
      "type": "shell",
      "paramsJson": "{\"command\":\"hostname\"}",
      "retry": 2,
      "timeoutMs": 5000,
      "dependsOn": []
    }
  ]
}'
```

2. 查询任务
```bash
grpcurl -plaintext localhost:50051 execgo.v1.ExecGo/GetTask -d '{
  "id": "check-host"
}'
```

---

## 测试 | Testing

项目采用分层测试结构：保留必要的包内测试（用于私有实现细节），并在根目录 `tests/` 下集中维护跨包测试。

```bash
# 全量（含包内 + tests 分层）
go test ./...

# 单元测试（导出 API、边界与错误路径）
go test ./tests/unit/...

# 模块测试（跨组件但非完整端到端）
go test ./tests/module/...

# 集成测试（HTTP 提交 -> 调度执行 -> 状态查询）
go test ./tests/integration/...
```

---

## 项目结构 | Project Structure

```
execgo/
├── cmd/execgo/main.go           # 默认二进制：JSON 文件存储 / default binary: JSON file store
├── pkg/
│   ├── models/                  # Task DSL / DTO（可被其他模块导入）/ importable DTOs
│   ├── store/                   # Store 接口 / store interface
│   ├── store/jsonfile/          # 默认：内存 + state.json / default persistence
│   ├── executor/                # 执行器注册表与内置实现 / executor registry
│   ├── scheduler/               # DAG 调度器（依赖 store.Store）/ DAG scheduler
│   ├── httpserver/              # Engine + 中间件链 + 路由 / HTTP engine & middleware
│   ├── config/                  # Config + Provider（类 Viper 可插拔配置源）/ pluggable config
│   └── observability/           # slog、trace、指标 / logging, trace, metrics
├── tests/
│   ├── unit/                    # 单元测试（黑盒）/ unit tests
│   ├── module/                  # 模块级测试 / module tests
│   ├── integration/             # 集成测试 / integration tests
│   └── testutil/                # 复用测试夹具 / shared test helpers
├── contrib/sqlite/              # 子模块：SQLite 版 store.Store / SQLite store submodule
├── contrib/rediscache/          # 子模块：Redis 读穿缓存装饰器 / Redis cache submodule
├── examples/fullserver/         # 子模块：组合 jsonfile|sqlite + 可选 Redis / full stack example
├── go.work                      # 仅本仓库开发用；下游项目不需要 / dev-only; consumers do not need this
├── data/
├── go.mod                       # 核心模块无第三方依赖 / core module: no third-party deps
└── README.md
```

---

## Task DSL 规范 | Task DSL Specification

```json
{
  "id":         "unique-task-id",
  "type":       "os | mcp | cli-skills",
  "tool_name":  "shell | file | dns | tcp | sleep | noop | http | ...",
  "params":     { /* 工具相关参数 / tool-specific params */ },
  "depends_on": ["other-task-id"],
  "retry":      3,
  "timeout":    5000,
  "status":     "pending | running | success | failed | skipped"
}
```

### 内置执行器参数 | Built-in Executor Params

V2 分类执行建议使用 `type=os + tool_name=*`。同时保留 legacy 输入兼容：`type=shell/file/...` 会自动映射到 `os` 分类。

**HTTP Executor:**
```json
{"url": "https://example.com", "method": "GET", "headers": {"Authorization": "Bearer xxx"}, "body": "..."}
```

**Shell Executor（跨平台双模式 | cross-platform dual modes）:**
```json
{"command": "echo", "args": ["hello", "world"], "dir": "/tmp"}
```

或脚本模式 | Script mode:
```json
{"runner": "auto", "script": "echo hello from script", "dir": "/tmp"}
```

`runner` 支持 | Supported runners: `auto | direct | powershell | cmd | sh`。
- `auto` 默认：Windows 使用 `powershell -NoProfile -NonInteractive -Command`，Linux/macOS 使用 `/bin/sh -c`。
- `script` 与 `command` 同时给出时，优先执行 `script`。
- `direct` 仅用于 `command + args` 直连执行（不是脚本 runner）。

允许的命令 | Allowed commands: `echo, cat, ls, date, whoami, hostname, uname, pwd, curl, wget, ping, dig, grep, awk, sed, head, tail, wc, sort, uniq, find, dir, where, type`

可选开放模式 | Optional open mode: 设置环境变量 `EXECGO_SHELL_POLICY=open` 后，`shell` 执行器将跳过 direct 模式的命令白名单校验；`script` 模式同样可执行任意脚本（仍受进程权限与 OS 约束）。

安全建议 | Security note: `open` 模式仅建议在**可信的 Agent 编排层 + 已有鉴权/网络隔离**场景启用；若 API 对公网暴露，不建议启用该模式。

**File Executor:**
```json
{"action": "read | write | append | delete | stat", "path": "/tmp/data.txt", "content": "..."}
```

**Sleep Executor（编排延时，可被任务 `timeout` / context 取消 | delay for orchestration, cancellable）:**
```json
{"duration_ms": 2000}
```
单次上限 | Max `duration_ms`: `600000`（10 分钟 | 10 minutes）。

**DNS Executor:**
```json
{"name": "example.com", "record": "ip"}
```
`record` 可选：`ip`（默认，A/AAAA 地址列表）、`txt`、`cname`。

**TCP Executor（端口连通性 | TCP dial probe）:**
```json
{"address": "example.com:443", "timeout_ms": 5000}
```
`timeout_ms` 可选，默认 `5000`；上限 | Max `timeout_ms`: `60000`。

**Noop Executor（占位 / 测试，无外部 IO | placeholder, no I/O）:**
```json
{"message": "optional"}
```

---

## 配置 | Configuration

| Flag | 环境变量 Env Var | 默认值 Default | 描述 Description |
|---|---|---|---|
| `-addr` | `EXECGO_ADDR` | `:8080` | HTTP 监听地址 / listen address |
| `-data-dir` | `EXECGO_DATA_DIR` | `data` | 数据目录 / data directory |
| `-max-concurrency` | `EXECGO_MAX_CONCURRENCY` | `10` | 最大并发数 / max concurrency |
| `-shutdown-timeout` | `EXECGO_SHUTDOWN_TIMEOUT` | `15` | 关闭超时(秒) / shutdown timeout (seconds) |

优先级 / Priority: `flag > env > default`

---

## 扩展 | Extensibility

### 自定义执行器 | Custom executors

```go
import (
    "context"
    "encoding/json"

    "github.com/iammm0/execgo/pkg/executor"
    "github.com/iammm0/execgo/pkg/models"
)

type MyExecutor struct{}

func (e *MyExecutor) Name() string     { return "my_type" }
func (e *MyExecutor) Category() string { return "custom" }

func (e *MyExecutor) Execute(ctx context.Context, task *models.Task) (*executor.Result, error) {
    out, _ := json.Marshal(map[string]any{"result": "ok"})
    return &executor.Result{Status: "success", Output: out}, nil
}
func (e *MyExecutor) ListTools(ctx context.Context) ([]executor.Tool, error) { return nil, nil }
func (e *MyExecutor) HealthCheck() error { return nil }
func (e *MyExecutor) Shutdown(ctx context.Context) error { return nil }

func init() {
    executor.Register(&MyExecutor{})
}
```

### MCP HTTP endpoints (V2)

- `GET /mcp/tools`: list MCP tools.
- `POST /mcp/call`: call MCP tool, returns `handle_id`.
- `GET /mcp/tasks/{id}`: poll MCP task handle status/result.

### 作为库嵌入 | Embedding as a library

1. **存储** | **Storage**：实现或使用 `pkg/store.Store`。默认使用 `pkg/store/jsonfile`。SQLite 与 Redis 见下文子模块。
2. **HTTP（类 Gin 的 Use 链）** | **HTTP (Gin-style `Use`)**：`httpserver.NewEngine(store, scheduler, metrics, logger)`，按需 `engine.Use(mw)`，`engine.Handler()`；挂载到已有 `ServeMux` 时用 `httpserver.Mount(parentMux, "/execgo", engine)`。
3. **配置（类 Viper 的 Provider）** | **Config (Viper-style `Provider`)**：`config.Load(config.NewFlagEnvProvider())` 或实现 `config.Provider`（`GetString` / `GetInt`），将 Viper / 自有配置源适配到该接口即可。

### 下游项目如何依赖 | How downstream apps depend on ExecGo

**不需要**配置 `go.work`，也**不需要**为集成 ExecGo 单独写特殊 CI 工作流。在业务项目的 `go.mod` 里按需 `require` 已发布版本（或伪版本），照常 `go build` / `go test` 即可。

**不需要** | **No need for**: `go.work`, custom pipelines just for ExecGo.

**需要** | **Typical `go.mod`**:

```go
require (
    github.com/iammm0/execgo v0.x.y                    // 核心：scheduler、httpserver、jsonfile 等
    github.com/iammm0/execgo/contrib/sqlite v0.x.y     // 可选
    github.com/iammm0/execgo/contrib/rediscache v0.x.y // 可选
)
```

仅在**同时改 ExecGo 源码与你的应用**时，才在业务 `go.mod` 里用 `replace` 指向本地路径；这与 `go.work` 无关，也不是常规集成方式。

`go.work` **仅用于 clone 本仓库**后同时开发核心、`contrib`、`examples` 多模块；维护者本地或本仓库 CI 可用，**不是**使用方的义务。

### 子模块：SQLite | Submodule: SQLite

模块路径：`github.com/iammm0/execgo/contrib/sqlite`（子目录独立 `go.mod`）。对外集成：在业务 `go.mod` 中 `require` 该模块即可；clone 本仓库做开发时可在仓库根使用 `go.work` 方便联编。

```go
import "github.com/iammm0/execgo/contrib/sqlite"

st, err := sqlite.Open("/path/to/execgo.db")
defer st.Close()
```

### 子模块：Redis 读穿缓存 | Submodule: Redis read-through cache

模块路径：`github.com/iammm0/execgo/contrib/rediscache`。包装任意 `store.Store`；`Get` 走缓存，`Put` / `UpdateStatus` / `Delete` 会失效对应键；`GetAll` 始终直读底层存储。

```go
import "github.com/iammm0/execgo/contrib/rediscache"

st = rediscache.Wrap(st, redisClient, rediscache.Options{TTL: 5 * time.Minute})
```

### 全功能示例二进制 | Full-stack example binary

在 `examples/fullserver` 子模块中：`EXECGO_STORE=sqlite` 使用 SQLite（默认路径为 `data-dir/execgo.db`，也可用 `EXECGO_SQLITE_PATH`）；设置 `EXECGO_REDIS_URL` 时在存储之上叠加 Redis 缓存。构建：

```bash
cd examples/fullserver && go build -o fullserver .
```

---

## 设计原则 | Design Principles

1. **核心无第三方依赖** | Core module uses only the standard library — optional drivers live in `contrib/*`
2. **分层架构** | Layered architecture — API → Scheduler → Executor → State
3. **并发安全** | Concurrency safe — `sync.RWMutex` + channel 保护所有共享状态
4. **可扩展** | Extensible — 注册表模式，添加新执行器无需修改核心代码
5. **可观测** | Observable — 结构化日志 + traceID + 指标端点
6. **韧性** | Resilient — 重试、超时、崩溃恢复、优雅关闭

---

## 许可证 | License

MIT
