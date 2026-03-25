# ExecGo — Minimal AI Execution Engine

> 极简 AI 执行引擎 | A production-grade single-node execution kernel for AI agents

[![Go](https://img.shields.io/badge/Go-1.22+-00ADD8?logo=go)](https://go.dev)
[![License](https://img.shields.io/badge/License-MIT-green.svg)](LICENSE)
[![Zero Dependencies](https://img.shields.io/badge/Dependencies-zero-brightgreen.svg)]()

---

## 简介 | Overview

**ExecGo** 是一个使用纯 Go 标准库构建的极简 AI 执行引擎，零第三方依赖。它作为 AI Agent（如 secbot）的执行层，通过 HTTP API 暴露任务提交与管理能力。

**ExecGo** is a minimal AI execution engine built with pure Go standard library — zero third-party dependencies. It acts as an execution layer for AI agents (e.g., secbot), exposing an HTTP API for task submission and management.

### 核心特性 | Key Features

| 特性 Feature | 描述 Description |
|---|---|
| **Task DSL** | 严格的任务契约：支持 id, type, params, depends_on, retry, timeout |
| **DAG Scheduling** | 基于依赖图的任务编排，Kahn 算法环检测 |
| **Concurrent Execution** | goroutine + channel 并发模型，信号量控制最大并发 |
| **Pluggable Executors** | HTTP / Shell / File 内置执行器，注册表机制易于扩展 |
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
│ HTTP     │ Shell    │ File     │ ... (extensible)   │
│ Executor │ Executor │ Executor │                    │
├──────────┴──────────┴──────────┴────────────────────┤
│              State Manager                          │
│        map[string]*Task + sync.RWMutex              │
│          ↕ JSON file persistence                    │
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

## 项目结构 | Project Structure

```
execgo/
├── cmd/
│   └── execgo/
│       └── main.go              # 入口 / entry point
├── internal/
│   ├── api/
│   │   └── handler.go           # HTTP API 层 / HTTP API layer
│   ├── config/
│   │   └── config.go            # 配置管理 / configuration
│   ├── executor/
│   │   ├── executor.go          # 执行器接口与注册表 / executor interface & registry
│   │   ├── http.go              # HTTP 执行器 / HTTP executor
│   │   ├── shell.go             # Shell 执行器(白名单) / Shell executor (whitelisted)
│   │   └── file.go              # 文件执行器 / File executor
│   ├── models/
│   │   └── task.go              # Task DSL 与核心类型 / Task DSL & core types
│   ├── observability/
│   │   └── observability.go     # 日志、追踪、指标 / logging, tracing, metrics
│   ├── scheduler/
│   │   └── scheduler.go         # DAG 调度器 / DAG scheduler
│   └── state/
│       └── state.go             # 状态管理与持久化 / state management & persistence
├── data/                        # 持久化数据目录 / persistence data directory
├── go.mod
└── README.md
```

---

## Task DSL 规范 | Task DSL Specification

```json
{
  "id":         "unique-task-id",
  "type":       "http | shell | file",
  "params":     { /* 类型相关参数 / type-specific params */ },
  "depends_on": ["other-task-id"],
  "retry":      3,
  "timeout":    5000,
  "status":     "pending | running | success | failed | skipped"
}
```

### 内置执行器参数 | Built-in Executor Params

**HTTP Executor:**
```json
{"url": "https://example.com", "method": "GET", "headers": {"Authorization": "Bearer xxx"}, "body": "..."}
```

**Shell Executor (白名单命令 | whitelisted commands):**
```json
{"command": "echo", "args": ["hello", "world"], "dir": "/tmp"}
```

允许的命令 | Allowed commands: `echo, cat, ls, date, whoami, hostname, uname, pwd, curl, wget, ping, dig, grep, awk, sed, head, tail, wc, sort, uniq, find, dir, where, type`

**File Executor:**
```json
{"action": "read | write | append | delete | stat", "path": "/tmp/data.txt", "content": "..."}
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

添加自定义执行器只需实现 `Executor` 接口并注册：

Adding a custom executor requires implementing the `Executor` interface and registering it:

```go
type MyExecutor struct{}

func (e *MyExecutor) Type() string { return "my_type" }

func (e *MyExecutor) Execute(ctx context.Context, task *models.Task) (json.RawMessage, error) {
    // 你的逻辑 / your logic
    return json.Marshal(map[string]any{"result": "ok"})
}

// 注册 / Register
func init() {
    executor.Register(&MyExecutor{})
}
```

---

## 设计原则 | Design Principles

1. **零依赖** | Zero dependencies — 纯 Go 标准库，无供应商锁定
2. **分层架构** | Layered architecture — API → Scheduler → Executor → State
3. **并发安全** | Concurrency safe — `sync.RWMutex` + channel 保护所有共享状态
4. **可扩展** | Extensible — 注册表模式，添加新执行器无需修改核心代码
5. **可观测** | Observable — 结构化日志 + traceID + 指标端点
6. **韧性** | Resilient — 重试、超时、崩溃恢复、优雅关闭

---

## 许可证 | License

MIT
