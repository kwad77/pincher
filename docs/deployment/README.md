# Deployment guides

Pincher is a single static binary that runs anywhere Go runs. The
guides in this directory cover the common managed-process shapes:
container runtime, OS service manager, Kubernetes. Pick whichever
matches your environment; nothing here is required for a quick
`pincher --http 127.0.0.1:8080` at a shell.

| Platform | Guide | When to pick this |
|---|---|---|
| Docker | [`docker.md`](./docker.md) | Sidecar in agent containers; quick local stack; CI fixtures |
| systemd (Linux user) | [`systemd.md`](./systemd.md) | Desktop / workstation install without root or containers |
| Homebrew | [`homebrew.md`](./homebrew.md) | macOS or Linux dev machines using brew already |
| Kubernetes (Helm) | [`helm.md`](./helm.md) | Multi-tenant or production-shaped infra |
| Windows service | [`packaging/windows/install-service.ps1`](../../packaging/windows/install-service.ps1) | Windows server install |
| Scoop (Windows user) | [`packaging/scoop/pincher.json`](../../packaging/scoop/pincher.json) | Windows dev machines using scoop |

For the agent-side wiring (MCP clients connecting to a running
pincher) see [`docs/tutorials/`](../tutorials/) — those guides cover
Claude Code, Cursor, Zed, VS Code Copilot, and the HTTP dashboard.

## Decision matrix

```
Are you running an LLM agent locally?
├── Yes → pick the OS service manager (systemd / Homebrew / Windows)
│         + wire the client via stdio (pincher init --target=<host>)
└── No  → are you running it in a container?
    ├── Yes → Docker for one machine, Helm for many
    └── No  → run pincher --http 127.0.0.1:8080 directly at a shell
              (perfectly fine for tinkering; not production)
```

## Common ops concerns

- **HTTP auth.** All HTTP-exposed deployments should set
  `PINCHER_HTTP_KEY`. Each guide names where the auth env file lives.
- **State directory.** SQLite DB + WAL + per-project bench results
  live under one path per deployment. Container = `/data`; systemd =
  `~/.local/state/pincher/`; Homebrew = `$(brew --prefix)/var/pincher/`;
  Helm = the configured PVC.
- **Upgrades.** The schema migrates on startup; index state survives
  binary swaps. Schema-version skew is detected on every tool call
  via `StartSchemaDriftWatcher` (#1374) — supervised deployments
  respawn the binary automatically when an older binary is pinned.
- **Observability.** Every deployment exposes `/v1/health` +
  `/v1/healthz` + `/v1/readyz`. OTLP traces export when
  `OTEL_EXPORTER_OTLP_ENDPOINT` is set (#1163). Prometheus metrics
  scrape at `/metrics` by default.

## Related

- [`packaging/README.md`](../../packaging/README.md) — every supported install path
- [`docs/troubleshooting.md`](../troubleshooting.md) — operational issues
- [`docs/release-channels.md`](../release-channels.md) — pinned vs latest channel discipline

---

_Last reviewed: v0.75 (#1334)._
