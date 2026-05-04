package rules

import (
	"strings"

	"github.com/kaeawc/mcpcheck/internal/v2"
)

// urlPropertyNames is the set of input-property names that strongly imply
// the tool will dial that URL on the agent's behalf. Match is
// case-insensitive against the lowered + separator-stripped property name
// (so "targetURL", "target_url", and "target-url" all hit "targeturl").
var urlPropertyNames = map[string]struct{}{
	"url":          {},
	"uri":          {},
	"endpoint":     {},
	"webhook":      {},
	"webhookurl":   {},
	"callbackurl":  {},
	"targeturl":    {},
	"redirecturl":  {},
	"requesturl":   {},
}

func init() {
	v2.Register(&v2.Rule{
		ID:          "network-tool-no-allowlist",
		Category:    v2.CatSafety,
		Severity:    v2.SevWarning,
		Description: "Tools that accept a user-supplied URL must constrain the destination via an enum, pattern, or const so an agent cannot drive arbitrary outbound requests (SSRF).",
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
				if !isURLPropertyName(key) {
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
					"input property \"" + key + "\" looks like a URL but has no allowlist " +
						"(expected one of: enum, pattern, const) — agent could drive arbitrary outbound requests",
				)
				return
			}
		},
	})
}

func isURLPropertyName(key string) bool {
	stripped := strings.ToLower(key)
	stripped = strings.ReplaceAll(stripped, "_", "")
	stripped = strings.ReplaceAll(stripped, "-", "")
	_, ok := urlPropertyNames[stripped]
	return ok
}

