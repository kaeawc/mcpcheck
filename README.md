# mcpcheck — a static validator for MCP servers and tool-use schemas

## What you're building

MCP is becoming the default interop layer for LLM tool use, and it has the same problem every interop layer has: the schema is the contract, but the schema and the implementation drift. mcpcheck statically verifies that an MCP server's advertised tools match their handlers, that destructive tools are gated, that returned content respects size and content-type limits, that examples in tool descriptions actually validate, and that the server respects the MCP spec's invariants.

Architecture mirrors Krit (Go-first, tree-sitter, capability-gated rules, single-pass dispatch, multi-frontend, autofix tiers). Read `~/kaeawc/krit/CLAUDE.md` first.

## Rule taxonomy

**Schema/handler agreement**
- `tool-schema-handler-arity-mismatch` — declared input fields differ from handler signature.
- `tool-schema-handler-type-mismatch` — declared field type incompatible with handler annotation.
- `tool-schema-required-field-with-default` — required field that the handler treats as optional (or vice versa).
- `tool-result-content-type-undeclared` — handler returns image bytes but schema says text.

**Spec compliance**
- `tool-name-not-snake-case` (or whatever the chosen convention is).
- `tool-description-empty-or-truncated`.
- `tool-description-mentions-secret` — heuristic for `password`, `api_key`, `token` patterns in descriptions.
- `tool-result-no-size-cap` — handler returns user-controlled bytes with no size limit.

**Safety contract**
- `destructive-tool-not-gated` — tool name implies mutation (`delete_*`, `send_*`, `transfer_*`) but no explicit confirmation field or `requires_confirmation` annotation.
- `network-tool-no-allowlist` — tool makes outbound HTTP with no URL allowlist.
- `file-tool-no-path-confinement` — tool reads/writes filesystem with no root-prefix check.

**Examples**
- `tool-example-fails-schema` — examples in description don't validate against the schema.
- `tool-example-uses-real-pii` — heuristic.

## Architecture

- **Go**, tree-sitter Python + JS/TS (the two dominant MCP server languages), JSON Schema parser.
- **Server intake** — given a path, locate the MCP entrypoint (Python `mcp.server.Server` or TS `@modelcontextprotocol/sdk` patterns), enumerate tools, build the schema/handler index.
- **Single-pass dispatcher** routes nodes to rules; project-scope phase runs cross-tool checks (duplicate names, conflicting auth strategies).
- **Live mode** — optionally launch the server, list tools via stdio, compare to the static index. Catches cases where tools are registered dynamically.
- **Outputs**: SARIF, JSON, LSP, PR comment, MCP server (yes — mcpcheck is itself an MCP server, so an agent can ask it to validate other servers).
- **Autofix tiers** — `cosmetic` (rename, reorder), `idiomatic` (add missing description), `semantic` (insert size cap or confirmation field).

## MVP

1. Repo skeleton.
2. Python MCP server intake (the reference SDK).
3. Six rules across three categories.
4. Live-mode harness that boots a server in a subprocess and lists tools.
5. CI corpus: a dozen public MCP servers from the registry. Hand-label baseline.

## Stretch

- **Cross-server compatibility check** — given two servers, detect tool-name collisions or contradictory schemas.
- **Capability negotiation linter** — verify that resources/prompts/tools advertised match what the client can use.
- **Property-based tests** — generate inputs from the schema, run the handler, assert output matches the declared shape.
- **TypeScript SDK support**.

## Why this is the right shape

MCP servers are the new "API endpoint" — they're going to ship by the thousands, and bad ones will degrade agent reliability across every product that talks to them. Static validation upstream of registry submission is the cheapest place to catch problems. The Krit shape (cheap by default, opt-in heavier checks, multi-frontend) maps directly: most rules are AST/JSON-schema work, the live-server check is a capability you pay for only when you want it.

## Non-goals

- Runtime monitoring of MCP traffic.
- Authorization/identity (that's the host's job).
- Model-side prompt quality.
