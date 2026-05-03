// Package output formats analyzer findings for human and machine consumers.
package output

import (
	"encoding/json"
	"fmt"
	"io"
	"sort"

	"github.com/kaeawc/mcpcheck/internal/v2"
)

// WriteText prints findings in a compact one-per-line form suitable for
// terminal output. Findings are grouped and sorted by tool name then rule.
func WriteText(w io.Writer, findings []v2.Finding) error {
	sorted := append([]v2.Finding(nil), findings...)
	sort.SliceStable(sorted, func(i, j int) bool {
		if sorted[i].ToolName != sorted[j].ToolName {
			return sorted[i].ToolName < sorted[j].ToolName
		}
		return sorted[i].RuleID < sorted[j].RuleID
	})

	for _, f := range sorted {
		loc := f.SourceFile
		if loc == "" {
			loc = "<source>"
		}
		if f.SourceLine > 0 {
			loc = fmt.Sprintf("%s:%d", loc, f.SourceLine)
		}
		if _, err := fmt.Fprintf(w, "%s: %s [%s] tool=%q %s\n",
			loc, f.Severity, f.RuleID, f.ToolName, f.Message); err != nil {
			return err
		}
	}
	return nil
}

// WriteJSON emits findings as a JSON array, one object per finding. The
// shape is stable enough for downstream tooling to depend on.
func WriteJSON(w io.Writer, findings []v2.Finding) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	if findings == nil {
		findings = []v2.Finding{}
	}
	return enc.Encode(findings)
}
