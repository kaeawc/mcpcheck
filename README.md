# mcpcheck

A static validator for MCP (Model Context Protocol) servers and tool-use
schemas. Reads tool advertisements from a JSON `tools/list` response, a
Python source tree, a TypeScript source tree, or a live MCP server over
stdio, then runs a registered rule set and emits findings as text, JSON,
or SARIF.

The architecture mirrors [krit](https://github.com/kaeawc/krit) — Go-first,
single-pass dispatcher, capability-gated rules, multi-frontend output,
autofix tiers.

## Why

MCP is becoming the default interop layer for LLM tool use, and it has
the same problem every interop layer has: the schema is the contract,
but the schema and the implementation drift. Bad servers degrade agent
reliability across every product that talks to them. Static validation
upstream of registry submission is the cheapest place to catch problems.

## Install

```bash
go install github.com/kaeawc/mcpcheck/cmd/mcpcheck@latest
```

Or build from source:

```bash
git clone https://github.com/kaeawc/mcpcheck
cd mcpcheck
make build              # produces ./mcpcheck
```

## Usage

### Validate a tools/list response

```bash
mcpcheck path/to/tools.json
```

Accepts either an MCP `tools/list` response (`{"tools": [...]}`) or a
bare array of tool objects.

### Validate a Python or TypeScript server source tree

```bash
mcpcheck path/to/python-mcp-server/
mcpcheck path/to/typescript-mcp-server/
mcpcheck path/to/server.py        # single file also works
mcpcheck path/to/server.ts
```

Detects FastMCP-style `@mcp.tool()` / `@server.tool()` decorators in
Python and `<receiver>.tool("name", "description", ...)` calls in
TypeScript. The intake routing is automatic: directories containing any
`.py` go through Python; otherwise TypeScript. File extensions
(`.py`, `.ts`, `.tsx`, `.js`, `.mjs`, `.cjs`, `.json`) are routed
explicitly.

### Validate a live server

```bash
mcpcheck --live "python -m my_server"
mcpcheck --live "node dist/server.js" --live-timeout 60s
```

Spawns the command as a subprocess, runs the JSON-RPC `initialize` /
`notifications/initialized` / `tools/list` handshake over stdio, and
analyzes whatever tools the server actually advertises at runtime.
Catches dynamically-registered tools that static intakes miss.

Argv is split with `strings.Fields` — no shell features. Wrap in
`sh -c "..."` if you need them.

### Output formats

```bash
mcpcheck --format text  ...   # default; one finding per line
mcpcheck --format json  ...   # array of finding objects
mcpcheck --format sarif ...   # SARIF 2.1.0; consumed by GitHub Code
                              # Scanning, Azure DevOps, etc.
```

### List the registered rule set

```bash
mcpcheck --list-rules                    # tabular text
mcpcheck --list-rules --format json      # machine-readable
```

### Exit codes

- `0` — no findings, or only warnings / info
- `1` — at least one error-severity finding fired (or an internal error)
- `2` — invalid invocation (missing path, conflicting flags)

## Rules

| ID | Severity | Category | Summary |
|---|---|---|---|
| `tool-name-not-snake-case` | warning | spec | Tool names should be snake_case so they read predictably in agent tool lists. |
| `tool-name-collision` | error | spec | Tool names within a server must be unique; agent dispatch routes by name. |
| `tool-description-empty-or-truncated` | warning | spec | Descriptions must be non-empty and not visibly truncated. |
| `tool-description-mentions-secret` | warning | safety | Heuristic flag for descriptions that mention credential-shaped terms. |
| `destructive-tool-not-gated` | error | safety | Tools whose name implies mutation (`delete_*`, `send_*`, ...) must require explicit confirmation through the schema. |
| `network-tool-no-allowlist` | warning | safety | URL-shaped input properties without `enum` / `pattern` / `const` permit SSRF. |
| `file-tool-no-path-confinement` | warning | safety | Path-shaped input properties without `enum` / `pattern` / `const` permit path traversal. |
| `tool-example-fails-schema` | warning | examples | Examples declared on a tool's `inputSchema` must validate against that schema. |
| `tool-example-uses-real-pii` | warning | examples | Examples should use placeholder emails / SSNs / phones, not real-looking values. |

`mcpcheck --list-rules` always prints the authoritative current set.

## Architecture

- `cmd/mcpcheck/` — CLI entry point.
- `internal/mcpmodel/` — normalized `Tool` / `ToolSet` types.
- `internal/scanner/` — intakes (`LoadToolsJSON`, `LoadPythonFile`,
  `LoadTypeScriptFile`, `LoadLive`) plus the `Load(path)` dispatcher
  and the testable `FetchToolsLive(ctx, stdin, stdout)` core of the
  live-mode handshake.
- `internal/v2/` — rule registry. Defines `Rule`, `Context`,
  `ProjectContext`, `Finding`, `Severity`, `Category`, `FixLevel` plus
  `Register`, `All`, `Run`, `RunProject`. Per-tool and project-scope
  rules are dispatched separately.
- `internal/rules/` — every rule lives in its own file and registers
  itself from `init`.
- `internal/output/` — text, JSON, and SARIF formatters; rule-list
  formatters used by `--list-rules`.
- `tests/fixtures/` — positive / negative fixtures per rule, plus
  intake fixtures (`intake/python/`, `intake/typescript/`).
- `internal/{clock,vfs,fsutil,env,proc,idgen,random,logger,perf,...}` —
  shared plumbing carried over from the
  [`golang-build`](https://github.com/kaeawc/golang-build) template.

## Roadmap

Tracked loosely; not all of these are committed.

- Schema/handler agreement rules: `arity-mismatch`, `type-mismatch`,
  `required-field-with-default`, `content-type-undeclared`. Need
  handler-signature extraction (the regex Python intake will be
  extended to capture `def fn(args)` first; tree-sitter follows).
- `tool-result-no-size-cap` — handler-body analysis.
- Tree-sitter Python and TypeScript intakes (replacing the regex
  extractors).
- Live-vs-static comparison rule: tools advertised statically but not
  at runtime, or vice versa.
- LSP frontend, MCP-server frontend (so an agent can ask mcpcheck to
  validate other servers).
- Autofix application — the framework declares `FixLevel`
  (`cosmetic` / `idiomatic` / `semantic`) but no fix is wired into the
  `--fix` path yet.
- CI corpus of public MCP servers from the registry; baseline
  comparison for rule-tuning and regression detection.
- Cross-server compatibility check — given two intakes, detect tool
  collisions or contradictory schemas across servers.

## Non-goals

- Runtime monitoring of MCP traffic.
- Authorization / identity (that's the host's job).
- Model-side prompt quality.

## License

MIT — see [LICENSE](LICENSE).
