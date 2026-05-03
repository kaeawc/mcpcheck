# mcpcheck

Static validator for MCP (Model Context Protocol) servers and tool-use
schemas. Go-first, tree-sitter Python + TypeScript intake (planned),
single-pass rule dispatcher, multi-frontend output (text, JSON, SARIF,
LSP, MCP), capability-gated rules, autofix tiers.

Architecture mirrors `~/kaeawc/krit` — read krit's `AGENTS.md` for the
prior art it's modeled on.

## Working Rules

- Keep analyzer and rule work in Go. Use the standard library where
  possible; only add dependencies for clear value.
- New rules go in `internal/rules/<name>.go` and register themselves
  with `v2.Register` from init.
- New CLI entry points go under `cmd/<name>/`.
- Internal packages (not part of any public API) live under `internal/`.
- Filesystem writes that must survive crashes go through
  `internal/fsutil.WriteFileAtomic`.
- Time, randomness, env, subprocess, filesystem: inject via the
  `internal/{clock,random,env,proc,vfs}` interfaces. The fakes live next
  to the real implementations and exist precisely so analyzer code stays
  testable.

## Build & Validate

```bash
go build -o mcpcheck ./cmd/mcpcheck/   # Build the CLI
go vet ./...                            # Static analysis
go test ./... -count=1                  # Full test suite
make ci                                 # vet + test + complexity + lint + security + licenses
```

After any implementation change, run `go build ./... && go vet ./...`.
Use focused package tests while iterating:
`go test ./internal/rules/ -run TestPositiveFixtures -v`.

## Git

- Use branch prefix `work/` for agent-created branches.
- Never push to `main` directly; always open a PR.

## Project Map

### Analyzer

- `cmd/mcpcheck/` — CLI entry point.
- `internal/mcpmodel/` — normalized `Tool` / `ToolSet` types passed
  between intake and rules.
- `internal/scanner/` — intakes. JSON intake (`LoadToolsJSON`) reads an
  MCP `tools/list` response or a bare list of tool objects from disk.
  Future: tree-sitter Python and TypeScript intakes; live-mode intake
  that boots an MCP server in a subprocess and lists its tools over
  stdio.
- `internal/v2/` — rule registry. Defines `Rule`, `Context`, `Finding`,
  `Severity`, `Category`, `FixLevel`, plus `Register`, `All`, `Run`.
  Rules call `v2.Register(&v2.Rule{...})` from `init`.
- `internal/rules/` — rule implementations. One file per rule, plus a
  shared positive/negative fixture driver in `fixtures_test.go`. The
  package's side-effect import is what loads the registry.
- `internal/output/` — text and JSON formatters for findings.
- `tests/fixtures/{positive,negative}/<rule-id>/tools.json` — per-rule
  fixtures the shared driver runs against.

### Shared plumbing (from the `golang-build` template)

- `internal/fsutil/` — atomic filesystem helpers (`WriteFileAtomic`).
- `internal/clock/` — `Clock` interface, `System` impl, `Fake` for
  deterministic time tests.
- `internal/proc/` — `Runner` interface over `os/exec` plus `Fake` with
  scripted matchers. Live-mode subprocess MCP intake will run through
  this.
- `internal/env/` — `Reader` interface for env vars (distinguishes
  unset vs empty), `OS` and `Map` impls, typed getters.
- `internal/idgen/` — `Generator` interface; `UUID` and `Sequence`
  (deterministic counter for tests).
- `internal/vfs/` — `FS` interface; `OS` impl plus `Mem` in-memory fake.
  Shared contract test keeps the fake faithful to OS behavior.
- `internal/random/` — `Random` interface; `Crypto` and `Seeded`
  (deterministic PCG) impls.
- `internal/retry/` — exponential-backoff `Executor` with optional
  jitter; takes a `Sleeper` (`Real` or `Instant` for tests).
- `internal/httpx/` — `Client` interface for outbound HTTP; `Real` over
  `net/http`, `Fake` with scripted responses.
- `internal/shutdown/` — `Coordinator` runs registered hooks LIFO with
  per-hook timeout on SIGINT/SIGTERM.
- `internal/cacheutil/` — building blocks for on-disk caches
  (`ShardedEntryPath`, `VersionedDir`, `AsyncWriter`,
  `EncodeZstdGob`/`DecodeZstdGob`).
- `internal/errgroup/` — bounded-concurrency fan-out modeled on
  `golang.org/x/sync/errgroup`.
- `internal/scheduler/` — in-process job scheduler with deterministic
  test entry point (`RunDue` after `clock.Fake.Advance`).
- `internal/eventbus/` — generic in-process pub/sub (`Sync` and `Async`).
- `internal/limiter/` — bounded-concurrency `Semaphore` with stats.
- `internal/kv/` — generic in-memory `Store[V]` with optional TTL.
- `internal/ratelimit/` — token-bucket `Bucket` and test-controlled
  `Manual` limiters.
- `internal/circuitbreaker/` — circuit-breaker for outbound calls.
- `internal/logger/` — `Logger` over `log/slog`, plus `Capture` (for
  tests) and `Discard`.
- `internal/perf/` — local-only timing tracker (no OpenTelemetry, no
  exporter). Designed for CLI tools that print their own perf summary
  at exit.
- `internal/tokens/` — JWT-compatible signed tokens. Carry-over; trim
  if the analyzer never needs it.
- `ci/Dockerfile` — multi-stage build (compile in `golang:alpine`, copy
  to `scratch`).
- `scripts/` — `release-check.sh`, `validate-workflows.sh`.

## CI

`.github/workflows/commit.yml` runs on every push and PR:
`compile-binary`, `test`, `complexity`, `static-analysis`,
`check-licenses`, `security`, `codeql-analysis`. Go version comes from
`go.mod`.

## Conventions

- Errors: wrap with `fmt.Errorf("context: %w", err)` so callers can
  `errors.Is`/`errors.As`.
- Comments: only when the *why* is non-obvious. Don't restate what the
  code does.
- Tests: live next to the code as `_test.go`. Use `t.TempDir()` for
  filesystem fixtures.
- Time, randomness, env, subprocess, filesystem: inject via the
  `internal/{clock,idgen,random,env,proc,vfs}` interfaces. Never call
  `time.Now()`, `os.Getenv`, `os/exec`, or raw `os.ReadFile` from code
  paths that need to be testable. Tests substitute the `Fake`/`Map`/
  `Mem` implementations.
