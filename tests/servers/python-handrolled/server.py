#!/usr/bin/env python3
"""Hand-rolled MCP server: Python stdlib only.

Modes:
  python3 server.py stdio            newline-delimited JSON-RPC 2.0 over stdio
  python3 server.py http --port N    legacy HTTP+SSE transport (2024-11-05):
                                     GET /sse, POST /messages?session_id=...

tools/list paginates on purpose: the first page holds two tools and a
nextCursor of "page2"; the second page holds the third tool.
"""

import argparse
import json
import queue
import sys
import threading
import uuid
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
from urllib.parse import parse_qs, urlparse

SERVER_INFO = {"name": "mcpcheck-python-handrolled", "version": "0.1.0"}
KNOWN_VERSIONS = ["2024-11-05", "2025-03-26", "2025-06-18", "2025-11-25"]
DEFAULT_VERSION = "2025-06-18"
PAGE_SIZE = 2
PAGE2_CURSOR = "page2"

TOOLS = [
    {
        "name": "get_weather",
        "description": "Get the current weather for a city.",
        "inputSchema": {
            "type": "object",
            "properties": {
                "city": {"type": "string", "description": "City name"},
                "units": {"type": "string", "enum": ["celsius", "fahrenheit"]},
            },
            "required": ["city"],
        },
        "annotations": {"readOnlyHint": True},
    },
    {
        "name": "delete_file",
        "description": "Delete a file inside the sandbox directory.",
        "inputSchema": {
            "type": "object",
            "properties": {
                "path": {"type": "string"},
                "confirm": {"type": "boolean"},
            },
            "required": ["path", "confirm"],
        },
        "annotations": {"destructiveHint": True},
    },
    {
        "name": "fetch_url",
        "description": "Fetch a URL and return the response body.",
        "inputSchema": {
            "type": "object",
            "properties": {
                "url": {"type": "string"},
            },
            "required": ["url"],
        },
        "annotations": {"openWorldHint": True},
    },
]


class RPCError(Exception):
    def __init__(self, code, message):
        super().__init__(message)
        self.code = code
        self.message = message


def text_result(text):
    return {"content": [{"type": "text", "text": text}], "isError": False}


def get_weather(args):
    return text_result("Sunny in %s" % args["city"])


def delete_file(args):
    # Never touches the filesystem.
    return text_result("would delete %s" % args["path"])


def fetch_url(args):
    # Never touches the network.
    return text_result("would fetch %s" % args["url"])


HANDLERS = {"get_weather": get_weather, "delete_file": delete_file, "fetch_url": fetch_url}


def handle_initialize(params):
    requested = params.get("protocolVersion")
    version = requested if requested in KNOWN_VERSIONS else DEFAULT_VERSION
    return {
        "protocolVersion": version,
        "capabilities": {"tools": {"listChanged": False}},
        "serverInfo": SERVER_INFO,
    }


def handle_tools_list(params):
    cursor = params.get("cursor")
    if cursor is None:
        return {"tools": TOOLS[:PAGE_SIZE], "nextCursor": PAGE2_CURSOR}
    if cursor == PAGE2_CURSOR:
        return {"tools": TOOLS[PAGE_SIZE:]}
    raise RPCError(-32602, "invalid cursor")


def handle_tools_call(params):
    name = params.get("name")
    handler = HANDLERS.get(name)
    if handler is None:
        raise RPCError(-32602, "unknown tool: %s" % name)
    args = params.get("arguments") or {}
    tool = next(t for t in TOOLS if t["name"] == name)
    missing = [k for k in tool["inputSchema"]["required"] if k not in args]
    if missing:
        return {"content": [{"type": "text", "text": "missing arguments: %s" % ", ".join(missing)}], "isError": True}
    return handler(args)


METHODS = {
    "initialize": handle_initialize,
    "ping": lambda params: {},
    "tools/list": handle_tools_list,
    "tools/call": handle_tools_call,
}


def dispatch(msg):
    """Return the JSON-RPC response for msg, or None for notifications."""
    if not isinstance(msg, dict) or msg.get("jsonrpc") != "2.0":
        return {"jsonrpc": "2.0", "id": None, "error": {"code": -32600, "message": "invalid request"}}
    if "method" not in msg:
        return None  # a response to something we never sent; ignore
    is_request = "id" in msg
    method = msg["method"]
    fn = METHODS.get(method)
    if not is_request:
        return None  # notifications/initialized, notifications/cancelled, ...
    if fn is None:
        return {"jsonrpc": "2.0", "id": msg["id"], "error": {"code": -32601, "message": "method not found: %s" % method}}
    try:
        result = fn(msg.get("params") or {})
    except RPCError as e:
        return {"jsonrpc": "2.0", "id": msg["id"], "error": {"code": e.code, "message": e.message}}
    except Exception as e:  # pylint: disable=broad-except
        return {"jsonrpc": "2.0", "id": msg["id"], "error": {"code": -32603, "message": str(e)}}
    return {"jsonrpc": "2.0", "id": msg["id"], "result": result}


def handle_raw(raw):
    try:
        msg = json.loads(raw)
    except ValueError:
        return {"jsonrpc": "2.0", "id": None, "error": {"code": -32700, "message": "parse error"}}
    return dispatch(msg)


def serve_stdio():
    for line in sys.stdin:
        line = line.strip()
        if not line:
            continue
        resp = handle_raw(line)
        if resp is not None:
            sys.stdout.write(json.dumps(resp) + "\n")
            sys.stdout.flush()


# ---------------------------------------------------------------- HTTP+SSE

SESSIONS = {}
SESSIONS_LOCK = threading.Lock()


class SSEHandler(BaseHTTPRequestHandler):
    protocol_version = "HTTP/1.1"

    def log_message(self, fmt, *args):
        sys.stderr.write("%s - %s\n" % (self.address_string(), fmt % args))

    def do_GET(self):  # noqa: N802
        if urlparse(self.path).path != "/sse":
            self.send_error(404)
            return
        session_id = uuid.uuid4().hex
        outbox = queue.Queue()
        with SESSIONS_LOCK:
            SESSIONS[session_id] = outbox
        self.send_response(200)
        self.send_header("Content-Type", "text/event-stream")
        self.send_header("Cache-Control", "no-cache")
        self.send_header("Connection", "keep-alive")
        self.end_headers()
        try:
            self._event("endpoint", "/messages?session_id=%s" % session_id)
            while True:
                try:
                    msg = outbox.get(timeout=15)
                except queue.Empty:
                    self.wfile.write(b": keepalive\n\n")
                    self.wfile.flush()
                    continue
                self._event("message", json.dumps(msg))
        except (BrokenPipeError, ConnectionResetError):
            pass
        finally:
            with SESSIONS_LOCK:
                SESSIONS.pop(session_id, None)
            self.close_connection = True

    def _event(self, event, data):
        self.wfile.write(("event: %s\ndata: %s\n\n" % (event, data)).encode())
        self.wfile.flush()

    def do_POST(self):  # noqa: N802
        url = urlparse(self.path)
        if url.path != "/messages":
            self.send_error(404)
            return
        session_id = parse_qs(url.query).get("session_id", [""])[0]
        with SESSIONS_LOCK:
            outbox = SESSIONS.get(session_id)
        if outbox is None:
            self._reply(404, b"unknown session")
            return
        length = int(self.headers.get("Content-Length") or 0)
        resp = handle_raw(self.rfile.read(length))
        self._reply(202, b"Accepted")
        if resp is not None:
            outbox.put(resp)

    def _reply(self, status, body):
        self.send_response(status)
        self.send_header("Content-Type", "text/plain")
        self.send_header("Content-Length", str(len(body)))
        self.end_headers()
        self.wfile.write(body)


def serve_http(port):
    httpd = ThreadingHTTPServer(("127.0.0.1", port), SSEHandler)
    httpd.daemon_threads = True
    sys.stderr.write("listening on http://127.0.0.1:%d/sse\n" % port)
    try:
        httpd.serve_forever()
    except KeyboardInterrupt:
        pass


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("mode", choices=["stdio", "http", "sse"], nargs="?", default="stdio")
    ap.add_argument("--port", type=int, default=8000)
    args = ap.parse_args()
    if args.mode in ("http", "sse"):
        serve_http(args.port)
    else:
        serve_stdio()


if __name__ == "__main__":
    main()
