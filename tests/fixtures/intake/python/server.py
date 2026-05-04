"""Sample MCP server for the Python intake fixture.

This file is read by the scanner package's tests; it must remain
syntactically valid Python so it can also be imported / linted as a
regression check if we ever wire that up.
"""

from __future__ import annotations

# Intentionally fake-import so the file remains parseable without the SDK.
class _FakeMCP:
    def tool(self, *args, **kwargs):
        def deco(fn):
            return fn
        return deco

mcp = _FakeMCP()
server = _FakeMCP()


@mcp.tool()
def fetch_user(user_id: str) -> dict:
    """Fetch a user by id."""
    return {"id": user_id}


@mcp.tool()
def send_message(channel: str, body: str) -> None:
    """Post a message to a channel.

    The body is delivered as plain text; markdown is rendered client-side.
    """
    return None


@server.tool(name="custom_named_tool", description="Tool with an explicit kwarg description.")
def _internal(arg: str) -> str:
    """This docstring is overridden by the description= kwarg."""
    return arg


@mcp.tool
def ping_no_parens() -> str:
    """Ping with the bare-decorator form (no parens)."""
    return "pong"


# A function NOT decorated with @<obj>.tool — must NOT be picked up.
def helper(x: int) -> int:
    """Internal helper, not a tool."""
    return x + 1


# A decorator that resembles but is not @<obj>.tool — must NOT match.
@mcp.something_else()
def not_a_tool() -> None:
    """Not a tool."""
    return None
