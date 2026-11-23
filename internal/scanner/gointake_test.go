package scanner

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/kaeawc/mcpcheck/internal/mcpmodel"
)

// loadGoSources writes files (relative path -> contents) under a temp dir
// and runs LoadGo on it.
func loadGoSources(t *testing.T, files map[string]string) (string, []mcpmodel.Tool) {
	t.Helper()
	dir := t.TempDir()
	for name, src := range files {
		path := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(src), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	set, err := LoadGo(dir)
	if err != nil {
		t.Fatalf("LoadGo: %v", err)
	}
	if set.Source != dir {
		t.Errorf("Source = %q, want %q", set.Source, dir)
	}
	return dir, set.Tools
}

func goToolByName(t *testing.T, tools []mcpmodel.Tool, name string) mcpmodel.Tool {
	t.Helper()
	for _, tool := range tools {
		if tool.Name == name {
			return tool
		}
	}
	t.Fatalf("tool %q not found in %+v", name, toolNames(tools))
	return mcpmodel.Tool{}
}

func toolNames(tools []mcpmodel.Tool) []string {
	out := make([]string, len(tools))
	for i, tool := range tools {
		out[i] = tool.Name
	}
	return out
}

func assertToolNames(t *testing.T, tools []mcpmodel.Tool, want ...string) {
	t.Helper()
	if got := toolNames(tools); !reflect.DeepEqual(got, want) {
		t.Fatalf("tool names = %v, want %v", got, want)
	}
}

func assertDeepEqual(t *testing.T, what string, got, want any) {
	t.Helper()
	if !reflect.DeepEqual(got, want) {
		t.Errorf("%s:\n got  %#v\n want %#v", what, got, want)
	}
}

func TestLoadGo_CorpusServers(t *testing.T) {
	weather := map[string]any{
		"city":  map[string]any{"type": "string", "description": "City name"},
		"units": map[string]any{"type": "string", "enum": []any{"celsius", "fahrenheit"}},
	}
	cases := []struct {
		dir      string
		extra    map[string]any // keys the SDK adds to every inferred object schema
		readOnly map[string]any // get_weather annotations
	}{
		{
			dir:      "go-sdk",
			extra:    map[string]any{"additionalProperties": false},
			readOnly: map[string]any{"readOnlyHint": true, "idempotentHint": false},
		},
		{
			dir:      "go-mcpgo",
			extra:    map[string]any{},
			readOnly: map[string]any{"readOnlyHint": true, "destructiveHint": true, "idempotentHint": false, "openWorldHint": true},
		},
	}
	for _, tc := range cases {
		t.Run(tc.dir, func(t *testing.T) {
			dir := filepath.Join("..", "..", "tests", "servers", tc.dir)
			if _, err := os.Stat(filepath.Join(dir, "main.go")); err != nil {
				t.Skipf("corpus server not present: %v", err)
			}
			set, err := LoadGo(dir)
			if err != nil {
				t.Fatalf("LoadGo: %v", err)
			}
			assertToolNames(t, set.Tools, "get_weather", "delete_file", "fetch_url")
			schema := func(props map[string]any, required ...any) map[string]any {
				s := map[string]any{"type": "object", "properties": props, "required": required}
				for k, v := range tc.extra {
					s[k] = v
				}
				return s
			}

			w := goToolByName(t, set.Tools, "get_weather")
			assertDeepEqual(t, "get_weather description", w.Description, "Get the current weather for a city.")
			assertDeepEqual(t, "get_weather schema", w.InputSchema, schema(weather, "city"))
			assertDeepEqual(t, "get_weather annotations", w.Annotations, tc.readOnly)

			d := goToolByName(t, set.Tools, "delete_file")
			assertDeepEqual(t, "delete_file description", d.Description, "Delete a file inside the sandbox directory.")
			assertDeepEqual(t, "delete_file schema", d.InputSchema, schema(map[string]any{
				"path":    map[string]any{"type": "string"},
				"confirm": map[string]any{"type": "boolean"},
			}, "path", "confirm"))
			assertDeepEqual(t, "delete_file destructiveHint", d.Annotations["destructiveHint"], true)

			f := goToolByName(t, set.Tools, "fetch_url")
			assertDeepEqual(t, "fetch_url description", f.Description, "Fetch a URL and return the response body.")
			assertDeepEqual(t, "fetch_url schema", f.InputSchema, schema(map[string]any{
				"url": map[string]any{"type": "string"},
			}, "url"))
			assertDeepEqual(t, "fetch_url openWorldHint", f.Annotations["openWorldHint"], true)

			for _, tool := range set.Tools {
				if tool.Raw != nil {
					t.Errorf("%s: Raw should be nil for a source intake", tool.Name)
				}
				if tool.SourceFile != filepath.Join(dir, "main.go") || tool.SourceLine == 0 {
					t.Errorf("%s: source = %s:%d", tool.Name, tool.SourceFile, tool.SourceLine)
				}
			}
		})
	}
}

func TestLoadGo_SDKAliasedImportExplicitSchema(t *testing.T) {
	dir, tools := loadGoSources(t, map[string]string{"main.go": `package main

import (
	"github.com/google/jsonschema-go/jsonschema"
	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

func ptr[T any](v T) *T { return &v }

func register(s *sdk.Server) {
	yes := true
	sdk.AddTool(s, &sdk.Tool{
		Name:        "search",
		Title:       "Search",
		Description: "Search the index.",
		InputSchema: &jsonschema.Schema{
			Type: "object",
			Properties: map[string]*jsonschema.Schema{
				"query": {Type: "string", Description: "Search terms", MinLength: jsonschema.Ptr(1)},
				"limit": {Type: "integer", Minimum: jsonschema.Ptr(1.0), Maximum: jsonschema.Ptr(50.0)},
				"tags":  {Types: []string{"null", "array"}, Items: &jsonschema.Schema{Type: "string"}},
			},
			Required: []string{"query"},
		},
		Annotations: &sdk.ToolAnnotations{
			ReadOnlyHint:    true,
			DestructiveHint: ptr(false),
			OpenWorldHint:   &yes,
			Title:           "Search tool",
		},
	}, handleSearch)

	s.AddTool(&sdk.Tool{
		Name:        "raw",
		Description: "Raw handler with a map schema.",
		InputSchema: map[string]any{"type": "object", "properties": map[string]any{"x": map[string]any{"type": "number"}}},
	}, rawHandler)
}
`})
	assertToolNames(t, tools, "search", "raw")

	search := tools[0]
	assertDeepEqual(t, "title", search.Title, "Search")
	assertDeepEqual(t, "description", search.Description, "Search the index.")
	assertDeepEqual(t, "schema", search.InputSchema, map[string]any{
		"type": "object",
		"properties": map[string]any{
			"query": map[string]any{"type": "string", "description": "Search terms", "minLength": 1.0},
			"limit": map[string]any{"type": "integer", "minimum": 1.0, "maximum": 50.0},
			"tags":  map[string]any{"type": []any{"null", "array"}, "items": map[string]any{"type": "string"}},
		},
		"required": []any{"query"},
	})
	assertDeepEqual(t, "annotations", search.Annotations, map[string]any{
		"readOnlyHint": true, "destructiveHint": false, "openWorldHint": true,
		"idempotentHint": false, "title": "Search tool",
	})
	assertDeepEqual(t, "source", []any{search.SourceFile, search.SourceLine}, []any{filepath.Join(dir, "main.go"), 12})

	raw := tools[1]
	assertDeepEqual(t, "raw schema", raw.InputSchema, map[string]any{
		"type": "object", "properties": map[string]any{"x": map[string]any{"type": "number"}},
	})
	if raw.Annotations != nil {
		t.Errorf("raw annotations = %v, want nil (none declared)", raw.Annotations)
	}
}

func TestLoadGo_SDKVariablesAndConstants(t *testing.T) {
	_, tools := loadGoSources(t, map[string]string{"main.go": `package main

import "github.com/modelcontextprotocol/go-sdk/mcp"

const (
	echoName = "echo"
	prefix   = "Echo the "
)

var pkgTool = &mcp.Tool{Name: "pkg_level", Description: "Declared at package level."}

type echoIn struct {
	Text string ` + "`json:\"text\"`" + `
}

func echo(ctx context.Context, req *mcp.CallToolRequest, in echoIn) (*mcp.CallToolResult, any, error) {
	return nil, nil, nil
}

func register(s *mcp.Server) {
	tool := &mcp.Tool{Name: echoName, Description: prefix + "input" + "."}
	mcp.AddTool(s, tool, echo)
	mcp.AddTool(s, pkgTool, echo)
}
`})
	assertToolNames(t, tools, "pkg_level", "echo")
	want := map[string]any{
		"type":                 "object",
		"additionalProperties": false,
		"properties":           map[string]any{"text": map[string]any{"type": "string"}},
		"required":             []any{"text"},
	}
	for _, tool := range tools {
		assertDeepEqual(t, tool.Name+" schema", tool.InputSchema, want)
	}
	assertDeepEqual(t, "echo description", tools[1].Description, "Echo the input.")
}

func TestLoadGo_SDKCrossFileStructInference(t *testing.T) {
	_, tools := loadGoSources(t, map[string]string{
		"server/main.go": `package main

import (
	"context"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func register(s *mcp.Server) {
	mcp.AddTool(s, &mcp.Tool{Name: "book", Description: "Book a trip."}, book)
	mcp.AddTool[queryIn, queryOut](s, &mcp.Tool{Name: "query", Description: "Explicit type args."}, nil)
}

func book(ctx context.Context, req *mcp.CallToolRequest, in *bookIn) (*mcp.CallToolResult, bookOut, error) {
	return nil, bookOut{}, nil
}
`,
		"server/types.go": "package main\n\nimport \"time\"\n\n" +
			"type common struct {\n\tTraceID string `json:\"trace_id,omitempty\"`\n\tCity string `json:\"city\"`\n}\n\n" +
			"type address struct {\n\tStreet string `json:\"street\"`\n}\n\n" +
			"type bookIn struct {\n" +
			"\tcommon\n" +
			"\tCity string `json:\"city\" jsonschema:\"Destination city\"`\n" +
			"\tNights int `json:\"nights\"`\n" +
			"\tRooms uint8 `json:\"rooms,omitempty\"`\n" +
			"\tBudget *float64 `json:\"budget\"`\n" +
			"\tTags []string `json:\"tags,omitzero\"`\n" +
			"\tSeats [2]int `json:\"seats\"`\n" +
			"\tPrefs map[string]bool `json:\"prefs,omitempty\"`\n" +
			"\tWhen time.Time `json:\"when\"`\n" +
			"\tHome address `json:\"home\"`\n" +
			"\tAnything any `json:\"anything,omitempty\"`\n" +
			"\tUntagged string\n" +
			"\tDash string `json:\"-,\"`\n" +
			"\tSkipped string `json:\"-\"`\n" +
			"\tinternal string\n" +
			"}\n\n" +
			"type bookOut struct {\n\tID string `json:\"id\"`\n}\n\n" +
			"type queryIn struct {\n\tQ string `json:\"q\"`\n}\n\n" +
			"type queryOut struct {\n\tN int `json:\"n\"`\n}\n",
	})
	assertToolNames(t, tools, "book", "query")

	book := tools[0]
	assertDeepEqual(t, "book schema", book.InputSchema, map[string]any{
		"type":                 "object",
		"additionalProperties": false,
		"properties": map[string]any{
			"trace_id": map[string]any{"type": "string"},
			"city":     map[string]any{"type": "string", "description": "Destination city"},
			"nights":   map[string]any{"type": "integer"},
			"rooms":    map[string]any{"type": "integer", "minimum": 0.0, "maximum": 255.0},
			"budget":   map[string]any{"type": []any{"null", "number"}},
			"tags":     map[string]any{"type": []any{"null", "array"}, "items": map[string]any{"type": "string"}},
			"seats":    map[string]any{"type": "array", "items": map[string]any{"type": "integer"}, "minItems": 2.0, "maxItems": 2.0},
			"prefs":    map[string]any{"type": "object", "additionalProperties": map[string]any{"type": "boolean"}},
			"when":     map[string]any{"type": "string"},
			"home": map[string]any{
				"type": "object", "additionalProperties": false,
				"properties": map[string]any{"street": map[string]any{"type": "string"}},
				"required":   []any{"street"},
			},
			"anything": map[string]any{},
			"Untagged": map[string]any{"type": "string"},
			"-":        map[string]any{"type": "string"},
		},
		"required": []any{"city", "nights", "budget", "seats", "when", "home", "Untagged", "-"},
	})
	assertDeepEqual(t, "book output schema", book.OutputSchema, map[string]any{
		"type": "object", "additionalProperties": false,
		"properties": map[string]any{"id": map[string]any{"type": "string"}},
		"required":   []any{"id"},
	})

	query := tools[1]
	assertDeepEqual(t, "query input properties", query.InputSchema["properties"], map[string]any{"q": map[string]any{"type": "string"}})
	assertDeepEqual(t, "query output properties", query.OutputSchema["properties"], map[string]any{"n": map[string]any{"type": "integer"}})
}

func TestLoadGo_SDKDotImportFuncLitAndMethodHandlers(t *testing.T) {
	_, tools := loadGoSources(t, map[string]string{"main.go": `package main

import . "github.com/modelcontextprotocol/go-sdk/mcp"

type svc struct{}

type pingIn struct {
	Host string ` + "`json:\"host\"`" + `
}

func (s *svc) ping(ctx context.Context, req *CallToolRequest, in pingIn) (*CallToolResult, any, error) {
	return nil, nil, nil
}

func register(server *Server, s *svc) {
	AddTool(server, &Tool{Name: "ping", Description: "Ping a host."}, s.ping)
	AddTool(server, &Tool{Name: "inline", Description: "Inline handler."},
		func(ctx context.Context, req *CallToolRequest, in struct {
			N int ` + "`json:\"n,omitempty\"`" + `
		}) (*CallToolResult, any, error) {
			return nil, nil, nil
		})
	AddTool(server, &Tool{Name: "anything", Description: "Untyped input."},
		func(ctx context.Context, req *CallToolRequest, in any) (*CallToolResult, any, error) {
			return nil, nil, nil
		})
	AddTool(server, &Tool{Name: "opaque", Description: "Schema built at runtime.", InputSchema: buildSchema()},
		func(ctx context.Context, req *CallToolRequest, in any) (*CallToolResult, any, error) {
			return nil, nil, nil
		})
}
`})
	assertToolNames(t, tools, "ping", "inline", "anything", "opaque")
	assertDeepEqual(t, "ping required", tools[0].InputSchema["required"], []any{"host"})
	assertDeepEqual(t, "inline schema", tools[1].InputSchema, map[string]any{
		"type": "object", "additionalProperties": false,
		"properties": map[string]any{"n": map[string]any{"type": "integer"}},
	})
	assertDeepEqual(t, "any input schema", tools[2].InputSchema, map[string]any{"type": "object"})
	if tools[3].InputSchema != nil {
		t.Errorf("opaque schema = %v, want nil (runtime-built, untyped handler)", tools[3].InputSchema)
	}
}

func TestLoadGo_MCPGoOptions(t *testing.T) {
	_, tools := loadGoSources(t, map[string]string{"main.go": `package main

import (
	"encoding/json"

	m "github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

const rawSchema = ` + "`" + `{"type":"object","properties":{"id":{"type":"string"}},"required":["id"]}` + "`" + `

var units = []string{"c", "f"}

func register(s *server.MCPServer) {
	tool := m.NewTool("forecast",
		m.WithDescription("Forecast weather."),
		m.WithToolTitle("Forecast"),
		m.WithString("city", m.Required(), m.Description("City"), m.MinLength(2), m.Pattern("^[A-Z]")),
		m.WithString("units", m.Enum(units...), m.DefaultString("c")),
		m.WithInteger("days", m.Min(1), m.Max(14), m.DefaultNumber(3)),
		m.WithNumber("lat", m.Required()),
		m.WithBoolean("hourly", m.DefaultBool(false)),
		m.WithArray("fields", m.WithStringItems(m.Enum("temp", "wind")), m.MinItems(1), m.UniqueItems(true)),
		m.WithObject("opts", m.Properties(map[string]any{"verbose": map[string]any{"type": "boolean"}})),
		m.WithTitleAnnotation("Forecast annotation"),
		m.WithReadOnlyHintAnnotation(true),
		m.WithIdempotentHintAnnotation(true),
	)
	s.AddTool(tool, handler)

	s.AddTool(m.NewTool("bare", m.WithoutDefaultAnnotations(), m.WithDescription("No hints.")), handler)
	s.AddTool(m.NewToolWithRawSchema("lookup", "Look up by id.", json.RawMessage(rawSchema)), handler)
	s.AddTool(m.NewTool("raw_opt", m.WithRawInputSchema(json.RawMessage(rawSchema))), handler)
	s.AddTool(m.NewTool("custom", m.WithString("x", myOption())), handler)
	s.AddTool(m.NewTool("set_annotations",
		m.WithToolAnnotation(m.ToolAnnotation{ReadOnlyHint: m.ToBoolPtr(true), Title: "Set"})), handler)
}
`})
	assertToolNames(t, tools, "forecast", "bare", "lookup", "raw_opt", "custom", "set_annotations")

	f := tools[0]
	assertDeepEqual(t, "forecast title", f.Title, "Forecast")
	assertDeepEqual(t, "forecast description", f.Description, "Forecast weather.")
	assertDeepEqual(t, "forecast schema", f.InputSchema, map[string]any{
		"type": "object",
		"properties": map[string]any{
			"city":   map[string]any{"type": "string", "description": "City", "minLength": 2.0, "pattern": "^[A-Z]"},
			"units":  map[string]any{"type": "string", "enum": []any{"c", "f"}, "default": "c"},
			"days":   map[string]any{"type": "integer", "minimum": 1.0, "maximum": 14.0, "default": 3.0},
			"lat":    map[string]any{"type": "number"},
			"hourly": map[string]any{"type": "boolean", "default": false},
			"fields": map[string]any{
				"type":        "array",
				"items":       map[string]any{"type": "string", "enum": []any{"temp", "wind"}},
				"minItems":    1.0,
				"uniqueItems": true,
			},
			"opts": map[string]any{"type": "object", "properties": map[string]any{"verbose": map[string]any{"type": "boolean"}}},
		},
		"required": []any{"city", "lat"},
	})
	assertDeepEqual(t, "forecast annotations", f.Annotations, map[string]any{
		"title": "Forecast annotation", "readOnlyHint": true, "destructiveHint": true,
		"idempotentHint": true, "openWorldHint": true,
	})

	bare := tools[1]
	if bare.Annotations != nil {
		t.Errorf("bare annotations = %v, want nil after WithoutDefaultAnnotations", bare.Annotations)
	}
	assertDeepEqual(t, "bare schema", bare.InputSchema, map[string]any{"type": "object", "properties": map[string]any{}})

	wantRaw := map[string]any{
		"type": "object", "properties": map[string]any{"id": map[string]any{"type": "string"}}, "required": []any{"id"},
	}
	assertDeepEqual(t, "lookup description", tools[2].Description, "Look up by id.")
	assertDeepEqual(t, "lookup schema", tools[2].InputSchema, wantRaw)
	if tools[2].Annotations != nil {
		t.Errorf("NewToolWithRawSchema sets no default annotations, got %v", tools[2].Annotations)
	}
	assertDeepEqual(t, "raw_opt schema", tools[3].InputSchema, wantRaw)

	if tools[4].InputSchema != nil {
		t.Errorf("custom: unknown property option should drop the schema, got %v", tools[4].InputSchema)
	}
	assertDeepEqual(t, "set_annotations", tools[5].Annotations, map[string]any{"readOnlyHint": true, "title": "Set"})
	assertDeepEqual(t, "set_annotations title fallback", tools[5].Title, "Set")
}

func TestLoadGo_HandRolledLiterals(t *testing.T) {
	_, tools := loadGoSources(t, map[string]string{"main.go": `package main

import "encoding/json"

type Tool struct {
	Name        string
	Description string
	InputSchema json.RawMessage
}

type Schema map[string]any

type Thing struct{ Tool Tool }

var catalog = []Tool{
	{Name: "list_files", Description: "List files.", InputSchema: json.RawMessage(` + "`" + `{"type":"object","properties":{"dir":{"type":"string"}}}` + "`" + `)},
}

func tools(name string) any {
	_ = Tool{}                      // no name: not a tool
	_ = Tool{Name: name}            // name unknown statically: skipped
	_ = map[string]any{"name": "x"} // no inputSchema: not a tool
	_ = Thing{}
	return map[string]any{
		"tools": []any{
			map[string]any{
				"name":        "add",
				"description": "Add two numbers.",
				"inputSchema": Schema{
					"type":     "object",
					"properties": map[string]any{"a": map[string]any{"type": "number"}, "b": map[string]any{"type": "number"}},
					"required": []string{"a", "b"},
				},
				"annotations": map[string]any{"readOnlyHint": true, "title": "Adder"},
			},
		},
	}
}
`})
	assertToolNames(t, tools, "list_files", "add")
	assertDeepEqual(t, "list_files schema", tools[0].InputSchema, map[string]any{
		"type": "object", "properties": map[string]any{"dir": map[string]any{"type": "string"}},
	})
	add := tools[1]
	assertDeepEqual(t, "add description", add.Description, "Add two numbers.")
	assertDeepEqual(t, "add required", add.InputSchema["required"], []any{"a", "b"})
	assertDeepEqual(t, "add title", add.Title, "Adder")
}

func TestLoadGo_SDKLiteralNotReportedTwice(t *testing.T) {
	_, tools := loadGoSources(t, map[string]string{"main.go": `package main

import "github.com/modelcontextprotocol/go-sdk/mcp"

func register(s *mcp.Server) {
	tools := []*mcp.Tool{{Name: "a", Description: "A."}, {Name: "b", Description: "B."}}
	for _, t := range tools {
		s.AddTool(t, nil)
	}
	s.AddTool(&mcp.Tool{Name: "c", Description: "C."}, nil)
}
`})
	assertToolNames(t, tools, "a", "b", "c")
}

func TestLoadGo_SkipsUnparseableTestAndVendorFiles(t *testing.T) {
	_, tools := loadGoSources(t, map[string]string{
		"main.go":   "package main\n\nimport \"github.com/mark3labs/mcp-go/mcp\"\n\nvar t = mcp.NewTool(\"ok\", mcp.WithDescription(\"Fine.\"))\n",
		"broken.go": "package main\n\nimport \"github.com/mark3labs/mcp-go/mcp\"\n\nvar t = mcp.NewTool(\"broken\",\n",
		"main_test.go": "package main\n\nimport \"github.com/mark3labs/mcp-go/mcp\"\n\n" +
			"var tt = mcp.NewTool(\"fixture\")\n",
		"vendor/x/x.go": "package x\n\nimport \"github.com/mark3labs/mcp-go/mcp\"\n\nvar v = mcp.NewTool(\"vendored\")\n",
	})
	assertToolNames(t, tools, "ok")
}

func TestLoadGo_SingleFileUsesSiblingDeclarations(t *testing.T) {
	dir := t.TempDir()
	main := filepath.Join(dir, "main.go")
	files := map[string]string{
		main: `package main

import "github.com/modelcontextprotocol/go-sdk/mcp"

func register(s *mcp.Server) {
	mcp.AddTool(s, &mcp.Tool{Name: "greet", Description: "Greet someone."}, greet)
}
`,
		filepath.Join(dir, "types.go"): "package main\n\nimport \"github.com/modelcontextprotocol/go-sdk/mcp\"\n\n" +
			"type greetIn struct {\n\tWho string `json:\"who\"`\n}\n\n" +
			"func greet(ctx context.Context, req *mcp.CallToolRequest, in greetIn) (*mcp.CallToolResult, any, error) {\n\treturn nil, nil, nil\n}\n\n" +
			"var other = &mcp.Tool{Name: \"sibling_only\", Description: \"Not in the scanned file.\"}\n",
	}
	for path, src := range files {
		if err := os.WriteFile(path, []byte(src), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	set, err := LoadGo(main)
	if err != nil {
		t.Fatalf("LoadGo: %v", err)
	}
	assertToolNames(t, set.Tools, "greet")
	assertDeepEqual(t, "source", []any{set.Source, set.Tools[0].SourceFile, set.Tools[0].SourceLine}, []any{main, main, 6})
	assertDeepEqual(t, "required", set.Tools[0].InputSchema["required"], []any{"who"})
}

func TestLoadGo_MissingPath(t *testing.T) {
	if _, err := LoadGo(filepath.Join(t.TempDir(), "nope")); err == nil {
		t.Fatal("expected an error for a missing path")
	}
}
