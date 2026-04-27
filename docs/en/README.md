# ExecGo Docs (English)

This documentation is intended for three audiences:

- Orchestrator / Agent developers: map your workflow (DAG) into ExecGo `TaskGraph`, and handle failure + retry semantics correctly
- Platform / Ops engineers: deploy ExecGo into your own Docker Compose or Kubernetes cluster (including persistence and health checks)
- Client developers: integrate with ExecGo via HTTP using Go/Java/Python (submit graphs, poll status, read results)

If you already read the root `README.md` quick start, continue from the sections below.

## Navigation

### 1) How an orchestration layer adopts ExecGo
- [Orchestrator：DAG -> TaskGraph mapping guide](./orchestrator/README.md)

### 2) Deploy to Docker Compose / Kubernetes
- [Deploy：Docker Compose](./deploy/compose.md)
- [Deploy：Kubernetes](./deploy/kubernetes.md)

### 3) Multi-language HTTP integration examples (Go/Java/Python)
- [Integration：Mature agent adapter](./integration/agent-adapter.md)
- [Integration：Go example](./integration/client-go.md)
- [Integration：Java example](./integration/client-java.md)
- [Integration：Python example](./integration/client-python.md)
- [Integration：Node.js + TypeScript example](./integration/client-nodejs-ts.md)

### 4) Reference (API / Task DSL / executor parameters)
- [Reference：API endpoints & errors](./reference/api.md)
- [Reference：Task DSL (task model & validation)](./reference/task-dsl.md)
- [Reference：executor parameters & built-ins](./reference/executors.md)

### 5) FAQ
- [FAQ：User questions index](./faqs.md)

## Versioning

Docs reflect the current repository behavior. When upgrading ExecGo, double-check `TaskGraph` submission/validation and executor parameter compatibility.
