// mcpcheck corpus: @modelcontextprotocol/sdk 1.x McpServer.registerTool, served
// over stdio, stateful Streamable HTTP, or the deprecated HTTP+SSE transport.
import { randomUUID } from "node:crypto";
import { createServer, type IncomingMessage, type ServerResponse } from "node:http";
import { McpServer } from "@modelcontextprotocol/sdk/server/mcp.js";
import { StdioServerTransport } from "@modelcontextprotocol/sdk/server/stdio.js";
import { StreamableHTTPServerTransport } from "@modelcontextprotocol/sdk/server/streamableHttp.js";
import { SSEServerTransport } from "@modelcontextprotocol/sdk/server/sse.js";
import { isInitializeRequest } from "@modelcontextprotocol/sdk/types.js";
import * as z from "zod";

function buildServer(): McpServer {
  const server = new McpServer({ name: "mcpcheck-ts-sdk-register", version: "0.1.0" });

  server.registerTool(
    "get_weather",
    {
      title: "Get Weather",
      description: "Get the current weather for a city.",
      inputSchema: {
        city: z.string().describe("City name"),
        units: z.enum(["celsius", "fahrenheit"]).optional(),
      },
      annotations: { readOnlyHint: true },
    },
    async ({ city }) => ({ content: [{ type: "text", text: `Sunny in ${city}` }] }),
  );

  server.registerTool(
    "delete_file",
    {
      description: "Delete a file inside the sandbox directory.",
      inputSchema: {
        path: z.string(),
        confirm: z.boolean(),
      },
      annotations: { destructiveHint: true },
    },
    async ({ path }) => ({ content: [{ type: "text", text: `would delete ${path}` }] }),
  );

  server.registerTool(
    "fetch_url",
    {
      description: "Fetch a URL and return the response body.",
      inputSchema: {
        url: z.string(),
      },
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

async function readJson(req: IncomingMessage): Promise<unknown> {
  const chunks: Buffer[] = [];
  for await (const chunk of req) chunks.push(chunk as Buffer);
  if (chunks.length === 0) return undefined;
  return JSON.parse(Buffer.concat(chunks).toString("utf8"));
}

function jsonError(res: ServerResponse, status: number, code: number, message: string): void {
  res.writeHead(status, { "Content-Type": "application/json" });
  res.end(JSON.stringify({ jsonrpc: "2.0", error: { code, message }, id: null }));
}

async function serveHttp(port: number): Promise<void> {
  const transports = new Map<string, StreamableHTTPServerTransport>();

  const http = createServer(async (req, res) => {
    try {
      const url = new URL(req.url ?? "/", "http://127.0.0.1");
      if (url.pathname !== "/mcp") {
        res.writeHead(404).end();
        return;
      }
      const header = req.headers["mcp-session-id"];
      const sessionId = Array.isArray(header) ? header[0] : header;
      const body = req.method === "POST" ? await readJson(req) : undefined;

      let transport = sessionId ? transports.get(sessionId) : undefined;
      if (!transport) {
        if (sessionId) {
          jsonError(res, 404, -32001, "Session not found");
          return;
        }
        if (req.method !== "POST" || !isInitializeRequest(body)) {
          jsonError(res, 400, -32000, "Bad Request: No valid session ID provided");
          return;
        }
        const created = new StreamableHTTPServerTransport({
          sessionIdGenerator: () => randomUUID(),
          onsessioninitialized: (sid) => {
            transports.set(sid, created);
          },
        });
        created.onclose = () => {
          if (created.sessionId) transports.delete(created.sessionId);
        };
        await buildServer().connect(created);
        transport = created;
      }
      await transport.handleRequest(req, res, body);
    } catch (err) {
      console.error("error handling /mcp request:", err);
      if (!res.headersSent) jsonError(res, 500, -32603, "Internal server error");
    }
  });

  await new Promise<void>((resolve) => http.listen(port, "127.0.0.1", resolve));
  console.error(`streamable HTTP listening on http://127.0.0.1:${port}/mcp`);
}

async function serveSse(port: number): Promise<void> {
  const transports = new Map<string, SSEServerTransport>();

  const http = createServer(async (req, res) => {
    try {
      const url = new URL(req.url ?? "/", "http://127.0.0.1");
      if (req.method === "GET" && url.pathname === "/sse") {
        const transport = new SSEServerTransport("/messages", res);
        transports.set(transport.sessionId, transport);
        transport.onclose = () => {
          transports.delete(transport.sessionId);
        };
        await buildServer().connect(transport);
        return;
      }
      if (req.method === "POST" && url.pathname === "/messages") {
        const transport = transports.get(url.searchParams.get("sessionId") ?? "");
        if (!transport) {
          jsonError(res, 400, -32000, "No transport found for sessionId");
          return;
        }
        await transport.handlePostMessage(req, res, await readJson(req));
        return;
      }
      res.writeHead(404).end();
    } catch (err) {
      console.error("error handling SSE request:", err);
      if (!res.headersSent) jsonError(res, 500, -32603, "Internal server error");
    }
  });

  await new Promise<void>((resolve) => http.listen(port, "127.0.0.1", resolve));
  console.error(`legacy SSE listening on http://127.0.0.1:${port}/sse`);
}

async function main(): Promise<void> {
  const mode = process.argv[2] ?? "stdio";
  switch (mode) {
    case "stdio":
      await buildServer().connect(new StdioServerTransport());
      return;
    case "http":
      await serveHttp(parsePort(process.argv));
      return;
    case "sse":
      await serveSse(parsePort(process.argv));
      return;
    default:
      console.error(`unknown transport ${mode}; expected stdio, http or sse`);
      process.exit(2);
  }
}

main().catch((err) => {
  console.error(err);
  process.exit(1);
});
