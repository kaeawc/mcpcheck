package scanner

import (
	"path/filepath"
	"testing"
)

func TestScanPython_PatternsAndDocstrings(t *testing.T) {
	src := `
@mcp.tool()
def fetch_user(user_id: str) -> dict:
    """Fetch a user by id."""
    return {}

@mcp.tool()
async def send_message(channel: str, body: str) -> None:
    """Post a message to a channel.

    The body is plain text.
    """
    return

@server.tool(name="custom", description="Custom kwarg description.")
def _internal(arg: str) -> str:
    """This docstring is overridden."""
    return arg

@mcp.tool
def bare_decorator() -> str:
    '''Bare-form decorator.'''
    return "pong"

@mcp.tool()
@retry(
    attempts=3,
)
def stacked(x: int) -> int:
    """Decorator stacked above the function."""
    return x

@mcp.tool(
    "positional_name",
    title="Multi-line decorator",
)
def renamed():
    pass

# Not a tool decorator — must be ignored.
@mcp.something_else()
def not_a_tool() -> None:
    """Nope."""

@server.call_tool()
async def handle_call(name, arguments):
    pass
`
	tools := scanPython("server.py", src)
	want := []struct {
		name, desc string
		line       int
	}{
		{"fetch_user", "Fetch a user by id.", 2},
		{"send_message", "Post a message to a channel.\n\nThe body is plain text.", 7},
		{"custom", "Custom kwarg description.", 15},
		{"bare_decorator", "Bare-form decorator.", 20},
		{"stacked", "Decorator stacked above the function.", 25},
		{"positional_name", "", 33},
	}
	if len(tools) != len(want) {
		t.Fatalf("got %d tools, want %d: %+v", len(tools), len(want), tools)
	}
	for i, w := range want {
		if tools[i].Name != w.name || tools[i].Description != w.desc || tools[i].SourceLine != w.line {
			t.Errorf("tools[%d] = %q %q line %d, want %q %q line %d",
				i, tools[i].Name, tools[i].Description, tools[i].SourceLine, w.name, w.desc, w.line)
		}
	}
	if tools[5].Title != "Multi-line decorator" {
		t.Errorf("title = %q", tools[5].Title)
	}
}

func TestScanPython_SignatureSchema(t *testing.T) {
	src := `
from typing import Annotated, Literal, Optional
from pydantic import Field

@mcp.tool(annotations=ToolAnnotations(read_only_hint=True, openWorldHint=False))
async def search(
    query: Annotated[str, Field(description="Search terms", min_length=1)],
    ctx: Context,
    limit: int = 10,
    units: Literal["celsius", "fahrenheit"] | None = None,
    tags: list[str] = Field(default_factory=list, max_length=5),
    url: str = Field(..., pattern=r"^https://"),
    since: Optional[datetime] = None,
    *,
    verbose: bool = False,
    **extra,
) -> list[dict]:
    """Search things."""
`
	tools := scanPython("s.py", src)
	if len(tools) != 1 {
		t.Fatalf("got %d tools", len(tools))
	}
	want := `{"properties":{"limit":{"default":10,"type":"integer"},"query":{"description":"Search terms","minLength":1,"type":"string"},` +
		`"since":{"format":"date-time","type":"string"},"tags":{"items":{"type":"string"},"maxItems":5,"type":"array"},` +
		`"units":{"enum":["celsius","fahrenheit"],"type":"string"},"url":{"pattern":"^https://","type":"string"},"verbose":{"default":false,"type":"boolean"}},` +
		`"required":["query","url"],"type":"object"}`
	if got := schemaJSON(tools[0].InputSchema); got != want {
		t.Errorf("schema:\n got %s\nwant %s", got, want)
	}
	if a := tools[0].Annotations; a["readOnlyHint"] != true || a["openWorldHint"] != false {
		t.Errorf("annotations = %v", a)
	}
}

func TestScanPython_LowLevelAndHandRolled(t *testing.T) {
	src := `
@server.list_tools()
async def list_tools() -> list[types.Tool]:
    return [
        types.Tool(
            name="get_weather",
            description="Get weather.",
            inputSchema={"type": "object", "properties": {"city": {"type": "string"}}, "required": ["city"]},
            annotations=types.ToolAnnotations(destructiveHint=False),
        ),
        Tool(name="dynamic", description="x", inputSchema=build()),
    ]

TOOLS = [
    {"name": "echo", "description": "Echo text.", "inputSchema": {"type": "object", "properties": {"text": {"type": "string"}}}},
]

def add(a: int) -> int:
    """Add."""
mcp.add_tool(add, name="adder")
`
	tools := scanPython("s.py", src)
	byName := map[string]int{}
	for i, tool := range tools {
		byName[tool.Name] = i
	}
	if len(tools) != 4 {
		t.Fatalf("got %d tools: %+v", len(tools), tools)
	}
	w := tools[byName["get_weather"]]
	if w.SourceLine != 5 || schemaJSON(w.InputSchema) != `{"properties":{"city":{"type":"string"}},"required":["city"],"type":"object"}` ||
		w.Annotations["destructiveHint"] != false {
		t.Errorf("get_weather: %+v", w)
	}
	if tools[byName["dynamic"]].InputSchema != nil {
		t.Error("dynamic schema should be unknown")
	}
	if e := tools[byName["echo"]]; e.Description != "Echo text." || e.SourceLine != 15 {
		t.Errorf("echo: %+v", e)
	}
	if a := tools[byName["adder"]]; a.Description != "Add." || schemaJSON(a.InputSchema) != `{"properties":{"a":{"type":"integer"}},"required":["a"],"type":"object"}` {
		t.Errorf("adder: %+v", a)
	}
}

func TestScanPython_StackedDecoratorsAndConstants(t *testing.T) {
	src := `
class Tools(str, Enum):
    STATUS = "git_status"

    LOG = "git_log"

READ_ONLY = ToolAnnotations(readOnlyHint=True)
LOG_DESC = "Shows the commit log"

def build(server):
    @server.tool(name=Tools.STATUS.value, description="Shows status", annotations=READ_ONLY)
    @reports_failures
    def status(repo_path: str) -> str:
        return ""

    @server.tool(name=Tools.LOG, description=LOG_DESC)
    @reports_failures
    @other
    async def log(repo_path: str, max_count: int = 10) -> str:
        return ""
`
	tools := scanPython("s.py", src)
	if len(tools) != 2 {
		t.Fatalf("got %d tools: %+v", len(tools), tools)
	}
	if tools[0].Name != "git_status" || tools[0].Annotations["readOnlyHint"] != true {
		t.Errorf("status: %+v", tools[0])
	}
	if tools[1].Name != "git_log" || tools[1].Description != "Shows the commit log" {
		t.Errorf("log: %+v", tools[1])
	}
}

func TestCleandoc(t *testing.T) {
	got := cleandoc("First line.\n\n        Indented body.\n          More.\n    ")
	if want := "First line.\n\nIndented body.\n  More."; got != want {
		t.Errorf("cleandoc = %q, want %q", got, want)
	}
}

func TestLoad_DispatchByExtension(t *testing.T) {
	jsonSet, err := Load(filepath.Join("..", "..", "tests", "fixtures", "positive", "tool-name-not-snake-case", "tools.json"))
	if err != nil || len(jsonSet.Tools) == 0 {
		t.Fatalf("Load(json): %v, %d tools", err, len(jsonSet.Tools))
	}
	pySet, err := Load(filepath.Join("..", "..", "tests", "fixtures", "intake", "python", "server.py"))
	if err != nil || len(pySet.Tools) == 0 {
		t.Fatalf("Load(py): %v", err)
	}
	if _, err := Load(filepath.Join("..", "..", "README.md")); err == nil {
		t.Error("Load(.md) should fail")
	}
}
