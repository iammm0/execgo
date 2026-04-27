# ExecGo 文档（中文）

本目录面向三类读者：

- 上层编排层/Agent 开发者：把你的工作流（DAG）映射为 ExecGo 的 `TaskGraph` 并正确处理失败与重试语义
- 运维/平台工程师：在你自己的 Docker Compose 或 Kubernetes 集群中部署 ExecGo（含持久化与健康检查）
- 客户端开发者：用 Go/Java/Python 通过 HTTP 接入 ExecGo（提交任务图、轮询状态、读取结果）

如果你已经读过根目录 `README.md` 的“快速开始”，建议从下方对应章节继续。

## 文档导航

### 0) 项目定位与路线
- [Agent-First 执行内核路线图](./agent-kernel-roadmap.md)

### 1) 上层编排层如何采用 ExecGo
- [Orchestrator：上层如何映射 DAG -> TaskGraph](./orchestrator/README.md)

### 2) 部署到 Docker Compose / Kubernetes
- [Deploy：Docker Compose](./deploy/compose.md)
- [Deploy：Kubernetes](./deploy/kubernetes.md)

### 3) 多语言 HTTP 接入示例（Go/Java/Python）
- [Integration：成熟 Agent Adapter 接入](./integration/agent-adapter.md)
- [Integration：HTTP API 入门使用文档](./integration/http-api-getting-started.md)
- [Integration：Go 示例](./integration/client-go.md)
- [Integration：Java 示例](./integration/client-java.md)
- [Integration：Python 示例](./integration/client-python.md)
- [Integration：Node.js + TypeScript 示例](./integration/client-nodejs-ts.md)

### 4) 参考手册（API / Task DSL / 执行器参数等）
- [Reference：API 端点与错误语义](./reference/api.md)
- [Reference：Task DSL（任务模型与校验）](./reference/task-dsl.md)
- [Reference：执行器参数与内置执行器](./reference/executors.md)

### 5) 常见疑惑（FAQ）
- [FAQ：使用者疑惑导览](./faqs.md)

### 6) 发布说明（Release Notes）
- [v1.0.0 发布说明](./releases/v1.0.0.md)

## 版本与兼容性

文档内容以当前仓库版本的 ExecGo 行为为准。若你升级 ExecGo 版本，建议优先阅读对应的 `TaskGraph` 提交/校验规则与执行器参数变更说明。
