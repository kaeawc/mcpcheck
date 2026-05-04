package rules

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/kaeawc/mcpcheck/internal/v2"
)

// piiPattern groups a pattern with its human-readable kind for finding
// messages and a sentinel-value allowlist (literal lowercase strings
// that look like PII but are universally understood as placeholders).
type piiPattern struct {
	kind        string
	re          *regexp.Regexp
	allowlist   func(matched string) bool
}

var (
	piiEmailRe = regexp.MustCompile(`\b[A-Za-z0-9._%+\-]+@[A-Za-z0-9.\-]+\.[A-Za-z]{2,}\b`)
	piiSSNRe   = regexp.MustCompile(`\b\d{3}-\d{2}-\d{4}\b`)
	piiPhoneRe = regexp.MustCompile(`(?:\(\d{3}\)\s?\d{3}-\d{4}|\b\d{3}-\d{3}-\d{4}\b)`)
)

var piiPatterns = []piiPattern{
	{
		kind:      "email",
		re:        piiEmailRe,
		allowlist: emailIsPlaceholder,
	},
	{
		kind:      "ssn",
		re:        piiSSNRe,
		allowlist: ssnIsPlaceholder,
	},
	{
		kind:      "phone",
		re:        piiPhoneRe,
		allowlist: phoneIsPlaceholder,
	},
}

func init() {
	v2.Register(&v2.Rule{
		ID:          "tool-example-uses-real-pii",
		Category:    v2.CatExamples,
		Severity:    v2.SevWarning,
		Description: "Schema examples should use placeholder values for emails, SSNs, and phone numbers; real-looking PII suggests it was pasted from production.",
		Fix:         v2.FixNone,
		Implementation: func(ctx *v2.Context) {
			examples := extractExamples(ctx.Tool.InputSchema)
			for i, ex := range examples {
				if hit := scanForPII(ex); hit != "" {
					ctx.Report(fmt.Sprintf(
						"inputSchema.examples[%d] contains a %s; use a placeholder (e.g. user@example.com, 555-555-0100, 123-45-6789)",
						i, hit,
					))
				}
			}
		},
	})
}

// scanForPII recursively walks v looking for the first PII match. Returns
// a human-readable kind (e.g. "real-looking email") on hit, or "" on
// none.
func scanForPII(v any) string {
	switch x := v.(type) {
	case string:
		return scanStringForPII(x)
	case map[string]any:
		for _, child := range x {
			if hit := scanForPII(child); hit != "" {
				return hit
			}
		}
	case []any:
		for _, child := range x {
			if hit := scanForPII(child); hit != "" {
				return hit
			}
		}
	}
	return ""
}

func scanStringForPII(s string) string {
	for _, p := range piiPatterns {
		matches := p.re.FindAllString(s, -1)
		for _, m := range matches {
			if p.allowlist != nil && p.allowlist(strings.ToLower(m)) {
				continue
			}
			return "real-looking " + p.kind
		}
	}
	return ""
}

// Allowlist functions return true when the matched string is a recognized
// placeholder pattern that should NOT fire the rule.

var placeholderEmailDomains = []string{
	"@example.com",
	"@example.org",
	"@example.net",
	"@test.com",
	"@invalid",
	"@localhost",
}

func emailIsPlaceholder(matched string) bool {
	for _, d := range placeholderEmailDomains {
		if strings.HasSuffix(matched, d) {
			return true
		}
	}
	return false
}

// Common universally-fake SSN values used in documentation and test
// fixtures. The IRS publishes these as never-issued sequences.
var placeholderSSNs = map[string]struct{}{
	"000-00-0000": {},
	"111-11-1111": {},
	"123-45-6789": {},
	"987-65-4321": {},
}

func ssnIsPlaceholder(matched string) bool {
	_, ok := placeholderSSNs[matched]
	return ok
}

// US phones with 555 in the area code or the central-office (exchange)
// position are reserved for fictional use. Examples: 555-867-5309,
// (555) 867-5309, 415-555-0100.
func phoneIsPlaceholder(matched string) bool {
	digits := digitsOnly(matched)
	switch len(digits) {
	case 7:
		// 7-digit local form: NXX-XXXX. Treat 555-XXXX as placeholder.
		return strings.HasPrefix(digits, "555")
	case 10:
		// NPA-NXX-XXXX. Either NPA == 555 or NXX == 555 qualifies.
		return digits[0:3] == "555" || digits[3:6] == "555"
	}
	return false
}

func digitsOnly(s string) string {
	var b strings.Builder
	for _, r := range s {
		if r >= '0' && r <= '9' {
			b.WriteRune(r)
		}
	}
	return b.String()
}
