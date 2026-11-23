package scanner

import (
	"regexp"
	"sort"

	"github.com/kaeawc/mcpcheck/internal/mcpmodel"
)

// Object-literal tool detection. Low-level SDK servers and hand-rolled
// servers advertise tools as literal objects — a JS object in a
// ListToolsRequestSchema handler, a Python dict in a TOOLS list, a
// json!({...}) in Rust. Any object literal with a string "name" and an
// "inputSchema" key is treated as a tool definition.

// schemaKeyRe finds inputSchema keys in object/dict literals.
var schemaKeyRe = regexp.MustCompile(`["']?\b(?:inputSchema|input_schema)\b["']?\s*:`)

// schemaFunc converts a parsed inputSchema/outputSchema value to JSON
// Schema, reporting false when it can't be evaluated statically.
type schemaFunc func(v any) (map[string]any, bool)

// literalSchema accepts only plain literal JSON Schema objects.
func literalSchema(v any) (map[string]any, bool) {
	js, ok := toJSONValue(v)
	m, isObj := js.(map[string]any)
	return m, ok && isObj
}

// objectLiteralTools returns tools defined as object literals in src.
func objectLiteralTools(path, src string, l lang, convert schemaFunc) []mcpmodel.Tool {
	matches := schemaKeyRe.FindAllStringIndex(src, -1)
	if len(matches) == 0 {
		return nil
	}
	targets := make([]int, len(matches))
	for i, m := range matches {
		targets[i] = m[0]
	}
	seen := map[int]bool{}
	var tools []mcpmodel.Tool
	for _, open := range enclosingOpen(src, l, targets) {
		if open < 0 || seen[open] {
			continue
		}
		seen[open] = true
		obj, ok := newLitParser(src, open, l).value().(map[string]any)
		if !ok {
			continue
		}
		if t, ok := toolFromFields(obj, convert); ok {
			t.SourceFile, t.SourceLine = path, lineAt(src, open)
			tools = append(tools, t)
		}
	}
	return tools
}

// toolFromFields builds a tool from a parsed object literal or keyword
// argument map. It needs at least a literal string name.
func toolFromFields(fields map[string]any, convert schemaFunc) (mcpmodel.Tool, bool) {
	name, ok := asString(fields["name"])
	if !ok || name == "" {
		return mcpmodel.Tool{}, false
	}
	t := mcpmodel.Tool{Name: name}
	t.Description, _ = asString(fields["description"])
	t.Title, _ = asString(fields["title"])
	if v, ok := firstField(fields, "inputSchema", "input_schema"); ok {
		t.InputSchema, _ = convert(v)
	}
	if v, ok := firstField(fields, "outputSchema", "output_schema"); ok {
		t.OutputSchema, _ = convert(v)
	}
	if v, ok := fields["annotations"]; ok {
		t.Annotations = annotationsValue(v)
	}
	return t, true
}

func firstField(fields map[string]any, names ...string) (any, bool) {
	for _, n := range names {
		if v, ok := fields[n]; ok {
			return v, true
		}
	}
	return nil, false
}

// annotationNames maps the snake_case spellings used by Python (SDK v2
// ToolAnnotations(read_only_hint=...)) and Rust to protocol names.
var annotationNames = map[string]string{
	"read_only_hint":   "readOnlyHint",
	"destructive_hint": "destructiveHint",
	"idempotent_hint":  "idempotentHint",
	"open_world_hint":  "openWorldHint",
}

// annotationsValue converts an annotations literal — an object, or a
// ToolAnnotations(...) call with keyword arguments — to protocol form.
func annotationsValue(v any) map[string]any {
	var fields map[string]any
	switch t := v.(type) {
	case map[string]any:
		fields = t
	case *chainValue:
		if n := len(t.segments); n > 0 && t.segments[n-1].called {
			fields = t.segments[n-1].kwargs
		}
	}
	if fields == nil {
		return nil
	}
	out := map[string]any{}
	for k, raw := range fields {
		js, ok := toJSONValue(raw)
		if !ok {
			continue
		}
		if proto, renamed := annotationNames[k]; renamed {
			k = proto
		}
		out[k] = js
	}
	return out
}

func sortedMapKeys(m map[string]any) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
