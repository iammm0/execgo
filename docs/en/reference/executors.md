# Executors & Parameters (Index)

ExecGo chooses an executor based on `task.type`. Each executor defines the structure of `task.params`.

Built-in executor types registered by `executor.RegisterBuiltins()`:

- `http`
- `shell`
- `file`
- `sleep`
- `dns`
- `tcp`
- `noop`

## Full details (Chinese)

- Executor system overview: [`执行器系统`](../../zh/reference/执行器系统/执行器系统.md)
- Parameters specification: [`执行参数规范`](../../zh/reference/任务%20DSL%20规范/执行参数规范/执行参数规范.md)

## Quick mapping (how to think)

- Create a `Task` with:
  - `type`: one of the executor types above
  - `params`: executor-specific JSON object
- Dependency edges in your DAG become `depends_on: [...]`
- Retry/timeout are handled by the scheduler per task (not by executors)

## Custom executors (optional)

ExecGo supports extending executors via a registry (your app registers new executors at startup). See the Chinese reference for implementation details.

