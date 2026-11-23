package scanner

import (
	"encoding/json"
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
	want := []struct {
		name, desc string
		line       int
	}{
		{"fetch_user", "Fetch a user by id.", 2},
		{"send_message", "Post a message.", 3},
		{"long_decl", "A multi-line declaration.", 5},
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
}

func TestScanTypeScript_NoMatches(t *testing.T) {
	src := `
const x = 1;
function notATool() {
  return server.handle("foo", "bar");
}
const config = { inputSchema: "not a tool: no name" };
`
	if tools := scanTypeScript("inline.ts", src); len(tools) != 0 {
		t.Fatalf("expected no tools, got %+v", tools)
	}
}

func schemaJSON(m map[string]any) string {
	b, _ := json.Marshal(m)
	return string(b)
}

func TestScanTypeScript_RegisterToolWithZod(t *testing.T) {
	src := "import * as z from 'zod';\n" +
		"const Units = z.enum(['celsius', 'fahrenheit']);\n" +
		"const FetchArgs = z.object({ url: z.string().url().regex(/^https:\\/\\//), retries: z.number().int().min(0).default(2) });\n" +
		"server.registerTool('get_weather', {\n" +
		"  title: `Get Weather`,\n" +
		"  description: 'Get the current ' + 'weather.',\n" +
		"  inputSchema: { city: z.string().describe('City name'), units: Units.optional(), tags: z.array(z.string()).max(3).nullish() },\n" +
		"  outputSchema: { temp: z.number() },\n" +
		"  annotations: { readOnlyHint: true, openWorldHint: false },\n" +
		"}, async ({ city }) => ({ content: [] }));\n" +
		"server.registerTool('fetch_url', { description: 'Fetch.', inputSchema: FetchArgs }, cb);\n" +
		"server.registerTool('ping', { description: 'No args.' }, cb);\n"
	tools := scanTypeScript("server.ts", src)
	if len(tools) != 3 {
		t.Fatalf("got %d tools: %+v", len(tools), tools)
	}
	w := tools[0]
	if w.Title != "Get Weather" || w.Description != "Get the current weather." || w.SourceLine != 4 {
		t.Errorf("metadata: %+v", w)
	}
	wantIn := `{"properties":{"city":{"description":"City name","type":"string"},"tags":{"items":{"type":"string"},"maxItems":3,"type":"array"},"units":{"enum":["celsius","fahrenheit"],"type":"string"}},"required":["city"],"type":"object"}`
	if got := schemaJSON(w.InputSchema); got != wantIn {
		t.Errorf("inputSchema:\n got %s\nwant %s", got, wantIn)
	}
	if got := schemaJSON(w.OutputSchema); got != `{"properties":{"temp":{"type":"number"}},"required":["temp"],"type":"object"}` {
		t.Errorf("outputSchema: %s", got)
	}
	if w.Annotations["readOnlyHint"] != true || w.Annotations["openWorldHint"] != false {
		t.Errorf("annotations: %v", w.Annotations)
	}
	wantFetch := `{"properties":{"retries":{"default":2,"minimum":0,"type":"integer"},"url":{"format":"uri","pattern":"^https:\\/\\/","type":"string"}},"required":["url"],"type":"object"}`
	if got := schemaJSON(tools[1].InputSchema); got != wantFetch {
		t.Errorf("fetch_url schema:\n got %s\nwant %s", got, wantFetch)
	}
	if got := schemaJSON(tools[2].InputSchema); got != `{"properties":{},"type":"object"}` {
		t.Errorf("no-arg schema: %s", got)
	}
}

func TestScanTypeScript_LegacyToolArgumentForms(t *testing.T) {
	src := `
server.tool("a", async () => ({}));
server.tool("b", "Desc b.", async () => ({}));
server.tool("c", { q: z.string() }, async () => ({}));
server.tool("d", "Desc d.", { q: z.string().optional() }, { destructiveHint: true }, async () => ({}));
`
	tools := scanTypeScript("s.ts", src)
	if len(tools) != 4 {
		t.Fatalf("got %d tools", len(tools))
	}
	if tools[1].Description != "Desc b." || tools[3].Description != "Desc d." {
		t.Errorf("descriptions: %q %q", tools[1].Description, tools[3].Description)
	}
	if got := schemaJSON(tools[2].InputSchema); got != `{"properties":{"q":{"type":"string"}},"required":["q"],"type":"object"}` {
		t.Errorf("c schema: %s", got)
	}
	if _, req := tools[3].InputSchema["required"]; req || tools[3].Annotations["destructiveHint"] != true {
		t.Errorf("d: schema %v annotations %v", tools[3].InputSchema, tools[3].Annotations)
	}
}

func TestScanTypeScript_LowLevelObjectLiterals(t *testing.T) {
	src := `
const TOOLS = [
  {
    name: "get_weather",
    description: "Get weather.",
    inputSchema: { type: "object", properties: { city: { type: "string" } }, required: ["city"] },
    annotations: { readOnlyHint: true },
  },
  { name: "dynamic", description: "Built elsewhere.", inputSchema: buildSchema() },
];
server.setRequestHandler(ListToolsRequestSchema, async () => ({ tools: TOOLS }));
`
	tools := scanTypeScript("s.ts", src)
	if len(tools) != 2 {
		t.Fatalf("got %d tools: %+v", len(tools), tools)
	}
	if tools[0].Name != "get_weather" || tools[0].SourceLine != 3 ||
		schemaJSON(tools[0].InputSchema) != `{"properties":{"city":{"type":"string"}},"required":["city"],"type":"object"}` {
		t.Errorf("get_weather: %+v", tools[0])
	}
	if tools[1].Name != "dynamic" || tools[1].InputSchema != nil {
		t.Errorf("dynamic schema should be unknown: %+v", tools[1])
	}
}
