# JSON 契约速查（execgocli 与 API）

与正式分册完全对齐的权威内容在 `docs/en|zh/reference/execgo-cli-contract.md`；下表为 skill 内**字段级**备忘。

## 1. CLI stdout 统一外壳

- 成功：`{ "ok": true, "data": <object|any> }`
- 失败：`{ "ok": false, "error": { "message", "status_code?", "body?" } }`
- **代理必须**先解析 `ok`；仅依赖 HTTP 状态码不足（部分错误仍可能带 body 片段）。

## 2. `AgentActionRequest`（`act` / `translate` 的 body）

与 `pkg/adapter/adapter.go` 一致。常用字段：

| 字段 | 类型 | 说明 |
| --- | --- | --- |
| `adapter` | string | 如 `codex` / `claudecode` / 留空，用于审计 |
| `agent_id` | string | 代理实例标识 |
| `session_id` | string | 会话 |
| `action_id` | string | 本次 action id；**通常与生成任务图内顶点的 id 规则对齐**，供 `wait` 使用 |
| `action` | object | 必有；含 `kind`、`input` 等 |
| `action.kind` | string | 如 `os.noop`、`runtime.command`；可经 adapter 归一化别名 |
| `action.input` | JSON | 随 kind 变化 |
| `metadata` | map | 选填；进入 trace/annotations 策略见 adapter |

## 3. 成功时 `act` 的 `data`（与 HTTP 202 体一致）

- `accepted`：接受的任务数
- `task_ids`：与提交图对应的 id 列表
- `task_graph`：翻译后的图（可审计）
- `translation_trace`：翻译/归一化痕迹

## 4. `tools` 的 `data`

- `schema_version`：应为 `adapter.v1`
- `tools`：列表项含 `name`、`action_kind`、`category`、`input_schema`（可能为简化的 object schema）、`aliases` 等

## 5. `wait` 的 `data`

- `tasks`：每轮全量拉取的 `GET /tasks/{id}` 的 JSON 对象列表（字段与 `pkg/models.Task` 一致，含 `status`、`result` 等）
- `all_terminal`：若全部为 `success` / `failed` / `skipped` 则为 `true`
- `deadline_rfc3339`：若设了 `wait` 的超时上下文则有

## 6. 模式 B：`submit`（`POST /tasks`）

- Body 为 **TaskGraph**：`{ "tasks": [ { "id", "type", ... } ] }`（字段以 `docs/reference/task-dsl` 与校验为准）。

## 7. 示例文件

见同目录 `examples/*.json`（可 `execgocli act < examples/...` 形式使用）。
