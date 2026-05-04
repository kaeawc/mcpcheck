package rules

import (
	"strings"

	"github.com/kaeawc/mcpcheck/internal/v2"
)

// minDescriptionLen is the threshold below which a non-empty description is
// considered too sparse to be useful. Tuned conservatively so that genuinely
// short descriptions like "ping" still pass — we only want to catch obvious
// placeholders like "x" or "TBD".
const minDescriptionLen = 4

func init() {
	v2.Register(&v2.Rule{
		ID:          "tool-description-empty-or-truncated",
		Category:    v2.CatSpec,
		Severity:    v2.SevWarning,
		Description: "Tool descriptions must be non-empty and not visibly truncated; agents rely on them to choose tools.",
		Fix:         v2.FixNone,
		Implementation: func(ctx *v2.Context) {
			desc := ctx.Tool.Description
			trimmed := strings.TrimSpace(desc)

			if trimmed == "" {
				ctx.Report("tool description is empty")
				return
			}
			if len(trimmed) < minDescriptionLen {
				ctx.Report("tool description is too short to be informative")
				return
			}
			if endsTruncated(trimmed) {
				ctx.Report("tool description appears to be truncated")
				return
			}
		},
	})
}

// endsTruncated returns true when the description ends with markers that
// indicate it was cut off — three ASCII dots or the unicode ellipsis.
func endsTruncated(s string) bool {
	switch {
	case strings.HasSuffix(s, "..."):
		return true
	case strings.HasSuffix(s, "…"):
		return true
	}
	return false
}
