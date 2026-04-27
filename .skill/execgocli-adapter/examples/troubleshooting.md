# 排错速查表（`execgocli` / adapter）

| 现象 | 可能原因 | 处理 |
| --- | --- | --- |
| `connect: connection refused` | ExecGo 未监听 | 启动 `cmd/execgo`；检查 `EXECGO_URL` 端口 |
| `ok: false`，HTTP 4xx，body 含 `invalid` | JSON 与 `action.kind` 不合法 | 对照 `docs/zh/integration/agent-adapter.md` 与 `GET /adapters/capabilities` 的 action_kinds |
| `act` 成功但 `wait` 一直 pending | 调度/并发满或依赖未就绪 | 看 ExecGo 日志与 `GET /tasks/{id}` 的 `status` |
| `runtime.*` 失败或卡住 | 未起 `execgo-runtime` 或 URL 错 | 在**运行 execgo 的进程**里设 `EXECGO_RUNTIME_URL`；`curl {url}/readyz` |
| `ensure-running` 起不来 | 无 Docker/compose 或端口占用 | 读返回 JSON 的 `manual_hints`；手动起二进制 |
| 中文乱码在终端里 | 终端编码 | UTF-8；Windows 可 `chcp 65001` 试验 |

**验收：** `./scripts/validate-execgo-cli.sh`；对运行中的服务设 `EXECGO_URL` 可多做 smoke。
