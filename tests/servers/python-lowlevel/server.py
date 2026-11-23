"""MCP server built with the official `mcp` SDK's low-level Server (mcp 1.x).

  .venv/bin/python server.py stdio
"""

import sys
from typing import Any

import anyio
import mcp.types as types
from mcp.server.lowlevel import Server
from mcp.server.stdio import stdio_server

server = Server("mcpcheck-python-lowlevel", version="0.1.0")


@server.list_tools()
async def list_tools() -> list[types.Tool]:
    return [
        types.Tool(
            name="get_weather",
            description="Get the current weather for a city.",
            inputSchema={
                "type": "object",
                "properties": {
                    "city": {"type": "string", "description": "City name"},
                    "units": {"type": "string", "enum": ["celsius", "fahrenheit"]},
                },
                "required": ["city"],
            },
            annotations=types.ToolAnnotations(readOnlyHint=True),
        ),
        types.Tool(
            name="delete_file",
            description="Delete a file inside the sandbox directory.",
            inputSchema={
                "type": "object",
                "properties": {
                    "path": {"type": "string"},
                    "confirm": {"type": "boolean"},
                },
                "required": ["path", "confirm"],
            },
            annotations=types.ToolAnnotations(destructiveHint=True),
        ),
        types.Tool(
            name="fetch_url",
            description="Fetch a URL and return the response body.",
            inputSchema={
                "type": "object",
                "properties": {
                    "url": {"type": "string"},
                },
                "required": ["url"],
            },
            annotations=types.ToolAnnotations(openWorldHint=True),
        ),
    ]


@server.call_tool()
async def call_tool(name: str, arguments: dict[str, Any]) -> list[types.TextContent]:
    if name == "get_weather":
        text = f"Sunny in {arguments['city']}"
    elif name == "delete_file":
        # Never touches the filesystem.
        text = f"would delete {arguments['path']}"
    elif name == "fetch_url":
        # Never touches the network.
        text = f"would fetch {arguments['url']}"
    else:
        raise ValueError(f"unknown tool: {name}")
    return [types.TextContent(type="text", text=text)]


async def run_stdio() -> None:
    async with stdio_server() as (read_stream, write_stream):
        await server.run(read_stream, write_stream, server.create_initialization_options())


def main() -> None:
    mode = sys.argv[1] if len(sys.argv) > 1 else "stdio"
    if mode != "stdio":
        sys.exit(f"unsupported mode {mode!r}: this server only speaks stdio")
    anyio.run(run_stdio)


if __name__ == "__main__":
    main()
