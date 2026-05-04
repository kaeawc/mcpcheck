package output

import (
	"encoding/json"
	"fmt"
	"io"
	"sort"

	"github.com/kaeawc/mcpcheck/internal/v2"
)

// WriteRulesText prints registered rules in a tabular form suitable for
// terminal output. Rules are sorted by id for stable output.
func WriteRulesText(w io.Writer, rules []*v2.Rule) error {
	sorted := append([]*v2.Rule(nil), rules...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].ID < sorted[j].ID })

	for _, r := range sorted {
		fix := string(r.Fix)
		if fix == "" {
			fix = "-"
		}
		if _, err := fmt.Fprintf(w, "%-40s  %-8s  %-25s  fix=%-9s  %s\n",
			r.ID, r.Severity, r.Category, fix, r.Description); err != nil {
			return err
		}
	}
	return nil
}

// WriteRulesJSON emits registered rules as a JSON array, one object per
// rule. Field order matches the v2.Rule shape so downstream tooling
// (docs generators, rule-set diff tools) can consume it stably.
func WriteRulesJSON(w io.Writer, rules []*v2.Rule) error {
	type ruleJSON struct {
		ID          string `json:"id"`
		Category    string `json:"category"`
		Severity    string `json:"severity"`
		Description string `json:"description"`
		Fix         string `json:"fix,omitempty"`
		ProjectScope bool  `json:"projectScope,omitempty"`
	}

	sorted := append([]*v2.Rule(nil), rules...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].ID < sorted[j].ID })

	out := make([]ruleJSON, 0, len(sorted))
	for _, r := range sorted {
		out = append(out, ruleJSON{
			ID:           r.ID,
			Category:     string(r.Category),
			Severity:     string(r.Severity),
			Description:  r.Description,
			Fix:          string(r.Fix),
			ProjectScope: r.ProjectImplementation != nil,
		})
	}
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(out)
}
