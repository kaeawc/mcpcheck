package output

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	_ "github.com/kaeawc/mcpcheck/internal/rules" // register rules so the driver section is populated
	"github.com/kaeawc/mcpcheck/internal/v2"
)

func TestWriteSARIF_Shape(t *testing.T) {
	findings := []v2.Finding{
		{
			RuleID:     "tool-name-not-snake-case",
			Severity:   v2.SevWarning,
			Category:   v2.CatSpec,
			Message:    "tool name \"fetchUser\" is not snake_case",
			ToolName:   "fetchUser",
			SourceFile: "server.py",
			SourceLine: 42,
		},
		{
			RuleID:   "destructive-tool-not-gated",
			Severity: v2.SevError,
			Category: v2.CatSafety,
			Message:  "no confirmation field",
			ToolName: "delete_user",
			// No SourceFile — JSON intake produced this finding.
		},
	}

	var buf bytes.Buffer
	if err := WriteSARIF(&buf, findings); err != nil {
		t.Fatalf("WriteSARIF: %v", err)
	}

	var log map[string]any
	if err := json.Unmarshal(buf.Bytes(), &log); err != nil {
		t.Fatalf("output is not valid JSON: %v\n%s", err, buf.String())
	}

	if got := log["version"]; got != "2.1.0" {
		t.Errorf("version = %v, want 2.1.0", got)
	}
	runs, ok := log["runs"].([]any)
	if !ok || len(runs) != 1 {
		t.Fatalf("runs missing or wrong shape: %+v", log["runs"])
	}
	run := runs[0].(map[string]any)

	tool := run["tool"].(map[string]any)
	driver := tool["driver"].(map[string]any)
	if driver["name"] != "mcpcheck" {
		t.Errorf("driver.name = %v, want mcpcheck", driver["name"])
	}
	rules, _ := driver["rules"].([]any)
	if len(rules) == 0 {
		t.Errorf("driver.rules is empty; expected the registered rule list")
	}

	// The first registered rule should be alphabetically sorted; check stability.
	prev := ""
	for _, r := range rules {
		rule := r.(map[string]any)
		id, _ := rule["id"].(string)
		if id == "" {
			t.Errorf("rule with empty id")
		}
		if prev != "" && id < prev {
			t.Errorf("rules not sorted: %q before %q", prev, id)
		}
		prev = id
	}

	results := run["results"].([]any)
	if len(results) != 2 {
		t.Fatalf("results length = %d, want 2", len(results))
	}

	// First finding has source coordinates → a location should be present.
	r0 := results[0].(map[string]any)
	if r0["ruleId"] != "tool-name-not-snake-case" {
		t.Errorf("results[0].ruleId = %v", r0["ruleId"])
	}
	if r0["level"] != "warning" {
		t.Errorf("results[0].level = %v, want warning", r0["level"])
	}
	locs0, _ := r0["locations"].([]any)
	if len(locs0) != 1 {
		t.Fatalf("results[0].locations = %+v", r0["locations"])
	}
	pl := locs0[0].(map[string]any)["physicalLocation"].(map[string]any)
	if pl["artifactLocation"].(map[string]any)["uri"] != "server.py" {
		t.Errorf("artifactLocation.uri = %v", pl["artifactLocation"])
	}
	region := pl["region"].(map[string]any)
	if int(region["startLine"].(float64)) != 42 {
		t.Errorf("startLine = %v, want 42", region["startLine"])
	}

	// Second finding has no source — locations should be omitted (nil/empty).
	r1 := results[1].(map[string]any)
	if r1["level"] != "error" {
		t.Errorf("results[1].level = %v, want error", r1["level"])
	}
	if locs, ok := r1["locations"]; ok && locs != nil {
		// `omitempty` should drop the field entirely when locations is nil.
		// Allow an empty slice too in case downstream tooling needs the key.
		if arr, ok := locs.([]any); !ok || len(arr) != 0 {
			t.Errorf("results[1].locations should be absent or empty for findings without source coords; got %+v", locs)
		}
	}
}

func TestSeverityToSARIFLevel(t *testing.T) {
	cases := []struct {
		in   v2.Severity
		want string
	}{
		{v2.SevError, "error"},
		{v2.SevWarning, "warning"},
		{v2.SevInfo, "note"},
		{v2.Severity("unknown"), "none"},
	}
	for _, tc := range cases {
		t.Run(string(tc.in), func(t *testing.T) {
			if got := severityToSARIFLevel(tc.in); got != tc.want {
				t.Fatalf("severityToSARIFLevel(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

func TestWriteSARIF_NoFindings(t *testing.T) {
	var buf bytes.Buffer
	if err := WriteSARIF(&buf, nil); err != nil {
		t.Fatalf("WriteSARIF: %v", err)
	}
	if !strings.Contains(buf.String(), `"results"`) {
		t.Errorf("expected results field even on empty input; got: %s", buf.String())
	}
	var log map[string]any
	if err := json.Unmarshal(buf.Bytes(), &log); err != nil {
		t.Fatalf("output is not valid JSON: %v", err)
	}
}
