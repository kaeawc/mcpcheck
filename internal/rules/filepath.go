package rules

import (
	"strings"

	"github.com/kaeawc/mcpcheck/internal/v2"
)

// pathPropertyNames is the set of input-property names that imply the tool
// will read or write the named filesystem path on the agent's behalf.
// Match is case-insensitive against the lowered + separator-stripped
// property name (so "filePath", "file_path", and "file-path" all hit
// "filepath").
var pathPropertyNames = map[string]struct{}{
	"path":       {},
	"filepath":   {},
	"filename":   {},
	"directory":  {},
	"dir":        {},
	"folder":     {},
	"sourcepath": {},
	"targetpath": {},
	"outputpath": {},
	"inputpath":  {},
	"destpath":   {},
}

func init() {
	v2.Register(&v2.Rule{
		ID:          "file-tool-no-path-confinement",
		Category:    v2.CatSafety,
		Severity:    v2.SevWarning,
		Description: "Tools that accept a filesystem path must constrain it via an enum, pattern, or const so an agent cannot read or write arbitrary files (path traversal).",
		Fix:         v2.FixNone,
		Implementation: func(ctx *v2.Context) {
			schema := ctx.Tool.InputSchema
			if schema == nil {
				return
			}
			propsAny, ok := schema["properties"]
			if !ok {
				return
			}
			props, ok := propsAny.(map[string]any)
			if !ok {
				return
			}

			for key, val := range props {
				if !isPathPropertyName(key) {
					continue
				}
				prop, ok := val.(map[string]any)
				if !ok {
					continue
				}
				if hasStringValueConstraint(prop) {
					continue
				}
				ctx.Report(
					"input property \"" + key + "\" looks like a filesystem path but has no allowlist " +
						"(expected one of: enum, pattern, const) — agent could read or write arbitrary files",
				)
				return
			}
		},
	})
}

func isPathPropertyName(key string) bool {
	stripped := strings.ToLower(key)
	stripped = strings.ReplaceAll(stripped, "_", "")
	stripped = strings.ReplaceAll(stripped, "-", "")
	_, ok := pathPropertyNames[stripped]
	return ok
}
