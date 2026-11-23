// mcpcheck corpus: @modelcontextprotocol/sdk 1.x McpServer with the legacy
// (deprecated) server.tool(name, description, shape, annotations, cb) form.
import { McpServer } from "@modelcontextprotocol/sdk/server/mcp.js";
import { StdioServerTransport } from "@modelcontextprotocol/sdk/server/stdio.js";
import * as z from "zod";

const server = new McpServer({ name: "mcpcheck-ts-sdk-tool", version: "0.1.0" });

server.tool(
  "get_weather",
  "Get the current weather for a city.",
  {
    city: z.string().describe("City name"),
    units: z.enum(["celsius", "fahrenheit"]).optional(),
  },
  { readOnlyHint: true },
  async ({ city }) => ({ content: [{ type: "text", text: `Sunny in ${city}` }] }),
);

server.tool(
  "delete_file",
  "Delete a file inside the sandbox directory.",
  {
    path: z.string(),
    confirm: z.boolean(),
  },
  { destructiveHint: true },
  async ({ path }) => ({ content: [{ type: "text", text: `would delete ${path}` }] }),
);

server.tool(
  "fetch_url",
  "Fetch a URL and return the response body.",
  {
    url: z.string(),
  },
  { openWorldHint: true },
  async ({ url }) => ({ content: [{ type: "text", text: `would fetch ${url}` }] }),
);

async function main(): Promise<void> {
  const mode = process.argv[2] ?? "stdio";
  if (mode !== "stdio") {
    console.error(`unsupported transport ${mode}; only stdio is implemented`);
    process.exit(2);
  }
  await server.connect(new StdioServerTransport());
}

main().catch((err) => {
  console.error(err);
  process.exit(1);
});
