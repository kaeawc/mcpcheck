package rules

import (
	"unicode"

	"github.com/kaeawc/mcpcheck/internal/v2"
)

func init() {
	v2.Register(&v2.Rule{
		ID:          "tool-name-not-snake-case",
		Category:    v2.CatSpec,
		Severity:    v2.SevWarning,
		Description: "MCP tool names should be snake_case so they read predictably in agent tool lists.",
		Fix:         v2.FixCosmetic,
		Implementation: func(ctx *v2.Context) {
			name := ctx.Tool.Name
			if name == "" {
				return // empty-name handling belongs to a different rule
			}
			if !isSnakeCase(name) {
				ctx.Report("tool name " + quote(name) + " is not snake_case")
			}
		},
	})
}

func isSnakeCase(s string) bool {
	if s == "" {
		return false
	}
	for i, r := range s {
		switch {
		case unicode.IsLower(r):
			// ok
		case unicode.IsDigit(r):
			if i == 0 {
				return false
			}
		case r == '_':
			if i == 0 || i == len(s)-1 {
				return false
			}
		default:
			return false
		}
	}
	return true
}

func quote(s string) string { return "\"" + s + "\"" }
