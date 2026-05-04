package scanner

import (
	"path/filepath"
	"testing"
)

func TestScanTypeScript_BasicShapes(t *testing.T) {
	src := `
server.tool("fetch_user", "Fetch a user by id.", schema, handler);
mcp.tool('send_message', 'Post a message.', {}, async () => null);

server.tool(
  "long_decl",
  "A multi-line declaration.",
  {},
  async () => null,
);

// Not a tool call.
server.setRequestHandler(ListToolsRequestSchema, async () => ({}));
`
	tools := scanTypeScript("inline.ts", src)
	if got, want := len(tools), 3; got != want {
		t.Fatalf("got %d tools, want %d: %+v", got, want, tools)
	}

	want := []struct{ name, desc string }{
		{"fetch_user", "Fetch a user by id."},
		{"send_message", "Post a message."},
		{"long_decl", "A multi-line declaration."},
	}
	for i, w := range want {
		if tools[i].Name != w.name {
			t.Errorf("tools[%d].Name = %q, want %q", i, tools[i].Name, w.name)
		}
		if tools[i].Description != w.desc {
			t.Errorf("tools[%d].Description = %q, want %q", i, tools[i].Description, w.desc)
		}
		if tools[i].SourceLine <= 0 {
			t.Errorf("tools[%d].SourceLine = %d, want > 0", i, tools[i].SourceLine)
		}
	}
}

func TestScanTypeScript_NoMatches(t *testing.T) {
	src := `
const x = 1;
function notATool() {
  return server.handle("foo", "bar");
}
`
	if tools := scanTypeScript("inline.ts", src); len(tools) != 0 {
		t.Fatalf("expected no tools, got %+v", tools)
	}
}

func TestLoadTypeScriptFile_Fixture(t *testing.T) {
	path := filepath.Join("..", "..", "tests", "fixtures", "intake", "typescript", "server.ts")
	set, err := LoadTypeScriptFile(path)
	if err != nil {
		t.Fatalf("LoadTypeScriptFile: %v", err)
	}

	wantNames := []string{"fetch_user", "send_message", "ping", "long_decl", "util_tool"}
	if len(set.Tools) != len(wantNames) {
		t.Fatalf("got %d tools, want %d: %+v", len(set.Tools), len(wantNames), set.Tools)
	}
	for i, want := range wantNames {
		if set.Tools[i].Name != want {
			t.Errorf("tools[%d].Name = %q, want %q", i, set.Tools[i].Name, want)
		}
		if set.Tools[i].SourceFile == "" || set.Tools[i].SourceLine == 0 {
			t.Errorf("tools[%d] (%s): source location not populated", i, set.Tools[i].Name)
		}
	}

	// `setRequestHandler` and `function notATool` must not appear.
	for _, tool := range set.Tools {
		if tool.Name == "ignored" {
			t.Errorf("setRequestHandler payload leaked into tool set")
		}
	}
}

func TestByteOffsetToLine(t *testing.T) {
	src := "a\nbb\nccc\n"
	cases := []struct {
		offset int
		want   int
	}{
		{0, 1},
		{1, 1}, // newline at offset 1 itself counts as line 1's terminator
		{2, 2},
		{4, 2},
		{5, 3},
		{8, 3},
		{9, 4},
		{100, 4}, // past end clamps
		{-1, 0},  // negative returns 0 (sentinel)
	}
	for _, tc := range cases {
		t.Run("", func(t *testing.T) {
			if got := byteOffsetToLine(src, tc.offset); got != tc.want {
				t.Fatalf("byteOffsetToLine(_, %d) = %d, want %d", tc.offset, got, tc.want)
			}
		})
	}
}

func TestLoad_TypeScriptDispatch(t *testing.T) {
	tsPath := filepath.Join("..", "..", "tests", "fixtures", "intake", "typescript", "server.ts")
	set, err := Load(tsPath)
	if err != nil {
		t.Fatalf("Load(.ts): %v", err)
	}
	if len(set.Tools) == 0 {
		t.Fatalf("Load(.ts) returned empty tool set")
	}

	tsDir := filepath.Join("..", "..", "tests", "fixtures", "intake", "typescript")
	dirSet, err := Load(tsDir)
	if err != nil {
		t.Fatalf("Load(dir): %v", err)
	}
	if len(dirSet.Tools) == 0 {
		t.Fatalf("Load(dir) returned empty tool set; dir scan should pick up .ts files")
	}
}
