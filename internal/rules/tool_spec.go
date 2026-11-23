package rules

import (
	"fmt"
	"regexp"
	"sort"
	"strings"

	v2 "github.com/kaeawc/mcpcheck/internal/v2"
)

// toolNameRe is the tool-name grammar from protocol 2025-11-25: 1–128
// characters from A–Z, a–z, 0–9, underscore, hyphen, and dot.
var toolNameRe = regexp.MustCompile(`^[A-Za-z0-9_.-]{1,128}$`)

// knownAnnotations are the ToolAnnotations fields and their JSON types.
var knownAnnotations = map[string]string{
	"title":           "string",
	"readOnlyHint":    "boolean",
	"destructiveHint": "boolean",
	"idempotentHint":  "boolean",
	"openWorldHint":   "boolean",
}

func init() {
	v2.Register(&v2.Rule{
		ID:             "tool-name-invalid",
		Category:       v2.CatProtocol,
		Severity:       v2.SevError,
		Description:    "Tool names must be 1–128 characters of A–Z, a–z, 0–9, _, -, or . so every client can route calls to them.",
		Fix:            v2.FixNone,
		Implementation: checkToolName,
	})
	v2.Register(&v2.Rule{
		ID:             "tool-input-schema-invalid",
		Category:       v2.CatProtocol,
		Severity:       v2.SevError,
		Description:    "Every tool must advertise an inputSchema that is a JSON Schema object with \"type\": \"object\".",
		Fix:            v2.FixNone,
		Implementation: checkInputSchema,
	})
	v2.Register(&v2.Rule{
		ID:          "tool-output-schema-invalid",
		Category:    v2.CatProtocol,
		Severity:    v2.SevError,
		Description: "A tool's outputSchema, when present, must be a JSON Schema object with \"type\": \"object\".",
		Fix:         v2.FixNone,
		Implementation: func(ctx *v2.Context) {
			if ctx.Tool.Raw != nil {
				if v, ok := ctx.Tool.Raw["outputSchema"]; ok {
					checkObjectSchema(ctx, "outputSchema", v)
				}
			}
		},
	})
	v2.Register(&v2.Rule{
		ID:             "tool-annotations-inconsistent",
		Category:       v2.CatProtocol,
		Severity:       v2.SevWarning,
		Description:    "Tool annotations must use the spec's field types and must not contradict each other or the tool's name.",
		Fix:            v2.FixNone,
		Implementation: checkAnnotations,
	})
}

func checkToolName(ctx *v2.Context) {
	name := ctx.Tool.Name
	switch {
	case name == "" && ctx.Tool.Raw != nil:
		if _, ok := ctx.Tool.Raw["name"].(string); !ok {
			ctx.Report("tool has no string \"name\" field")
			return
		}
		ctx.Report("tool name is empty")
	case name == "":
	case len(name) > 128:
		ctx.Report(fmt.Sprintf("tool name is %d characters; the limit is 128", len(name)))
	case !toolNameRe.MatchString(name):
		ctx.Report(fmt.Sprintf("tool name %q contains characters outside A–Z a–z 0–9 _ - . (e.g. %q); many clients reject or mangle such names",
			name, firstInvalidNameChar(name)))
	}
}

func firstInvalidNameChar(name string) string {
	for _, r := range name {
		if !toolNameRe.MatchString(string(r)) {
			return string(r)
		}
	}
	return ""
}

func checkInputSchema(ctx *v2.Context) {
	if ctx.Tool.Raw == nil {
		if ctx.Tool.InputSchema != nil {
			checkObjectSchema(ctx, "inputSchema", ctx.Tool.InputSchema)
		}
		return
	}
	v, ok := ctx.Tool.Raw["inputSchema"]
	if !ok || v == nil {
		ctx.Report("tool has no inputSchema; the spec requires one even for tools without arguments — use {\"type\": \"object\"}")
		return
	}
	checkObjectSchema(ctx, "inputSchema", v)
}

func checkObjectSchema(ctx *v2.Context, field string, v any) {
	schema, ok := v.(map[string]any)
	if !ok {
		ctx.Report(fmt.Sprintf("%s must be a JSON object, got %s", field, jsonType(v)))
		return
	}
	typ, ok := schema["type"]
	if !ok {
		ctx.Report(fmt.Sprintf("%s has no \"type\"; it must declare \"type\": \"object\"", field))
		return
	}
	if typ != "object" {
		ctx.Report(fmt.Sprintf("%s has \"type\": %v; it must be \"object\" (wrap other values in a property)", field, typ))
		return
	}
	if props, ok := schema["properties"]; ok {
		if _, isObj := props.(map[string]any); !isObj {
			ctx.Report(fmt.Sprintf("%s.properties must be an object, got %s", field, jsonType(props)))
		}
	}
}

func checkAnnotations(ctx *v2.Context) {
	if !annotationsAreObject(ctx) {
		return
	}
	ann := ctx.Tool.Annotations
	if ann == nil {
		return
	}
	checkAnnotationTypes(ctx, ann)
	readOnly, _ := ann["readOnlyHint"].(bool)
	destructive, hasDestructive := ann["destructiveHint"].(bool)
	if readOnly && hasDestructive && destructive {
		ctx.Report("annotations set both readOnlyHint and destructiveHint to true; a read-only tool cannot be destructive")
	}
	if readOnly {
		if prefix := destructivePrefix(ctx.Tool.Name); prefix != "" {
			ctx.Report(fmt.Sprintf("tool is annotated readOnlyHint: true but its name starts with %q, which implies it modifies state", prefix))
		}
	}
}

// annotationsAreObject reports a non-object annotations field on a
// protocol tool and returns false in that case.
func annotationsAreObject(ctx *v2.Context) bool {
	if ctx.Tool.Raw == nil {
		return true
	}
	v, ok := ctx.Tool.Raw["annotations"]
	if !ok || v == nil {
		return true
	}
	if _, isObj := v.(map[string]any); !isObj {
		ctx.Report(fmt.Sprintf("annotations must be an object, got %s", jsonType(v)))
		return false
	}
	return true
}

func checkAnnotationTypes(ctx *v2.Context, ann map[string]any) {
	keys := make([]string, 0, len(ann))
	for k := range ann {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		want, known := knownAnnotations[k]
		if !known {
			continue
		}
		if got := jsonType(ann[k]); got != want {
			ctx.Report(fmt.Sprintf("annotations.%s must be a %s, got %s", k, want, got))
		}
	}
}

func jsonType(v any) string {
	switch v.(type) {
	case nil:
		return "null"
	case bool:
		return "boolean"
	case string:
		return "string"
	case float64, int, int64:
		return "number"
	case []any:
		return "array"
	case map[string]any:
		return "object"
	}
	return strings.TrimPrefix(fmt.Sprintf("%T", v), "*")
}
