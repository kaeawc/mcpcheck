package scanner

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/kaeawc/mcpcheck/internal/mcpmodel"
)

func TestScanPythonReader_PatternsAndDocstrings(t *testing.T) {
	src := `
@mcp.tool()
def fetch_user(user_id: str) -> dict:
    """Fetch a user by id."""
    return {}

@mcp.tool()
def send_message(channel: str, body: str) -> None:
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
    """Bare-form decorator."""
    return "pong"

@mcp.tool()
@some_other_decorator
def stacked(x: int) -> int:
    """Decorator stacked above the function."""
    return x

# Not a tool decorator — must be ignored.
@mcp.something_else()
def not_a_tool() -> None:
    """Nope."""
`
	tools, err := scanPythonReader("inline.py", strings.NewReader(src))
	if err != nil {
		t.Fatalf("scanPythonReader: %v", err)
	}

	want := []mcpmodel.Tool{
		{Name: "fetch_user", Description: "Fetch a user by id.", SourceFile: "inline.py"},
		{Name: "send_message", Description: "Post a message to a channel.", SourceFile: "inline.py"},
		{Name: "custom", Description: "Custom kwarg description.", SourceFile: "inline.py"},
		{Name: "bare_decorator", Description: "Bare-form decorator.", SourceFile: "inline.py"},
		{Name: "stacked", Description: "Decorator stacked above the function.", SourceFile: "inline.py"},
	}
	if got := len(tools); got != len(want) {
		t.Fatalf("got %d tools, want %d: %+v", got, len(want), tools)
	}
	for i, w := range want {
		got := tools[i]
		if got.Name != w.Name {
			t.Errorf("tool %d: name = %q, want %q", i, got.Name, w.Name)
		}
		if got.Description != w.Description {
			t.Errorf("tool %d (%s): description = %q, want %q", i, got.Name, got.Description, w.Description)
		}
		if got.SourceFile != w.SourceFile {
			t.Errorf("tool %d (%s): source file = %q, want %q", i, got.Name, got.SourceFile, w.SourceFile)
		}
		if got.SourceLine <= 0 {
			t.Errorf("tool %d (%s): source line = %d, want > 0", i, got.Name, got.SourceLine)
		}
	}
}

func TestLoadPythonFile_Fixture(t *testing.T) {
	path := filepath.Join("..", "..", "tests", "fixtures", "intake", "python", "server.py")
	set, err := LoadPythonFile(path)
	if err != nil {
		t.Fatalf("LoadPythonFile: %v", err)
	}

	wantNames := []string{"fetch_user", "send_message", "custom_named_tool", "ping_no_parens"}
	if len(set.Tools) != len(wantNames) {
		t.Fatalf("got %d tools, want %d: %+v", len(set.Tools), len(wantNames), set.Tools)
	}
	for i, want := range wantNames {
		if set.Tools[i].Name != want {
			t.Errorf("tools[%d].Name = %q, want %q", i, set.Tools[i].Name, want)
		}
		if set.Tools[i].SourceFile == "" || set.Tools[i].SourceLine == 0 {
			t.Errorf("tools[%d] (%s): source location not populated (file=%q line=%d)",
				i, set.Tools[i].Name, set.Tools[i].SourceFile, set.Tools[i].SourceLine)
		}
	}

	// `helper` and `not_a_tool` must not appear.
	for _, tool := range set.Tools {
		if tool.Name == "helper" || tool.Name == "not_a_tool" {
			t.Errorf("undecorated function leaked into tool set: %q", tool.Name)
		}
	}
}

func TestLoadPythonDir_RecursiveAndSkipsJunk(t *testing.T) {
	dir := filepath.Join("..", "..", "tests", "fixtures", "intake", "python")
	set, err := LoadPythonDir(dir)
	if err != nil {
		t.Fatalf("LoadPythonDir: %v", err)
	}
	if len(set.Tools) == 0 {
		t.Fatalf("expected at least one tool from the fixture directory")
	}
}

func TestExtractDocstringFirstLine(t *testing.T) {
	cases := []struct {
		name string
		body []string // lines after the def line
		want string
	}{
		{
			name: "single-line",
			body: []string{`    """One liner."""`, `    return None`},
			want: "One liner.",
		},
		{
			name: "multi-line, content on opening",
			body: []string{`    """First line.`, ``, `    Second paragraph.`, `    """`, `    return None`},
			want: "First line.",
		},
		{
			name: "multi-line, opening line empty",
			body: []string{`    """`, `    First non-empty line.`, ``, `    Other content.`, `    """`, `    return None`},
			want: "First non-empty line.",
		},
		{
			name: "no docstring",
			body: []string{`    return None`},
			want: "",
		},
		{
			name: "single-quote triple",
			body: []string{`    '''Tripled single quotes.'''`, `    return None`},
			want: "Tripled single quotes.",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := extractDocstringFirstLine(tc.body, 0); got != tc.want {
				t.Fatalf("extractDocstringFirstLine = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestLoad_DispatchByExtension(t *testing.T) {
	jsonPath := filepath.Join("..", "..", "tests", "fixtures", "positive", "tool-name-not-snake-case", "tools.json")
	pySetPath := filepath.Join("..", "..", "tests", "fixtures", "intake", "python", "server.py")

	jsonSet, err := Load(jsonPath)
	if err != nil {
		t.Fatalf("Load(json): %v", err)
	}
	if len(jsonSet.Tools) == 0 {
		t.Fatal("Load(json) returned empty tool set")
	}

	pySet, err := Load(pySetPath)
	if err != nil {
		t.Fatalf("Load(py): %v", err)
	}
	if len(pySet.Tools) == 0 {
		t.Fatal("Load(py) returned empty tool set")
	}
}
