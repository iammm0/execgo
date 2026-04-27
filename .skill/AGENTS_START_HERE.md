# Agents: start here (ExecGo adapter CLI)

**Human or automated agent reading this repository:** the canonical onboarding for **Codex, Claude Code, and similar tools** is the bundled skill at:

- **[`.skill/execgocli-adapter/SKILL.md`](./execgocli-adapter/SKILL.md)**

Read that file first. It contains: build steps, environment variables, all `execgocli` subcommands, JSON output contract, how to wire thin wrappers, troubleshooting, and links to `docs/`.

**Repo role:** `execgo` = control plane. Optional `execgo-runtime` = data plane for `runtime.*` actions (separate project in the same ecosystem; see main docs).

**Do not re-implement** the HTTP protocol in the skill; shell out to `execgocli` or call the same JSON endpoints from your runtime.
