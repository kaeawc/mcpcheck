package rules_test

import (
	"path/filepath"
	"testing"

	"github.com/kaeawc/mcpcheck/internal/scanner"
	v2 "github.com/kaeawc/mcpcheck/internal/v2"
)

// TestToolSpecRulesFireOncePerPositiveTool checks that every tool in the
// positive fixtures of the per-tool protocol rules is a distinct case:
// each one must produce exactly one finding.
func TestToolSpecRulesFireOncePerPositiveTool(t *testing.T) {
	for _, ruleID := range []string{
		"tool-name-invalid",
		"tool-input-schema-invalid",
		"tool-output-schema-invalid",
		"tool-mcp-header-invalid",
	} {
		t.Run(ruleID, func(t *testing.T) {
			set, err := scanner.LoadToolsJSON(filepath.Join("..", "..", "tests", "fixtures", "positive", ruleID, "tools.json"))
			if err != nil {
				t.Fatal(err)
			}
			rule := findRule(t, ruleID)
			for i := range set.Tools {
				if got := len(v2.Run([]*v2.Rule{rule}, &set.Tools[i])); got != 1 {
					t.Errorf("tool %d (%q): %d findings, want 1", i, set.Tools[i].Name, got)
				}
			}
		})
	}
}

func TestAnnotationsInconsistentCases(t *testing.T) {
	set, err := scanner.LoadToolsJSON(filepath.Join("..", "..", "tests", "fixtures", "positive", "tool-annotations-inconsistent", "tools.json"))
	if err != nil {
		t.Fatal(err)
	}
	rule := findRule(t, "tool-annotations-inconsistent")
	want := map[string]string{
		"delete_file": "tool is annotated readOnlyHint: true but its name starts with \"delete\", which implies it modifies state",
		"fetch_url":   "annotations.openWorldHint must be a boolean, got string",
		"list_files":  "annotations must be an object, got string",
	}
	for i := range set.Tools {
		tool := &set.Tools[i]
		findings := v2.Run([]*v2.Rule{rule}, tool)
		if len(findings) != 1 || findings[0].Message != want[tool.Name] {
			t.Errorf("%s: got %+v, want one finding %q", tool.Name, findings, want[tool.Name])
		}
	}
}

func TestServerRulesSilentForStaticSets(t *testing.T) {
	set, err := scanner.LoadToolsJSON(filepath.Join("..", "..", "tests", "fixtures", "negative", "tool-name-invalid", "tools.json"))
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{
		"server-stdout-not-protocol", "server-jsonrpc-malformed", "server-protocol-version-unsupported",
		"server-lifecycle-nonconformant", "server-transport-nonconformant", "server-tools-pagination-broken",
	} {
		if n := len(v2.RunProject([]*v2.Rule{findRule(t, id)}, set)); n != 0 {
			t.Errorf("%s: %d findings on a set without a server", id, n)
		}
	}
}
