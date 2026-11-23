"""MCP server built with the standalone `fastmcp` package (jlowin/PrefectHQ).

  .venv/bin/python server.py stdio
  .venv/bin/python server.py http --port N   (streamable HTTP at /mcp)
  .venv/bin/python server.py sse --port N    (legacy SSE at /sse)

fetch_url deliberately uses the bare `@mcp.tool` decorator (no parentheses),
which cannot carry annotations, so it advertises no openWorldHint.
"""

import argparse
from typing import Annotated, Literal

from fastmcp import FastMCP
from mcp.types import ToolAnnotations
from pydantic import Field

mcp = FastMCP("mcpcheck-python-fastmcp2", version="0.1.0")


@mcp.tool(annotations=ToolAnnotations(readOnlyHint=True))
def get_weather(
    city: Annotated[str, Field(description="City name")],
    units: Literal["celsius", "fahrenheit"] | None = None,
) -> str:
    """Get the current weather for a city."""
    return f"Sunny in {city}"


@mcp.tool(annotations={"destructiveHint": True})
def delete_file(path: str, confirm: bool) -> str:
    """Delete a file inside the sandbox directory."""
    # Never touches the filesystem.
    return f"would delete {path}"


@mcp.tool
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
        mcp.run("stdio", show_banner=False)
        return
    mcp.run(args.mode, show_banner=False, host="127.0.0.1", port=args.port, log_level="WARNING")


if __name__ == "__main__":
    main()
