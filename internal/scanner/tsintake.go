// TypeScript / JavaScript intake for the official SDK
// (@modelcontextprotocol/sdk v1 and @modelcontextprotocol/server v2) and
// hand-rolled servers. Calls are located with a regex and their arguments
// read with the shared literal parser, so multi-line calls, comments,
// template literals, and trailing commas are all fine.
//
// Recognized patterns:
//
//	server.registerTool("name", { title, description, inputSchema, outputSchema, annotations }, cb)
//	server.tool("name", "description"?, { zodShape }?, { annotations }?, cb)
//	{ name: "x", description: "...", inputSchema: { type: "object", ... } }
//	    (object literals, e.g. in a ListToolsRequestSchema handler)
//
// inputSchema may be a zod raw shape, a z.object(...), a literal JSON
// Schema, or a const declared in the same file; zod is converted to JSON
// Schema (see zod.go).
//
// Limitations: names and descriptions must be literals or file-local
// consts; schemas built by helper functions or imported from other files
// are left nil; calls inside comments are not distinguished from code.
package scanner

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/kaeawc/mcpcheck/internal/mcpmodel"
)

// tsRegisterRe matches the opening of .registerTool( and .tool( calls.
var tsRegisterRe = regexp.MustCompile(`\.(registerTool|tool)\s*\(`)

// tsConstRe matches `const name = ` / `const name: Type = ` declarations.
var tsConstRe = regexp.MustCompile(`\b(?:const|let|var)\s+([A-Za-z_$][\w$]*)\s*(?::[^=;\n]+)?=\s*`)

// emptyObjectSchema is what the SDK advertises for tools declared
// without an input schema.
func emptyObjectSchema() map[string]any {
	return map[string]any{"type": "object", "properties": map[string]any{}}
}

func isTypeScriptSourceFile(path string) bool {
	if strings.HasSuffix(path, ".d.ts") {
		return false
	}
	switch filepath.Ext(path) {
	case ".ts", ".tsx", ".mts", ".cts", ".js", ".mjs", ".cjs", ".jsx":
		return true
	}
	return false
}

// LoadTypeScript scans a TypeScript/JavaScript file or a directory tree
// for tool registrations.
func LoadTypeScript(path string) (*mcpmodel.ToolSet, error) {
	set := &mcpmodel.ToolSet{Source: path}
	err := walkSources(path, isTypeScriptSourceFile, func(p string) error {
		raw, err := os.ReadFile(p) // #nosec G304 -- scanning user-named sources is the point
		if err != nil {
			return fmt.Errorf("read %s: %w", p, err)
		}
		set.Tools = append(set.Tools, scanTypeScript(p, string(raw))...)
		return nil
	})
	return set, err
}

// scanTypeScript extracts tools from one source file.
func scanTypeScript(path, src string) []mcpmodel.Tool {
	zc := &zodConverter{consts: tsConsts(src)}
	var tools []mcpmodel.Tool
	for _, m := range tsRegisterRe.FindAllStringSubmatchIndex(src, -1) {
		args, _ := newLitParser(src, m[1]-1, langJS).callArgs()
		var t mcpmodel.Tool
		var ok bool
		if src[m[2]:m[3]] == "registerTool" {
			t, ok = zc.registerTool(args)
		} else {
			t, ok = zc.legacyTool(args)
		}
		if ok {
			t.SourceFile, t.SourceLine = path, lineAt(src, m[0]+1)
			tools = append(tools, t)
		}
	}
	return append(tools, objectLiteralTools(path, src, langJS, zc.shapeSchema)...)
}

// tsConsts parses every const/let/var initializer in src so schemas and
// strings declared once and referenced by name can be resolved.
func tsConsts(src string) map[string]any {
	consts := map[string]any{}
	for _, m := range tsConstRe.FindAllStringSubmatchIndex(src, -1) {
		consts[src[m[2]:m[3]]] = newLitParser(src, m[1], langJS).value()
	}
	return consts
}

func (zc *zodConverter) str(v any) (string, bool) {
	return asString(zc.resolve(v))
}

// registerTool handles registerTool(name, config, cb).
func (zc *zodConverter) registerTool(args []any) (mcpmodel.Tool, bool) {
	if len(args) < 2 {
		return mcpmodel.Tool{}, false
	}
	name, ok := zc.str(args[0])
	config, isObj := zc.resolve(args[1]).(map[string]any)
	if !ok || !isObj {
		return mcpmodel.Tool{}, false
	}
	t := mcpmodel.Tool{Name: name, InputSchema: emptyObjectSchema()}
	t.Title, _ = zc.str(config["title"])
	t.Description, _ = zc.str(config["description"])
	if v, ok := config["inputSchema"]; ok {
		t.InputSchema, _ = zc.shapeSchema(v)
	}
	if v, ok := config["outputSchema"]; ok {
		t.OutputSchema, _ = zc.shapeSchema(v)
	}
	if v, ok := config["annotations"]; ok {
		t.Annotations = annotationsValue(zc.resolve(v))
	}
	return t, true
}

// legacyTool handles tool(name, description?, shape?, annotations?, cb),
// telling the optional arguments apart the way the SDK does: a string is
// the description, the first object of zod types is the shape, and a
// remaining object is the annotations.
func (zc *zodConverter) legacyTool(args []any) (mcpmodel.Tool, bool) {
	if len(args) < 2 {
		return mcpmodel.Tool{}, false
	}
	name, ok := zc.str(args[0])
	if !ok {
		return mcpmodel.Tool{}, false
	}
	t := mcpmodel.Tool{Name: name, InputSchema: emptyObjectSchema()}
	shapeSeen := false
	for _, arg := range args[1 : len(args)-1] {
		arg = zc.resolve(arg)
		if s, isStr := asString(arg); isStr && t.Description == "" {
			t.Description = s
			continue
		}
		obj, isObj := arg.(map[string]any)
		switch {
		case isObj && !shapeSeen && isZodShape(obj):
			t.InputSchema, shapeSeen = zc.objectSchema(obj), true
		case isObj:
			t.Annotations = annotationsValue(obj)
		}
	}
	return t, true
}

// isZodShape reports whether every value of obj is a zod expression (an
// empty object counts, as in the SDK).
func isZodShape(obj map[string]any) bool {
	for _, v := range obj {
		ch, ok := v.(*chainValue)
		if !ok || ch.base != nil {
			return false
		}
	}
	return true
}
