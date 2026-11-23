package scanner

import (
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"

	"github.com/kaeawc/mcpcheck/internal/mcpmodel"
)

// scanRustSources runs the Rust intake over in-memory files, visiting
// them in path order as a directory walk would.
func scanRustSources(files map[string]string) []mcpmodel.Tool {
	paths := make([]string, 0, len(files))
	for p := range files {
		paths = append(paths, p)
	}
	sort.Strings(paths)
	sc := newRustScan()
	for _, p := range paths {
		sc.addFile(p, files[p])
	}
	return sc.tools()
}

func scanRust(src string) []mcpmodel.Tool {
	return scanRustSources(map[string]string{"main.rs": src})
}

func rustToolByName(t *testing.T, tools []mcpmodel.Tool, name string) mcpmodel.Tool {
	t.Helper()
	for _, tool := range tools {
		if tool.Name == name {
			return tool
		}
	}
	names := make([]string, 0, len(tools))
	for _, tool := range tools {
		names = append(names, tool.Name)
	}
	t.Fatalf("tool %q not found in %v", name, names)
	return mcpmodel.Tool{}
}

func rustProp(t *testing.T, tool mcpmodel.Tool, name string) map[string]any {
	t.Helper()
	props, _ := tool.InputSchema["properties"].(map[string]any)
	p, ok := props[name].(map[string]any)
	if !ok {
		t.Fatalf("%s: property %q missing in %v", tool.Name, name, tool.InputSchema)
	}
	return p
}

func assertRequired(t *testing.T, tool mcpmodel.Tool, want ...string) {
	t.Helper()
	_, got := schemaShapeRust(tool.InputSchema)
	sort.Strings(want)
	if len(want) == 0 {
		want = nil
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("%s: required %v, want %v", tool.Name, got, want)
	}
}

func schemaShapeRust(schema map[string]any) (props, required []string) {
	p, _ := schema["properties"].(map[string]any)
	for name := range p {
		props = append(props, name)
	}
	r, _ := schema["required"].([]any)
	for _, name := range r {
		required = append(required, name.(string))
	}
	sort.Strings(props)
	sort.Strings(required)
	return props, required
}

const rmcpServer = `use rmcp::{handler::server::wrapper::Parameters, tool, tool_router};

#[derive(Debug, Deserialize, schemars::JsonSchema)]
#[serde(rename_all = "lowercase")]
pub enum Units {
    Celsius,
    Fahrenheit,
}

#[derive(Debug, Deserialize, schemars::JsonSchema)]
pub struct WeatherArgs {
    #[schemars(description = "City name")]
    pub city: String,
    /// Units doc
    pub units: Option<Units>,
}

#[tool_router]
impl Weather {
    #[tool(description = "Get the current weather for a city.", annotations(read_only_hint = true, title = "Weather"))]
    async fn get_weather(&self, Parameters(WeatherArgs { city, units }): Parameters<WeatherArgs>) -> Result<CallToolResult, McpError> {
        todo!()
    }

    /// Fetch a URL.
    ///
    /// Returns the body.
    #[tool(name = "fetch_url", title = "Fetch")]
    async fn fetch(&self, Parameters(args): Parameters<FetchArgs>) -> Result<CallToolResult, McpError> {
        todo!()
    }

    #[tool]
    fn ping(&self) -> String {
        "pong".into()
    }

    fn helper(&self, x: u32) -> u32 { x }
}
`

func TestRustRmcpToolAttributes(t *testing.T) {
	tools := scanRustSources(map[string]string{
		"src/main.rs": rmcpServer,
		"src/args.rs": `
#[derive(Deserialize, JsonSchema)]
pub struct FetchArgs {
    /// The URL to fetch.
    pub url: String,
    #[serde(default)]
    pub timeout_ms: u64,
    pub headers: HashMap<String, String>,
    pub tags: Vec<String>,
}`,
	})
	if len(tools) != 3 {
		t.Fatalf("got %d tools, want 3: %+v", len(tools), tools)
	}

	w := rustToolByName(t, tools, "get_weather")
	if w.Description != "Get the current weather for a city." {
		t.Errorf("description %q", w.Description)
	}
	if !reflect.DeepEqual(w.Annotations, map[string]any{"readOnlyHint": true, "title": "Weather"}) {
		t.Errorf("annotations %v", w.Annotations)
	}
	if w.SourceFile != "src/main.rs" || w.SourceLine != 20 {
		t.Errorf("location %s:%d, want src/main.rs:20", w.SourceFile, w.SourceLine)
	}
	if w.Raw != nil {
		t.Errorf("Raw must stay nil for source intakes")
	}
	city := rustProp(t, w, "city")
	if city["type"] != "string" || city["description"] != "City name" {
		t.Errorf("city %v", city)
	}
	units := rustProp(t, w, "units")
	if !reflect.DeepEqual(units, map[string]any{"type": "string", "enum": []any{"celsius", "fahrenheit"}, "description": "Units doc"}) {
		t.Errorf("units %v", units)
	}
	assertRequired(t, w, "city")

	f := rustToolByName(t, tools, "fetch_url")
	if f.Description != "Fetch a URL.\nReturns the body." || f.Title != "Fetch" {
		t.Errorf("fetch_url description %q title %q", f.Description, f.Title)
	}
	if f.Annotations != nil {
		t.Errorf("fetch_url annotations %v, want nil", f.Annotations)
	}
	if got := rustProp(t, f, "url")["description"]; got != "The URL to fetch." {
		t.Errorf("url description %v", got)
	}
	if got := rustProp(t, f, "timeout_ms")["type"]; got != "integer" {
		t.Errorf("timeout_ms type %v", got)
	}
	if got := rustProp(t, f, "headers"); !reflect.DeepEqual(got, map[string]any{"type": "object", "additionalProperties": map[string]any{"type": "string"}}) {
		t.Errorf("headers %v", got)
	}
	if got := rustProp(t, f, "tags"); !reflect.DeepEqual(got, map[string]any{"type": "array", "items": map[string]any{"type": "string"}}) {
		t.Errorf("tags %v", got)
	}
	assertRequired(t, f, "url", "headers", "tags")

	p := rustToolByName(t, tools, "ping")
	if !reflect.DeepEqual(p.InputSchema, map[string]any{"type": "object", "properties": map[string]any{}}) {
		t.Errorf("no-arg tool schema %v", p.InputSchema)
	}
	if p.Description != "" {
		t.Errorf("ping description %q", p.Description)
	}
}

func TestRustDocDescriptionResetByAttribute(t *testing.T) {
	// rmcp-macros folds the fn's attributes in order and a non-doc
	// attribute discards the doc lines collected so far.
	tools := scanRust(`
impl S {
    /// Lost.
    #[allow(dead_code)]
    /// Kept.
    #[tool]
    fn a(&self) {}

    #[tool]
    /// After the attribute.
    #[doc = "Doc attribute."]
    fn b(&self) {}
}`)
	if got := rustToolByName(t, tools, "a").Description; got != "Kept." {
		t.Errorf("a description %q", got)
	}
	if got := rustToolByName(t, tools, "b").Description; got != "After the attribute.\nDoc attribute." {
		t.Errorf("b description %q", got)
	}
}

func TestRustSerdeRenames(t *testing.T) {
	tools := scanRust(`
#[derive(Deserialize, JsonSchema)]
#[serde(rename_all = "camelCase", deny_unknown_fields)]
struct Args {
    file_path: String,
    #[serde(rename = "MAX")]
    max_count: Option<i32>,
    #[serde(skip)]
    cache: Vec<u8>,
    #[serde(flatten)]
    paging: Paging,
    mode: Mode,
    ratio: f64,
    r#type: Kind,
    raw: serde_json::Value,
}

#[derive(Deserialize, JsonSchema)]
struct Paging { page_size: u32, #[serde(default)] cursor: String }

#[derive(Deserialize, JsonSchema)]
#[serde(rename_all = "SCREAMING_SNAKE_CASE")]
enum Mode { FastPath, #[serde(rename = "slow")] SlowPath, #[serde(skip)] Hidden }

enum Kind { A(String), B }

#[tool_router]
impl S {
    #[tool(description = "d")]
    async fn run(&self, params: Parameters<Args>) -> String { String::new() }
}`)
	run := rustToolByName(t, tools, "run")
	props, _ := schemaShapeRust(run.InputSchema)
	want := []string{"MAX", "cursor", "filePath", "mode", "page_size", "ratio", "raw", "type"}
	if !reflect.DeepEqual(props, want) {
		t.Errorf("properties %v, want %v", props, want)
	}
	assertRequired(t, run, "filePath", "page_size", "mode", "ratio", "type", "raw")
	if got := rustProp(t, run, "mode")["enum"]; !reflect.DeepEqual(got, []any{"FAST_PATH", "slow"}) {
		t.Errorf("mode enum %v", got)
	}
	if got := rustProp(t, run, "type"); len(got) != 0 {
		t.Errorf("data-carrying enum should be unconstrained, got %v", got)
	}
	if got := rustProp(t, run, "raw"); len(got) != 0 {
		t.Errorf("serde_json::Value should be unconstrained, got %v", got)
	}
	if run.InputSchema["additionalProperties"] != false {
		t.Errorf("deny_unknown_fields should set additionalProperties false: %v", run.InputSchema)
	}
}

func TestRustUnknownParamsTypeHasNilSchema(t *testing.T) {
	tools := scanRust(`
impl S {
    #[tool(description = "x")]
    async fn ext(&self, Parameters(p): Parameters<other_crate::Args>) {}
}`)
	if got := rustToolByName(t, tools, "ext").InputSchema; got != nil {
		t.Errorf("schema for a type outside the scanned sources should be nil, got %v", got)
	}
}

func TestRustNestedStructAndOutputSchema(t *testing.T) {
	tools := scanRust(`
struct Inner { id: u64 }
struct Args { inner: Inner, list: Vec<Inner>, fixed: [u8; 4] }
struct Out { ok: bool }
impl S {
    #[tool(description = "nested")]
    async fn nested(&self, Parameters(a): Parameters<Args>) -> Result<Json<Out>, String> { todo!() }
}`)
	n := rustToolByName(t, tools, "nested")
	wantInner := map[string]any{"type": "object", "properties": map[string]any{"id": map[string]any{"type": "integer"}}, "required": []any{"id"}}
	if got := rustProp(t, n, "inner"); !reflect.DeepEqual(got, wantInner) {
		t.Errorf("inner %v", got)
	}
	if got := rustProp(t, n, "list")["items"]; !reflect.DeepEqual(got, wantInner) {
		t.Errorf("list items %v", got)
	}
	if got := rustProp(t, n, "fixed"); !reflect.DeepEqual(got, map[string]any{"type": "array", "items": map[string]any{"type": "integer"}}) {
		t.Errorf("fixed %v", got)
	}
	wantOut := map[string]any{"type": "object", "properties": map[string]any{"ok": map[string]any{"type": "boolean"}}, "required": []any{"ok"}}
	if !reflect.DeepEqual(n.OutputSchema, wantOut) {
		t.Errorf("output schema %v", n.OutputSchema)
	}
}

func TestRustLegacyToolBox(t *testing.T) {
	tools := scanRust(`
#[tool(tool_box)]
impl Calculator {
    #[tool(description = "Add two numbers")]
    fn sum(
        &self,
        #[tool(param)]
        #[schemars(description = "the left hand side number")]
        a: i32,
        #[tool(param)] b: Option<i32>,
    ) -> String { todo!() }

    #[tool(description = "Subtract")]
    fn sub(&self, #[tool(aggr)] req: SubRequest) -> String { todo!() }
}
struct SubRequest { a: i32, b: i32 }`)
	if len(tools) != 2 {
		t.Fatalf("got %d tools, want 2", len(tools))
	}
	sum := rustToolByName(t, tools, "sum")
	if got := rustProp(t, sum, "a"); got["description"] != "the left hand side number" || got["type"] != "integer" {
		t.Errorf("a %v", got)
	}
	assertRequired(t, sum, "a")
	sub := rustToolByName(t, tools, "sub")
	assertRequired(t, sub, "a", "b")
}

func TestRustAttrInputSchemaAndPaths(t *testing.T) {
	tools := scanRust(`
impl S {
    #[rmcp::tool(name = "raw", description = r#"Says "hi"."#, input_schema = rmcp::object!({"type": "object", "properties": {"q": {"type": "string"}}}))]
    pub(crate) async fn raw_tool<'a>(&'a self, ctx: RequestContext<'a>) {}
}`)
	raw := rustToolByName(t, tools, "raw")
	if raw.Description != `Says "hi".` {
		t.Errorf("raw-string description %q", raw.Description)
	}
	if got := rustProp(t, raw, "q")["type"]; got != "string" {
		t.Errorf("q type %v (schema %v)", got, raw.InputSchema)
	}
}

func TestRustToolNew(t *testing.T) {
	tools := scanRust(`
use rmcp::model::{Tool, ToolAnnotations};
use serde_json::json;

const LOOKUP_SCHEMA: &str = r#"{"type": "object", "properties": {"id": {"type": "integer"}}, "required": ["id"]}"#;

fn tools() -> Vec<Tool> {
    let schema = serde_json::json!({"type": "object", "properties": {"q": {"type": "string"}}});
    vec![
        Tool::new("search", "Search the index.", Arc::new(schema.as_object().unwrap().clone()))
            .with_title("Search")
            .with_annotations(ToolAnnotations::new().read_only(true).open_world(false)),
        rmcp::model::Tool::new(
            "lookup",
            "Look up \
             by id.",
            Arc::new(serde_json::from_str(LOOKUP_SCHEMA).unwrap()),
        ),
        Tool::new(name_var, "dynamic", schema()),
    ]
}`)
	if len(tools) != 2 {
		t.Fatalf("got %d tools, want 2: %+v", len(tools), tools)
	}
	s := rustToolByName(t, tools, "search")
	if s.Description != "Search the index." || s.Title != "Search" || s.SourceLine != 10 {
		t.Errorf("search %q %q line %d", s.Description, s.Title, s.SourceLine)
	}
	if !reflect.DeepEqual(s.Annotations, map[string]any{"readOnlyHint": true, "openWorldHint": false}) {
		t.Errorf("annotations %v", s.Annotations)
	}
	if got := rustProp(t, s, "q")["type"]; got != "string" {
		t.Errorf("q %v", got)
	}
	l := rustToolByName(t, tools, "lookup")
	if l.Description != "Look up by id." {
		t.Errorf("lookup description %q", l.Description)
	}
	assertRequired(t, l, "id")
}

func TestRustToolStructLiteral(t *testing.T) {
	tools := scanRust(`
pub struct ToolConfig { name: String }
fn cfg() -> ToolConfig { ToolConfig { name: "not-a-tool".into() } }

fn list() -> Vec<Tool> {
    vec![Tool {
        name: Cow::Borrowed("echo"),
        title: Some("Echo".to_string()),
        description: Some("Echo the input back.".into()),
        input_schema: Arc::new(json!({"type": "object", "properties": {"text": {"type": "string"}}, "required": ["text"]}).as_object().unwrap().clone()),
        output_schema: None,
        annotations: Some(ToolAnnotations { read_only_hint: Some(true), destructive_hint: Some(false), ..Default::default() }),
        ..Default::default()
    }]
}

fn matcher(t: &Tool) -> bool {
    match t { Tool { name, .. } => name.is_empty() }
}`)
	if len(tools) != 1 {
		t.Fatalf("got %d tools, want 1: %+v", len(tools), tools)
	}
	e := tools[0]
	if e.Name != "echo" || e.Title != "Echo" || e.Description != "Echo the input back." {
		t.Errorf("echo %+v", e)
	}
	if !reflect.DeepEqual(e.Annotations, map[string]any{"readOnlyHint": true, "destructiveHint": false}) {
		t.Errorf("annotations %v", e.Annotations)
	}
	assertRequired(t, e, "text")
}

func TestRustJSONLiteralTools(t *testing.T) {
	tools := scanRust(`
// {"name": "commented", "inputSchema": {}}
fn list_tools() -> Value {
    json!({
        "tools": [
            {
                "name": "add",
                "description": "Add numbers. It's simple.",
                "inputSchema": {"type": "object", "properties": {"a": {"type": "number"}}, "required": ["a"]},
                "annotations": {"idempotentHint": true}
            },
            {"name": "noop", "inputSchema": schema_var}
        ]
    })
}

fn unrelated(map: &mut Map<String, Value>) {
    map.insert("inputSchema".to_string(), Value::Null);
    let s = "inputSchema: nope";
    let o = json!({"inputSchema": {}, "kind": 1});
}`)
	if len(tools) != 2 {
		t.Fatalf("got %d tools, want 2: %+v", len(tools), tools)
	}
	add := rustToolByName(t, tools, "add")
	if add.Description != "Add numbers. It's simple." || add.SourceLine != 6 {
		t.Errorf("add %q line %d", add.Description, add.SourceLine)
	}
	if !reflect.DeepEqual(add.Annotations, map[string]any{"idempotentHint": true}) {
		t.Errorf("annotations %v", add.Annotations)
	}
	assertRequired(t, add, "a")
	if noop := rustToolByName(t, tools, "noop"); noop.InputSchema != nil {
		t.Errorf("non-literal schema should be nil, got %v", noop.InputSchema)
	}
}

func TestRustNoFalsePositives(t *testing.T) {
	tools := scanRust(`
/// #[tool(description = "in a doc comment")]
fn documented() {}

// #[tool] fn commented() {}
fn plain(&self, Parameters(p): Parameters<Args>) {}

const SNIPPET: &str = "#[tool(description = \"in a string\")] fn quoted() {}";

#[derive(Debug)]
pub struct ToolConfig {
    pub name: String,
}

impl Tool {
    pub fn describe(&self) -> &str { "x" }
}

fn lifetimes<'a>(x: &'a str, c: char) -> &'a str {
    let q = '"';
    let brace = '{';
    x
}

#[cfg(test)]
mod tests {
    #[test]
    fn t() {
        let _ = Tool::new("test_only", "fixture", json!({}));
    }
}`)
	if len(tools) != 0 {
		t.Fatalf("expected no tools, got %+v", tools)
	}
}

func TestRustDeduplicatesAcrossPatterns(t *testing.T) {
	tools := scanRust(`
fn list() -> Vec<Tool> {
    vec![Tool {
        name: "one".into(),
        description: Some("first".into()),
        input_schema: Arc::new(json!({"type": "object", "properties": {}, "inputSchema": {}, "name": "inner"}).as_object().unwrap().clone()),
        ..Default::default()
    }]
}`)
	if len(tools) != 1 || tools[0].Name != "one" {
		t.Fatalf("want only tool one, got %+v", tools)
	}
}

func TestRustNormalizeKeepsOffsets(t *testing.T) {
	src := "let a = r#\"x\n\"y\"\n\"#; let b = \"multi\nline \\u{e9}\"; let c = 'q'; fn f<'a>() {}\n"
	rs := newRustSource("x.rs", src)
	if len(rs.norm) != len(src) || len(rs.code) != len(src) {
		t.Fatalf("lengths changed: %d %d %d", len(src), len(rs.norm), len(rs.code))
	}
	if strings.Count(rs.norm, "\n") != strings.Count(src, "\n") || strings.Count(rs.code, "\n") != strings.Count(src, "\n") {
		t.Fatalf("line count changed")
	}
	if got := rs.parseValue(strings.Index(src, "r#")); got != "x\n\"y\"\n" {
		t.Errorf("raw string %q", got)
	}
	if got := rs.parseValue(strings.Index(src, `"multi`)); got != "multi\nline é" {
		t.Errorf("multi-line string %q", got)
	}
	if strings.Contains(rs.norm, "'a") {
		t.Errorf("lifetime not blanked: %q", rs.norm)
	}
}

func TestRustRenameRules(t *testing.T) {
	cases := []struct{ in, rule, field, variant string }{
		{"user_id", "camelCase", "userId", ""},
		{"user_id", "PascalCase", "UserId", ""},
		{"user_id", "kebab-case", "user-id", ""},
		{"user_id", "SCREAMING-KEBAB-CASE", "USER-ID", ""},
		{"UserId", "snake_case", "", "user_id"},
		{"UserId", "camelCase", "", "userId"},
		{"UserId", "kebab-case", "", "user-id"},
		{"UserId", "SCREAMING_SNAKE_CASE", "", "USER_ID"},
		{"UserId", "lowercase", "", "userid"},
	}
	for _, c := range cases {
		if c.field != "" {
			if got := rustRenameField(c.in, c.rule); got != c.field {
				t.Errorf("field %s %s = %s, want %s", c.in, c.rule, got, c.field)
			}
		}
		if c.variant != "" {
			if got := rustRenameVariant(c.in, c.rule); got != c.variant {
				t.Errorf("variant %s %s = %s, want %s", c.in, c.rule, got, c.variant)
			}
		}
	}
}

func TestLoadRustFileAndErrors(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "lib.rs")
	if err := os.WriteFile(path, []byte(rmcpServer), 0o600); err != nil {
		t.Fatal(err)
	}
	set, err := LoadRust(path)
	if err != nil {
		t.Fatal(err)
	}
	if set.Source != path || len(set.Tools) != 3 || set.Tools[0].SourceFile != path {
		t.Errorf("LoadRust(file) = %+v", set)
	}
	if _, err := LoadRust(filepath.Join(dir, "missing.rs")); err == nil {
		t.Error("expected an error for a missing path")
	}
}

func TestLoadRustTestdata(t *testing.T) {
	set, err := LoadRust(filepath.Join("testdata", "rust"))
	if err != nil {
		t.Fatal(err)
	}
	assertCanonicalTools(t, set.Tools)
	for _, tool := range set.Tools {
		if !strings.HasPrefix(filepath.ToSlash(tool.SourceFile), "testdata/rust/src/") {
			t.Errorf("%s: unexpected source file %s", tool.Name, tool.SourceFile)
		}
	}
}

func TestLoadRustCorpusServer(t *testing.T) {
	dir := corpusDir(t, "rust-rmcp")
	set, err := LoadRust(dir)
	if err != nil {
		t.Fatal(err)
	}
	if set.Source != dir {
		t.Errorf("Source = %q, want %q", set.Source, dir)
	}
	assertCanonicalTools(t, set.Tools)
	w := rustToolByName(t, set.Tools, "get_weather")
	if got := rustProp(t, w, "city")["description"]; got != "City name" {
		t.Errorf("city description %v", got)
	}
	if got := rustProp(t, w, "units")["enum"]; !reflect.DeepEqual(got, []any{"celsius", "fahrenheit"}) {
		t.Errorf("units enum %v", got)
	}
	want := map[string]map[string]any{
		"get_weather": {"readOnlyHint": true},
		"delete_file": {"destructiveHint": true},
		"fetch_url":   {"openWorldHint": true},
	}
	for _, tool := range set.Tools {
		if !reflect.DeepEqual(tool.Annotations, want[tool.Name]) {
			t.Errorf("%s annotations %v, want %v", tool.Name, tool.Annotations, want[tool.Name])
		}
	}
	d := rustToolByName(t, set.Tools, "delete_file")
	if got := rustProp(t, d, "confirm")["type"]; got != "boolean" {
		t.Errorf("confirm type %v", got)
	}
}

func TestRustProbeTmp(t *testing.T) {
	dir := os.Getenv("RUST_PROBE")
	if dir == "" {
		t.Skip()
	}
	set, err := LoadRust(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, tool := range set.Tools {
		t.Logf("%s:%d %s %q schema=%v ann=%v", tool.SourceFile, tool.SourceLine, tool.Name, tool.Description, tool.InputSchema, tool.Annotations)
	}
}
