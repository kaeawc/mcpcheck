package rules_test

import (
	"strings"
	"testing"

	"github.com/kaeawc/mcpcheck/internal/mcpmodel"
	_ "github.com/kaeawc/mcpcheck/internal/rules"
	"github.com/kaeawc/mcpcheck/internal/v2"
)

func TestCollisionRule_PointsAtPeers(t *testing.T) {
	set := &mcpmodel.ToolSet{
		Tools: []mcpmodel.Tool{
			{Name: "fetch_user", SourceFile: "a.py", SourceLine: 10},
			{Name: "fetch_user", SourceFile: "b.py", SourceLine: 22},
			{Name: "send_message", SourceFile: "a.py", SourceLine: 30},
		},
	}

	rule := findCollisionRule(t)
	findings := v2.RunProject([]*v2.Rule{rule}, set)
	if got, want := len(findings), 2; got != want {
		t.Fatalf("got %d findings, want %d: %+v", got, want, findings)
	}

	// Each finding should mention the peer's location, not its own.
	for _, f := range findings {
		switch f.SourceFile {
		case "a.py":
			if !strings.Contains(f.Message, "b.py:22") {
				t.Errorf("finding at a.py:%d should reference b.py:22; got %q", f.SourceLine, f.Message)
			}
			if strings.Contains(f.Message, "a.py:10") {
				t.Errorf("finding at a.py:10 should not reference itself; got %q", f.Message)
			}
		case "b.py":
			if !strings.Contains(f.Message, "a.py:10") {
				t.Errorf("finding at b.py:%d should reference a.py:10; got %q", f.SourceLine, f.Message)
			}
		default:
			t.Errorf("unexpected finding source file: %q", f.SourceFile)
		}
		if f.Severity != v2.SevError {
			t.Errorf("collision finding severity = %s, want error", f.Severity)
		}
	}
}

func TestCollisionRule_IgnoresUniqueAndEmpty(t *testing.T) {
	set := &mcpmodel.ToolSet{
		Tools: []mcpmodel.Tool{
			{Name: "fetch_user"},
			{Name: "fetch_org"},
			{Name: ""}, // empty names are someone else's problem
			{Name: ""}, // duplicates of empty must not collide here
		},
	}
	rule := findCollisionRule(t)
	findings := v2.RunProject([]*v2.Rule{rule}, set)
	if len(findings) != 0 {
		t.Fatalf("expected no findings on unique-and-empty fixture; got %+v", findings)
	}
}

func findCollisionRule(t *testing.T) *v2.Rule {
	t.Helper()
	for _, r := range v2.All() {
		if r.ID == "tool-name-collision" {
			return r
		}
	}
	t.Fatal("tool-name-collision rule not registered")
	return nil
}
