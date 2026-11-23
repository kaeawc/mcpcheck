# MCP reference server corpus

Small, real MCP servers that each use a different language, SDK or
registration style. They all expose the **same three tools**, so a harness
can assert identical static and live results across SDKs and transports.
`servers.json` is the machine-readable manifest.

## The canonical tools

| tool | description (exact) | inputs | annotation | returns |
|---|---|---|---|---|
| `get_weather` | `Get the current weather for a city.` | `city` string, required, described as "City name"; `units` string, optional, enum `celsius`/`fahrenheit` | `readOnlyHint: true` | `Sunny in <city>` |
| `delete_file` | `Delete a file inside the sandbox directory.` | `path` string, required; `confirm` boolean, required | `destructiveHint: true` | `would delete <path>` (nothing is deleted) |
| `fetch_url` | `Fetch a URL and return the response body.` | `url` string, required | `openWorldHint: true` | `would fetch <url>` (no network) |

Tool names and descriptions are string literals at the registration site,
so the static analyzer can read them from source.

## Setup

```bash
tests/servers/setup.sh             # build every server
tests/servers/setup.sh go-sdk      # build only the named ids
```

The script is idempotent. It creates a `.venv` per server with `uv` and
installs with `npm ci`. It also runs `cargo build --release --locked` and
`go build -o bin/server .`. A language whose toolchain is missing is
skipped with a message. Besides `PATH`, the script looks in
`/workspaces/tooling` (or `$MCPCHECK_TOOLING`) for `bin/uv`, `cargo/`,
`rustup/` and `uv-python/`, and in `/workspaces/gotool/go/bin` for Go.
Build output (`.venv/`, `node_modules/`, `target/`, `bin/`) is gitignored.

The argv lists in `servers.json` run with cwd set to the server's directory
and use only prebuilt artifacts. For HTTP and SSE entries the harness
substitutes `{port}`.

## Servers × transports

| id | language | SDK (pinned) | stdio | streamable HTTP (`/mcp`) | legacy SSE (`/sse`) |
|---|---|---|---|---|---|
| python-fastmcp | python 3.10 | `mcp==1.30.0`, `mcp.server.fastmcp.FastMCP` | yes | yes | yes |
| python-fastmcp2 | python 3.12 | `fastmcp==4.2.5` (on `mcp==2.3.0`) | yes | yes | yes |
| python-lowlevel | python 3.10 | `mcp==1.30.0`, low-level `Server` decorators | yes | — | — |
| python-mcpserver | python 3.10 | `mcp==2.3.0`, `mcp.server.mcpserver.MCPServer` | yes | yes | yes |
| python-handrolled | python 3 stdlib | handrolled | yes | — | yes (`http` mode) |
| ts-sdk-tool | typescript | `@modelcontextprotocol/sdk@1.32.2`, `server.tool(...)` | yes | — | — |
| ts-sdk-register | typescript | `@modelcontextprotocol/sdk@1.32.2`, `server.registerTool(...)` | yes | yes | yes |
| ts-lowlevel | typescript | `@modelcontextprotocol/sdk@1.32.2`, low-level `Server` | yes | — | — |
| ts-sdk-v2 | typescript | `@modelcontextprotocol/server@2.4.0` + `@modelcontextprotocol/node@2.1.2` | yes | yes (stateless) | — |
| rust-rmcp | rust | `rmcp 3.11.0` | yes | yes | — (rmcp has no SSE server) |
| go-sdk | go | `github.com/modelcontextprotocol/go-sdk v1.8.0` | yes | yes | yes |
| go-mcpgo | go | `github.com/mark3labs/mcp-go v1.6.0` | yes | yes | yes |

Each Go server is its own nested module, so the root module's `./...`
never includes it.

## Deliberate deviations

- **python-fastmcp, python-lowlevel:** these pin the last 1.x release of
  `mcp`. In mcp 2.x, `mcp.server.fastmcp` is gone (it was renamed
  `MCPServer`; see python-mcpserver), and the low-level `@server.list_tools()`
  decorators were replaced by `Server(on_list_tools=...)` constructor
  arguments.
- **python-fastmcp2:** `fetch_url` uses the bare `@mcp.tool` decorator to
  exercise that syntax. A bare decorator can't carry annotations, so this
  tool has **no `openWorldHint`**.
- **go-mcpgo:** `mcp.NewTool` fills in all four hints by default
  (`readOnlyHint:false`, `destructiveHint:true`, `idempotentHint:false`,
  `openWorldHint:true`). As a result, `get_weather` advertises both
  `readOnlyHint:true` and `destructiveHint:true`.
- **python-handrolled:** `tools/list` paginates on purpose. The first page
  holds two tools plus `nextCursor: "page2"`, and the second page holds the
  third tool.

Every SDK that was requested was built; none were skipped.
