# mcpcheck

A validator for MCP (Model Context Protocol) servers. It reads tool
advertisements from source code (Python, TypeScript/JavaScript, Go,
Rust), from a running server over stdio or HTTP, or from a recorded
`tools/list` response, then runs one rule set over all of them and
emits findings as text, JSON, or SARIF.

It works with servers built on the official SDKs (TypeScript v1 and v2,
Python `mcp` 1.x and 2.x, Go, Rust), on fastmcp and mcp-go, and with
hand-rolled servers; and it speaks every published protocol revision,
from the 2024-11-05 HTTP+SSE transport to the stateless 2026-07-28
revision.

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

### Validate server source

```bash
mcpcheck path/to/server/          # every language in the tree
mcpcheck path/to/server.py        # or a single file
```

Directories are scanned by every source intake and the results merged,
so polyglot repositories work. Dependency, build-output, VCS, and
`testdata` directories, and test files, are skipped. Each intake
recovers names, descriptions, annotations, and input schemas, so schema
rules run on source too:

| Language | Recognized registrations |
|---|---|
| Python | `@mcp.tool` / `@mcp.tool(name=..., description=..., annotations=...)` on a `def` (official SDK FastMCP and `MCPServer`, fastmcp); `mcp.add_tool(fn, ...)`; low-level `types.Tool(...)`; hand-rolled dicts. Schemas come from the signature: `Annotated[..., Field(...)]`, `Literal`, `Optional` / `X \| None`, defaults, with `Context` parameters excluded. Module constants and `Enum` members resolve. |
| TypeScript / JS | `server.registerTool(name, { title, description, inputSchema, outputSchema, annotations }, cb)` and `server.tool(name, description?, shape?, annotations?, cb)` (SDK v1 and v2); object literals in `ListToolsRequestSchema` handlers. Zod raw shapes and `z.object(...)` convert to JSON Schema; file-local consts resolve. |
| Go | official go-sdk `mcp.AddTool` / `server.AddTool` with `&mcp.Tool{...}`, schemas inferred from the handler's input struct; mark3labs/mcp-go `mcp.NewTool(name, mcp.WithDescription(...), mcp.WithString(...), ...)`; hand-rolled `Tool{...}` and map literals. Parsed with `go/parser`. |
| Rust | rmcp `#[tool(...)]` functions (current and older macro styles) with schemars parameter structs and serde renames; `Tool::new`, `Tool { ... }` literals; `json!` tool objects. |

When a scan recognizes no tools, mcpcheck prints the supported patterns.

### Validate a running server

```bash
mcpcheck --live "uv run server.py"                 # stdio
mcpcheck --live "npx -y @scope/my-server" --live-timeout 90s
mcpcheck --url http://localhost:8000/mcp           # Streamable HTTP
mcpcheck --url http://localhost:8000/sse           # legacy HTTP+SSE
mcpcheck --url https://api.example.com/mcp --header "Authorization: Bearer $TOKEN"
mcpcheck --live "python server.py" path/to/source/ # live + static + drift check
```

`--live` takes a command line (quotes and backslash escapes are honored;
wrap in `sh -c '...'` for pipes or expansions). `--url` uses Streamable
HTTP and, per the spec's compatibility procedure, falls back to the
legacy HTTP+SSE transport when the server rejects the first POST;
`--transport streamable-http|sse` pins one.

mcpcheck is a deliberately forgiving client: it matches responses by id,
answers server pings, follows `tools/list` pagination, decodes JSON and
SSE responses, and tolerates banners on stdout or LSP-style framing —
and records each of those mistakes as a finding instead of failing. When
a session does fail, the error names the step, suggests a cause, and
shows the server's exit status and stderr.

Some servers register tools only for clients that declare certain
capabilities; `--client-capabilities roots,sampling,elicitation` (or
`all`) declares them so those tools show up.

`--trace` logs every JSON-RPC message to stderr; `--verbose` streams the
stdio server's stderr.

### Protocol versions

By default mcpcheck speaks both protocol eras. It first probes with
`server/discover` at 2026-07-28 (stateless: version and client identity
in every request's `_meta`, no sessions); a server that answers is
checked as a modern server. Anything else is a legacy server, and
mcpcheck falls back to the `initialize` handshake at 2025-11-25, which
negotiates down to 2024-11-05. `--protocol-version` pins a version:
a modern one disables the fallback, a legacy one skips the probe.

### Record and replay

```bash
mcpcheck --live "python server.py" --save session.json
mcpcheck session.json            # re-check offline, e.g. in CI
```

The recording holds the tools exactly as advertised plus what the
session observed (protocol, server identity, protocol violations), so
every rule gives the same answer offline.

### Validate a tools/list response

```bash
mcpcheck path/to/tools.json
```

Accepts an MCP `tools/list` result (`{"tools": [...]}`), a bare array
of tool objects, or a file written by `--save`.

### Output formats

```bash
mcpcheck --format text  ...   # default; one finding per line
mcpcheck --format json  ...   # array of finding objects
mcpcheck --format sarif ...   # SARIF 2.1.0; consumed by GitHub Code
                              # Scanning, Azure DevOps, etc.
```

A summary of what was analyzed goes to stderr (`--quiet` suppresses it),
so stdout stays machine-readable.

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
| `destructive-tool-not-gated` | error | safety-contract | Tools whose name implies a destructive or externally-visible action must require explicit confirmation through the schema. |
| `file-tool-no-path-confinement` | warning | safety-contract | Tools that accept a filesystem path must constrain it via an enum, pattern, or const so an agent cannot read or write arbitrary files (path traversal). |
| `network-tool-no-allowlist` | warning | safety-contract | Tools that accept a user-supplied URL must constrain the destination via an enum, pattern, or const so an agent cannot drive arbitrary outbound requests (SSRF). |
| `server-jsonrpc-malformed` | error | protocol-conformance | Server messages must be well-formed JSON-RPC 2.0: correct jsonrpc version, matching response ids, exactly one of result or error. |
| `server-legacy-protocol-only` | info | protocol-conformance | The server speaks only the legacy initialize-based protocol (2025-11-25 and earlier); clients that implement only 2026-07-28 cannot connect to it. |
| `server-lifecycle-nonconformant` | warning | protocol-conformance | The initialize exchange must follow the spec: serverInfo with name and version, a capabilities object that declares tools, and no requests the client didn't enable. |
| `server-protocol-version-unsupported` | error | protocol-conformance | The server must negotiate a published MCP protocol version in its initialize result. |
| `server-result-shape-nonconformant` | warning | protocol-conformance | Under protocol 2026-07-28 results must carry resultType, and list/discover results must carry ttlMs and cacheScope. |
| `server-stdout-not-protocol` | error | protocol-conformance | A stdio server must write only JSON-RPC messages to stdout; logs and banners there corrupt the transport for most clients. |
| `server-tools-pagination-broken` | error | protocol-conformance | tools/list pagination must terminate and use string cursors. |
| `server-transport-nonconformant` | warning | protocol-conformance | The server must follow its transport's framing rules (newline-delimited stdio; Streamable HTTP status codes, content types, and session headers). |
| `tool-annotations-inconsistent` | warning | protocol-conformance | Tool annotations must use the spec's field types and must not contradict each other or the tool's name. |
| `tool-description-empty-or-truncated` | warning | spec-compliance | Tool descriptions must be non-empty and not visibly truncated; agents rely on them to choose tools. |
| `tool-description-mentions-secret` | warning | safety-contract | Tool descriptions that mention credential-shaped terms may be exposing secrets through arguments or results; flag for review. |
| `tool-example-fails-schema` | warning | examples | Examples declared on a tool's inputSchema must validate against that schema; stale examples mislead agents and reviewers. |
| `tool-example-uses-real-pii` | warning | examples | Schema examples should use placeholder values for emails, SSNs, and phone numbers; real-looking PII suggests it was pasted from production. |
| `tool-input-schema-invalid` | error | protocol-conformance | Every tool must advertise an inputSchema that is a JSON Schema object with "type": "object". |
| `tool-mcp-header-invalid` | error | protocol-conformance | x-mcp-header annotations in an inputSchema must name a unique, valid header for a statically reachable string, integer, or boolean property; clients drop tools that violate this. |
| `tool-name-collision` | error | spec-compliance | Tool names within a single server must be unique; agent tool-call dispatch routes by name. |
| `tool-name-invalid` | error | protocol-conformance | Tool names must be 1–128 characters of A–Z, a–z, 0–9, _, -, or . so every client can route calls to them. |
| `tool-name-not-snake-case` | warning | spec-compliance | MCP tool names should be snake_case so they read predictably in agent tool lists. |
| `tool-output-schema-invalid` | error | protocol-conformance | A tool's outputSchema, when present, must be a JSON Schema object with "type": "object". |
| `tool-schema-required-references-undefined-property` | error | spec-compliance | Every name in inputSchema.required must exist in inputSchema.properties; otherwise the schema is internally inconsistent. |
| `tool-static-vs-live-mismatch` | warning | spec-compliance | Tools advertised by the static intake should match the tools the live server registers at runtime; drift here indicates dynamic registration that the static analysis can't see. |

`mcpcheck --list-rules` always prints the authoritative current set.
Protocol-conformance rules about the server itself (`server-*`) need a
live session or a `--save` recording.

## Architecture

- `cmd/mcpcheck/` — CLI entry point, flag parsing, summary.
- `internal/mcpmodel/` — normalized `Tool` / `ToolSet` types, plus
  `Server`: what a live session observed (protocol era and version,
  identity, capabilities, protocol violations).
- `internal/mcpclient/` — the live MCP client: stdio, Streamable HTTP,
  and legacy HTTP+SSE transports; dual-era (`server/discover` /
  `initialize`) protocol handling; a recorder for violations.
- `internal/scanner/` — intakes producing a `ToolSet`: per-language
  source intakes sharing a literal parser (`literal.go`) and zod
  converter (`zod.go`), the Go intake on `go/parser`, the JSON intake,
  and `LoadLive`, plus the `Load(path)` dispatcher.
- `internal/v2/` — rule registry. Defines `Rule`, `Context`,
  `ProjectContext`, `ComparisonContext`, `Finding`, `Severity`,
  `Category`, `FixLevel` plus `Register`, `All`, `Run`, `RunProject`,
  `RunComparison`.
- `internal/rules/` — every rule lives in its own file and registers
  itself from `init`.
- `internal/output/` — text, JSON, and SARIF formatters; rule-list
  formatters used by `--list-rules`.
- `tests/fixtures/` — positive / negative fixtures per rule, plus
  intake fixtures.
- `tests/servers/` — reference servers in Python, TypeScript, Rust, and
  Go (official SDKs v1/v2, fastmcp, mcp-go, hand-rolled) over stdio,
  Streamable HTTP, and SSE, all exposing the same three tools.
- `tests/e2e/` — runs the static and live intakes against every
  reference server: `make e2e` (builds them with
  `tests/servers/setup.sh`, which needs uv, Node, Cargo, and Go).
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
- Tree-sitter intakes, for registrations built by helper functions or
  imported across files.
- Exercise tools in live mode (`tools/call` with schema-valid
  arguments) to check handlers against their schemas.
- Resources and prompts.
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
