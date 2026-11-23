"""MCP server built with the official `mcp` SDK v2 `MCPServer` (the v2 rename of FastMCP).

  .venv/bin/python server.py stdio
  .venv/bin/python server.py http --port N   (streamable HTTP at /mcp)
  .venv/bin/python server.py sse --port N    (legacy SSE at /sse)
"""

import argparse
from typing import Annotated, Literal

from mcp.server.mcpserver import MCPServer
from mcp.types import ToolAnnotations
from pydantic import Field

mcp = MCPServer("mcpcheck-python-mcpserver", version="0.1.0", log_level="WARNING")


@mcp.tool(annotations=ToolAnnotations(read_only_hint=True))
def get_weather(
    city: Annotated[str, Field(description="City name")],
    units: Literal["celsius", "fahrenheit"] | None = None,
) -> str:
    """Get the current weather for a city."""
    return f"Sunny in {city}"


@mcp.tool(annotations=ToolAnnotations(destructive_hint=True))
def delete_file(path: str, confirm: bool) -> str:
    """Delete a file inside the sandbox directory."""
    # Never touches the filesystem.
    return f"would delete {path}"


@mcp.tool(annotations=ToolAnnotations(open_world_hint=True))
def fetch_url(url: str) -> str:
    """Fetch a URL and return the response body."""
    # Never touches the network.
    return f"would fetch {url}"


def main() -> None:
    parser = argparse.ArgumentParser()
    parser.add_argument("mode", choices=["stdio", "http", "sse"], nargs="?", default="stdio")
    parser.add_argument("--port", type=int, default=8000)
    args = parser.parse_args()
    if args.mode == "stdio":
        mcp.run("stdio")
    elif args.mode == "http":
        mcp.run("streamable-http", host="127.0.0.1", port=args.port)
    else:
        mcp.run("sse", host="127.0.0.1", port=args.port)


if __name__ == "__main__":
    main()
