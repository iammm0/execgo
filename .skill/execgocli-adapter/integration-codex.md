# 与 Codex / 通用「命令 + JSON」型工具对接

## 原则

- Skill **只**负责：设 `EXECGO_URL`、把 `AgentActionRequest` JSON 交给 `execgocli`、解析 JSON stdout。
- 不在 skill 中手写 `curl` 除非环境禁止落盘二进制；优先 **同一仓库构建的 `execgocli` 绝对路径**。

## 推荐命令形状

1. **拉 manifest（可缓存到会话级）**

```bash
export EXECGO_URL="${EXECGO_URL:-http://127.0.0.1:8080}"
/path/to/execgocli tools
```

2. **执行 action**（由上层生成 `request.json` 或内存管道）

```bash
/path/to/execgocli act -file /tmp/execgo-action.json
# 或
printf '%s' "$JSON_STRING" | /path/to/execgocli act
```

3. **轮询**

```bash
/path/to/execgocli wait -task-ids "$ACTION_ID" -timeout 2m
```

`ACTION_ID` 必须与 `AgentActionRequest.action_id` 及引擎分配的任务 id 一致（见主 SKILL 与集成测试行为）。

## Codex 侧配置提示（实现依平台而异）

- **工作目录**：可设为**本仓库克隆根**（含 `go.mod`），便于 `go build -o ... ./cmd/execgocli` 一次，后续复用产物。
- **环境注入**：在 skill 或 runner 模板里写 `export EXECGO_URL=...`。
- **错误展示**：`ok: false` 时向用户显示完整 `error` 对象；`act` 成功时可选持久化 `data.translation_trace` 到工作区 log 方便溯源。

## 与 HTTP 直连的取舍

- 若运行环境**禁止**子进程、只允许 HTTP，可直接调用同一 JSON 端点；此时本仓库仍以 **OpenAPI/文档** 为契约，不再经过 `execgocli` 外壳。 skill 中应**二选一**并注明。
