"""MCP server built with the official `mcp` SDK's FastMCP (mcp 1.x).

  .venv/bin/python server.py stdio
  .venv/bin/python server.py http --port N   (streamable HTTP at /mcp)
  .venv/bin/python server.py sse --port N    (legacy SSE at /sse)
"""

import argparse
from typing import Annotated, Literal, Optional

from mcp.server.fastmcp import FastMCP
from mcp.types import ToolAnnotations
from pydantic import Field

mcp = FastMCP("mcpcheck-python-fastmcp")


@mcp.tool(annotations=ToolAnnotations(readOnlyHint=True))
def get_weather(
    city: Annotated[str, Field(description="City name")],
    units: Optional[Literal["celsius", "fahrenheit"]] = None,
) -> str:
    """Get the current weather for a city."""
    return f"Sunny in {city}"


@mcp.tool(annotations=ToolAnnotations(destructiveHint=True))
def delete_file(path: str, confirm: bool) -> str:
    """Delete a file inside the sandbox directory."""
    # Never touches the filesystem.
    return f"would delete {path}"


@mcp.tool(annotations=ToolAnnotations(openWorldHint=True))
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
        return
    mcp.settings.host = "127.0.0.1"
    mcp.settings.port = args.port
    mcp.settings.log_level = "WARNING"
    mcp.run("streamable-http" if args.mode == "http" else "sse")


if __name__ == "__main__":
    main()
