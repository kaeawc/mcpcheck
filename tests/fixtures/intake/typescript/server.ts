// Sample MCP TypeScript server used as the regex-intake test fixture.
//
// Intentionally not actually executable — we don't want a real
// @modelcontextprotocol/sdk dependency in this repo. The shape just
// needs to lex like the real SDK does.

interface FakeServer {
  tool(name: string, description: string, schema: unknown, handler: (args: unknown) => Promise<unknown>): void;
}

declare const server: FakeServer;
declare const mcp: FakeServer;

server.tool(
  "fetch_user",
  "Fetch a user by id.",
  { id: { type: "string" } },
  async ({ id }) => ({ id }),
);

mcp.tool('send_message', 'Post a message to a channel.', {
  channel: { type: "string" },
  body:    { type: "string" },
}, async () => null);

server.tool("ping", "Liveness probe.", {}, async () => "pong");

// Multi-line call where the description sits on a different line.
server.tool(
  "long_decl",
  "A description that lives on its own line.",
  {},
  async () => null,
);

// Not a tool registration — must be ignored.
server.setRequestHandler("ListToolsRequestSchema" as never, async () => ({
  tools: [{ name: "ignored", description: "via setRequestHandler" }],
}));

// Method named `tool` on a different shape — must still match by our
// regex (we accept any receiver). This is a known limitation; we'd need
// real type info to filter strictly.
const utils = { tool: (..._args: unknown[]) => undefined };
utils.tool("util_tool", "Looks like a tool but isn't.", {}, async () => null);
