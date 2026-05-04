package rules_test

import (
	"path/filepath"
	"testing"

	"github.com/kaeawc/mcpcheck/internal/mcpmodel"
	_ "github.com/kaeawc/mcpcheck/internal/rules"
	"github.com/kaeawc/mcpcheck/internal/scanner"
	"github.com/kaeawc/mcpcheck/internal/v2"
)

// TestPositiveFixtures asserts that each positive fixture produces at least
// one finding for the rule it's named after. Fixtures live at
// tests/fixtures/positive/<rule-id>/tools.json.
func TestPositiveFixtures(t *testing.T) {
	cases := []string{
		"tool-name-not-snake-case",
		"tool-description-empty-or-truncated",
		"tool-description-mentions-secret",
		"destructive-tool-not-gated",
		"tool-name-collision",
	}
	for _, ruleID := range cases {
		t.Run(ruleID, func(t *testing.T) {
			path := filepath.Join("..", "..", "tests", "fixtures", "positive", ruleID, "tools.json")
			set, err := scanner.LoadToolsJSON(path)
			if err != nil {
				t.Fatalf("load fixture: %v", err)
			}
			rule := findRule(t, ruleID)
			if hits := runRule(rule, set); hits == 0 {
				t.Fatalf("rule %q produced no findings on positive fixture", ruleID)
			}
		})
	}
}

// TestNegativeFixtures asserts that each negative fixture produces zero
// findings for the rule it's named after.
func TestNegativeFixtures(t *testing.T) {
	cases := []string{
		"tool-name-not-snake-case",
		"tool-description-empty-or-truncated",
		"tool-description-mentions-secret",
		"destructive-tool-not-gated",
		"tool-name-collision",
	}
	for _, ruleID := range cases {
		t.Run(ruleID, func(t *testing.T) {
			path := filepath.Join("..", "..", "tests", "fixtures", "negative", ruleID, "tools.json")
			set, err := scanner.LoadToolsJSON(path)
			if err != nil {
				t.Fatalf("load fixture: %v", err)
			}
			rule := findRule(t, ruleID)
			if hits := runRule(rule, set); hits > 0 {
				t.Fatalf("rule %q reported on negative fixture (%d findings)", ruleID, hits)
			}
		})
	}
}

// runRule dispatches per-tool and project-scope phases as appropriate for
// the rule's declared callbacks, returning the total finding count.
func runRule(rule *v2.Rule, set *mcpmodel.ToolSet) int {
	hits := 0
	if rule.Implementation != nil {
		for i := range set.Tools {
			hits += len(v2.Run([]*v2.Rule{rule}, &set.Tools[i]))
		}
	}
	if rule.ProjectImplementation != nil {
		hits += len(v2.RunProject([]*v2.Rule{rule}, set))
	}
	return hits
}

func findRule(t *testing.T, id string) *v2.Rule {
	t.Helper()
	for _, r := range v2.All() {
		if r.ID == id {
			return r
		}
	}
	t.Fatalf("rule %q not registered", id)
	return nil
}
