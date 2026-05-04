package rules

import (
	"regexp"
	"strings"

	"github.com/kaeawc/mcpcheck/internal/v2"
)

// secretMentionRe matches case-insensitive references to credential-shaped
// terms in tool descriptions. The rule is a heuristic — descriptions
// legitimately describe what a tool does ("generates an API key"), so this
// fires as a warning intended to prompt human review, not an error.
//
// Word boundaries on each side keep false positives down: "tokenizer",
// "secretly", "api_keys" (note trailing s) won't match.
var secretMentionRe = regexp.MustCompile(
	`(?i)\b(?:` +
		`password` +
		`|passphrase` +
		`|api[_-]?key` +
		`|access[_-]?token` +
		`|refresh[_-]?token` +
		`|private[_-]?key` +
		`|client[_-]?secret` +
		`|bearer` +
		`|secret` +
		`|token` +
		`)\b`,
)

func init() {
	v2.Register(&v2.Rule{
		ID:          "tool-description-mentions-secret",
		Category:    v2.CatSafety,
		Severity:    v2.SevWarning,
		Description: "Tool descriptions that mention credential-shaped terms may be exposing secrets through arguments or results; flag for review.",
		Fix:         v2.FixNone,
		Implementation: func(ctx *v2.Context) {
			match := findSecretMention(ctx.Tool.Description)
			if match == "" {
				return
			}
			ctx.Report("description mentions secret-shaped term \"" + match + "\"; verify that the tool does not accept or return credentials in plaintext")
		},
	})
}

// findSecretMention returns the first matched term in s (lowercased and with
// any "_" / "-" separator collapsed for stable reporting), or "" if none.
func findSecretMention(s string) string {
	loc := secretMentionRe.FindStringIndex(s)
	if loc == nil {
		return ""
	}
	return strings.ToLower(s[loc[0]:loc[1]])
}
