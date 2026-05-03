package rules_test

import (
	"path/filepath"
	"testing"

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
	}
	for _, ruleID := range cases {
		t.Run(ruleID, func(t *testing.T) {
			path := filepath.Join("..", "..", "tests", "fixtures", "positive", ruleID, "tools.json")
			set, err := scanner.LoadToolsJSON(path)
			if err != nil {
				t.Fatalf("load fixture: %v", err)
			}
			rule := findRule(t, ruleID)
			var hits int
			for i := range set.Tools {
				findings := v2.Run([]*v2.Rule{rule}, &set.Tools[i])
				hits += len(findings)
			}
			if hits == 0 {
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
	}
	for _, ruleID := range cases {
		t.Run(ruleID, func(t *testing.T) {
			path := filepath.Join("..", "..", "tests", "fixtures", "negative", ruleID, "tools.json")
			set, err := scanner.LoadToolsJSON(path)
			if err != nil {
				t.Fatalf("load fixture: %v", err)
			}
			rule := findRule(t, ruleID)
			for i := range set.Tools {
				if findings := v2.Run([]*v2.Rule{rule}, &set.Tools[i]); len(findings) > 0 {
					t.Fatalf("rule %q reported on negative fixture tool %q: %+v",
						ruleID, set.Tools[i].Name, findings)
				}
			}
		})
	}
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
