// mcpcheck corpus: v2 TypeScript SDK (@modelcontextprotocol/server) McpServer
// with registerTool + z.object() input schemas, served from one factory via
// serveStdio (stdio) or createMcpHandler + toNodeHandler (streamable HTTP).
import { createServer } from "node:http";
import { createMcpHandler, McpServer } from "@modelcontextprotocol/server";
import { serveStdio } from "@modelcontextprotocol/server/stdio";
import {
  localhostHostValidation,
  localhostOriginValidation,
  toNodeHandler,
} from "@modelcontextprotocol/node";
import * as z from "zod";

function buildServer(): McpServer {
  const server = new McpServer({ name: "mcpcheck-ts-sdk-v2", version: "0.1.0" });

  server.registerTool(
    "get_weather",
    {
      title: "Get Weather",
      description: "Get the current weather for a city.",
      inputSchema: z.object({
        city: z.string().describe("City name"),
        units: z.enum(["celsius", "fahrenheit"]).optional(),
      }),
      annotations: { readOnlyHint: true },
    },
    async ({ city }) => ({ content: [{ type: "text", text: `Sunny in ${city}` }] }),
  );

  server.registerTool(
    "delete_file",
    {
      description: "Delete a file inside the sandbox directory.",
      inputSchema: z.object({
        path: z.string(),
        confirm: z.boolean(),
      }),
      annotations: { destructiveHint: true },
    },
    async ({ path }) => ({ content: [{ type: "text", text: `would delete ${path}` }] }),
  );

  server.registerTool(
    "fetch_url",
    {
      description: "Fetch a URL and return the response body.",
      inputSchema: z.object({
        url: z.string(),
      }),
      annotations: { openWorldHint: true },
    },
    async ({ url }) => ({ content: [{ type: "text", text: `would fetch ${url}` }] }),
  );

  return server;
}

function parsePort(argv: string[]): number {
  const i = argv.indexOf("--port");
  const port = i >= 0 ? Number(argv[i + 1]) : NaN;
  if (!Number.isInteger(port) || port <= 0) {
    console.error("missing or invalid --port N");
    process.exit(2);
  }
  return port;
}

async function serveHttp(port: number): Promise<void> {
  const handler = toNodeHandler(
    createMcpHandler(buildServer, { onerror: (err) => console.error(err) }),
  );
  const validateHost = localhostHostValidation();
  const validateOrigin = localhostOriginValidation();

  const http = createServer(async (req, res) => {
    const path = new URL(req.url ?? "/", "http://127.0.0.1").pathname;
    if (path !== "/mcp") {
      res.writeHead(404).end();
      return;
    }
    if (!validateHost(req, res) || !validateOrigin(req, res)) return;
    await handler(req, res);
  });

  await new Promise<void>((resolve) => http.listen(port, "127.0.0.1", resolve));
  console.error(`streamable HTTP listening on http://127.0.0.1:${port}/mcp`);
}

async function main(): Promise<void> {
  const mode = process.argv[2] ?? "stdio";
  switch (mode) {
    case "stdio":
      serveStdio(buildServer, { onerror: (err) => console.error(err) });
      return;
    case "http":
      await serveHttp(parsePort(process.argv));
      return;
    default:
      console.error(`unknown transport ${mode}; expected stdio or http`);
      process.exit(2);
  }
}

main().catch((err) => {
  console.error(err);
  process.exit(1);
});
