package rules

import (
	"fmt"
	"sort"

	"github.com/kaeawc/mcpcheck/internal/v2"
)

func init() {
	v2.Register(&v2.Rule{
		ID:          "tool-schema-required-references-undefined-property",
		Category:    v2.CatSpec,
		Severity:    v2.SevError,
		Description: "Every name in inputSchema.required must exist in inputSchema.properties; otherwise the schema is internally inconsistent.",
		Fix:         v2.FixNone,
		Implementation: func(ctx *v2.Context) {
			schema := ctx.Tool.InputSchema
			if schema == nil {
				return
			}
			required := extractRequired(schema)
			if len(required) == 0 {
				return
			}
			properties := propertyNames(schema)

			var missing []string
			for _, name := range required {
				if _, ok := properties[name]; !ok {
					missing = append(missing, name)
				}
			}
			if len(missing) == 0 {
				return
			}
			sort.Strings(missing)
			ctx.Report(fmt.Sprintf(
				"inputSchema.required references property name(s) not in properties: %v",
				missing,
			))
		},
	})
}

// extractRequired returns the names listed under inputSchema.required, or
// nil if absent / malformed. JSON Schema mandates this be an array of
// unique strings; we tolerate non-string entries by skipping them rather
// than reporting (that's the schema-validity rule's territory).
func extractRequired(schema map[string]any) []string {
	raw, ok := schema["required"]
	if !ok {
		return nil
	}
	arr, ok := raw.([]any)
	if !ok {
		return nil
	}
	out := make([]string, 0, len(arr))
	for _, v := range arr {
		if s, ok := v.(string); ok {
			out = append(out, s)
		}
	}
	return out
}

// propertyNames returns the set of property names declared in
// inputSchema.properties.
func propertyNames(schema map[string]any) map[string]struct{} {
	props, ok := schema["properties"].(map[string]any)
	if !ok {
		return nil
	}
	out := make(map[string]struct{}, len(props))
	for name := range props {
		out[name] = struct{}{}
	}
	return out
}
