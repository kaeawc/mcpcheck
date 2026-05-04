package rules_test

import (
	"strings"
	"testing"

	"github.com/kaeawc/mcpcheck/internal/mcpmodel"
	_ "github.com/kaeawc/mcpcheck/internal/rules"
	"github.com/kaeawc/mcpcheck/internal/v2"
)

func findStaticVsLiveRule(t *testing.T) *v2.Rule {
	t.Helper()
	for _, r := range v2.All() {
		if r.ID == "tool-static-vs-live-mismatch" {
			return r
		}
	}
	t.Fatal("tool-static-vs-live-mismatch not registered")
	return nil
}

func TestStaticVsLive_StaticOnlyAndLiveOnly(t *testing.T) {
	staticSet := &mcpmodel.ToolSet{
		Tools: []mcpmodel.Tool{
			{Name: "fetch_user", SourceFile: "server.py", SourceLine: 12},
			{Name: "send_message", SourceFile: "server.py", SourceLine: 24},
			{Name: "static_only", SourceFile: "server.py", SourceLine: 36},
		},
	}
	live := &mcpmodel.ToolSet{
		Tools: []mcpmodel.Tool{
			{Name: "fetch_user"},
			{Name: "send_message"},
			{Name: "live_only_a"},
			{Name: "live_only_b"},
		},
	}
	rule := findStaticVsLiveRule(t)
	findings := v2.RunComparison([]*v2.Rule{rule}, staticSet, live)

	if got, want := len(findings), 3; got != want {
		t.Fatalf("got %d findings, want %d: %+v", got, want, findings)
	}

	// Findings should mention each unique-side tool exactly once.
	names := make(map[string]bool)
	for _, f := range findings {
		names[f.ToolName] = true
		switch f.ToolName {
		case "static_only":
			if !strings.Contains(f.Message, "static intake") {
				t.Errorf("static_only message should mention static intake: %q", f.Message)
			}
			if f.SourceFile != "server.py" {
				t.Errorf("static_only finding should carry source coords: %+v", f)
			}
		case "live_only_a", "live_only_b":
			if !strings.Contains(f.Message, "runtime") {
				t.Errorf("live_only message should mention runtime: %q", f.Message)
			}
		default:
			t.Errorf("unexpected finding for %q", f.ToolName)
		}
	}
	for _, want := range []string{"static_only", "live_only_a", "live_only_b"} {
		if !names[want] {
			t.Errorf("missing finding for %q", want)
		}
	}
}

func TestStaticVsLive_FullyMatchedSetsProduceNothing(t *testing.T) {
	staticSet := &mcpmodel.ToolSet{Tools: []mcpmodel.Tool{{Name: "a"}, {Name: "b"}}}
	live := &mcpmodel.ToolSet{Tools: []mcpmodel.Tool{{Name: "b"}, {Name: "a"}}}
	rule := findStaticVsLiveRule(t)
	findings := v2.RunComparison([]*v2.Rule{rule}, staticSet, live)
	if len(findings) != 0 {
		t.Fatalf("expected no findings on matching sets; got %+v", findings)
	}
}

func TestStaticVsLive_NilSidesGuard(t *testing.T) {
	rule := findStaticVsLiveRule(t)
	if findings := v2.RunComparison([]*v2.Rule{rule}, nil, &mcpmodel.ToolSet{}); len(findings) != 0 {
		t.Errorf("nil static side should not produce findings: %+v", findings)
	}
	if findings := v2.RunComparison([]*v2.Rule{rule}, &mcpmodel.ToolSet{}, nil); len(findings) != 0 {
		t.Errorf("nil live side should not produce findings: %+v", findings)
	}
}
