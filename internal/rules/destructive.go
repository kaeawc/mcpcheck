package rules

import (
	"strings"

	"github.com/kaeawc/mcpcheck/internal/v2"
)

// destructivePrefixes is the heuristic list of name prefixes that imply a
// mutating, hard-to-reverse, or externally-visible action. The rule fires
// when a tool's name starts with one of these (or matches one of them
// exactly, single-word case) and the schema lacks an explicit confirmation
// field.
var destructivePrefixes = []string{
	"delete",
	"remove",
	"drop",
	"purge",
	"destroy",
	"clear",
	"reset",
	"send",
	"post",
	"publish",
	"transfer",
	"pay",
	"charge",
	"submit",
	"merge",
	"deploy",
	"close",
	"archive",
	"revoke",
}

// confirmationFieldNames is the set of input-schema property names treated
// as evidence that the tool requires explicit confirmation before acting.
// The match is case-insensitive.
var confirmationFieldNames = map[string]struct{}{
	"confirm":                 {},
	"confirmed":               {},
	"confirmation":            {},
	"requires_confirmation":   {},
	"requiresconfirmation":    {},
	"acknowledged":            {},
	"acknowledge":             {},
	"i_understand":            {},
	"iunderstand":             {},
	"force":                   {},
	"dry_run":                 {},
	"dryrun":                  {},
}

func init() {
	v2.Register(&v2.Rule{
		ID:          "destructive-tool-not-gated",
		Category:    v2.CatSafety,
		Severity:    v2.SevError,
		Description: "Tools whose name implies a destructive or externally-visible action must require explicit confirmation through the schema.",
		Fix:         v2.FixNone,
		Implementation: func(ctx *v2.Context) {
			name := ctx.Tool.Name
			prefix := destructivePrefix(name)
			if prefix == "" {
				return
			}
			if hasConfirmationField(ctx.Tool.InputSchema) {
				return
			}
			ctx.Report(
				"tool name starts with destructive prefix \"" + prefix +
					"\" but schema has no confirmation field " +
					"(expected one of: confirm, confirmed, requires_confirmation, acknowledged, dry_run, force)",
			)
		},
	})
}

// destructivePrefix returns the destructive prefix matched by name, or ""
// if name is not destructive. A name is "destructive" if it is exactly one
// of the prefixes (single-word tool name like "delete") or starts with the
// prefix followed by an underscore ("delete_user").
func destructivePrefix(name string) string {
	if name == "" {
		return ""
	}
	lower := strings.ToLower(name)
	for _, p := range destructivePrefixes {
		if lower == p || strings.HasPrefix(lower, p+"_") {
			return p
		}
	}
	return ""
}

// hasConfirmationField returns true if the input schema declares a property
// whose name (case-insensitively, with "_" / "-" stripped) is in the
// confirmation set.
func hasConfirmationField(schema map[string]any) bool {
	if schema == nil {
		return false
	}
	propsAny, ok := schema["properties"]
	if !ok {
		return false
	}
	props, ok := propsAny.(map[string]any)
	if !ok {
		return false
	}
	for key := range props {
		if isConfirmationName(key) {
			return true
		}
	}
	return false
}

// isConfirmationName reports whether key is a confirmation-shaped property
// name. Comparison is case-insensitive and the matcher accepts both the
// snake_case and concatenated forms.
func isConfirmationName(key string) bool {
	normalized := strings.ToLower(key)
	if _, ok := confirmationFieldNames[normalized]; ok {
		return true
	}
	stripped := strings.ReplaceAll(normalized, "_", "")
	stripped = strings.ReplaceAll(stripped, "-", "")
	_, ok := confirmationFieldNames[stripped]
	return ok
}
