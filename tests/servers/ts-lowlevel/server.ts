// mcpcheck corpus: @modelcontextprotocol/sdk 1.x low-level Server with
// hand-written JSON Schema tool definitions and request handlers.
import { Server } from "@modelcontextprotocol/sdk/server/index.js";
import { StdioServerTransport } from "@modelcontextprotocol/sdk/server/stdio.js";
import {
  CallToolRequestSchema,
  ListToolsRequestSchema,
  McpError,
  ErrorCode,
} from "@modelcontextprotocol/sdk/types.js";

const server = new Server(
  { name: "mcpcheck-ts-lowlevel", version: "0.1.0" },
  { capabilities: { tools: {} } },
);

server.setRequestHandler(ListToolsRequestSchema, async () => ({
  tools: [
    {
      name: "get_weather",
      description: "Get the current weather for a city.",
      inputSchema: {
        type: "object",
        properties: {
          city: { type: "string", description: "City name" },
          units: { type: "string", enum: ["celsius", "fahrenheit"] },
        },
        required: ["city"],
      },
      annotations: { readOnlyHint: true },
    },
    {
      name: "delete_file",
      description: "Delete a file inside the sandbox directory.",
      inputSchema: {
        type: "object",
        properties: {
          path: { type: "string" },
          confirm: { type: "boolean" },
        },
        required: ["path", "confirm"],
      },
      annotations: { destructiveHint: true },
    },
    {
      name: "fetch_url",
      description: "Fetch a URL and return the response body.",
      inputSchema: {
        type: "object",
        properties: {
          url: { type: "string" },
        },
        required: ["url"],
      },
      annotations: { openWorldHint: true },
    },
  ],
}));

server.setRequestHandler(CallToolRequestSchema, async (request) => {
  const args = (request.params.arguments ?? {}) as Record<string, unknown>;
  switch (request.params.name) {
    case "get_weather":
      return { content: [{ type: "text", text: `Sunny in ${String(args.city)}` }] };
    case "delete_file":
      return { content: [{ type: "text", text: `would delete ${String(args.path)}` }] };
    case "fetch_url":
      return { content: [{ type: "text", text: `would fetch ${String(args.url)}` }] };
    default:
      throw new McpError(ErrorCode.InvalidParams, `Unknown tool: ${request.params.name}`);
  }
});

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
