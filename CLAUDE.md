# CLAUDE.md

Guidance for Claude Code when working on this repository.

## Project overview

mcpcheck is a Go-first static validator for MCP (Model Context Protocol)
servers. It loads tool advertisements (today: from JSON; later: by
tree-sitter intake of Python and TypeScript MCP servers), runs rules from
a single registry over them, and emits findings as text or JSON.

The architecture mirrors `~/kaeawc/krit`: v2-style rule registry,
single-pass dispatcher, multi-frontend output, capability-gated checks,
autofix tiers. Read krit's `CLAUDE.md` for the prior art.

## Working rules

- Keep analyzer and rule work in Go.
- After implementation changes, run `go build ./... && go vet ./...`.
- Run `go test ./... -count=1` for full validation; use focused package
  tests while iterating (`go test ./internal/rules/ -run TestPositiveFixtures -v`).
- New rules use the v2 registry: implement the rule struct in
  `internal/rules/<name>.go`, declare metadata, register from `init`.
- Each rule needs at least one positive fixture and one negative fixture
  under `tests/fixtures/{positive,negative}/<rule-id>/tools.json`.
  Fixable rules also need a fixable fixture once autofix lands.
- Auto-fixes (when implemented) declare `FixCosmetic`, `FixIdiomatic`,
  or `FixSemantic` so fix safety is visible to callers.

## Build & validate

```bash
go build -o mcpcheck ./cmd/mcpcheck/   # Build the CLI
go vet ./...                            # Lint Go code
go test ./... -count=1                  # Run all tests
go test ./internal/rules/ -run TestPositiveFixtures -v
make ci                                 # vet + test + complexity + lint + security + licenses
```

## Project structure

- `cmd/mcpcheck/` — CLI entry point.
- `internal/mcpmodel/` — normalized `Tool` / `ToolSet` types.
- `internal/scanner/` — intakes that produce a `ToolSet`. Today: JSON
  intake of MCP `tools/list` responses. Future: tree-sitter Python and
  TypeScript intakes; live-mode intake that boots a server over stdio.
- `internal/v2/` — rule registry, `Rule`, `Context`, `Finding`,
  `Severity`, `Category`, `FixLevel`, `Run`. Rules call `v2.Register`
  from init.
- `internal/rules/` — rule implementations. Each rule lives in its own
  file and registers itself.
- `internal/output/` — text and JSON formatters for findings.
- `tests/fixtures/{positive,negative}/<rule-id>/` — per-rule fixtures.
- `internal/{clock,vfs,fsutil,env,proc,idgen,random,logger,perf,...}` —
  shared plumbing carried over from the `golang-build` template. Time,
  randomness, env, subprocess, and filesystem must be injected through
  these so analyzer behavior stays testable.

## Adding a rule

1. Create the rule struct in `internal/rules/<name>.go`.
2. Implement the rule's behavior using `*v2.Context`. Report issues with
   `ctx.Report(message)`.
3. Register it from `init` with `v2.Register(&v2.Rule{...})`. Declare
   `ID`, `Category`, `Severity`, `Description`, `Fix`, and
   `Implementation`.
4. Add positive and negative fixtures under
   `tests/fixtures/{positive,negative}/<rule-id>/tools.json`.
5. Extend the `cases` slice in `internal/rules/fixtures_test.go` so the
   shared positive/negative driver runs against the new rule.

## Conventions

- Errors: wrap with `fmt.Errorf("context: %w", err)` so callers can
  `errors.Is`/`errors.As`.
- Comments: only when the *why* is non-obvious. Don't restate code.
- Tests live next to the code as `_test.go`. Use `t.TempDir()` for
  filesystem fixtures.
- Time/randomness/env/subprocess/filesystem: inject via `internal/clock`,
  `internal/random`, `internal/env`, `internal/proc`, `internal/vfs`.
  Never call `time.Now`, `os.Getenv`, `os/exec`, or raw `os.ReadFile`
  from analyzer code paths that need to be testable.
