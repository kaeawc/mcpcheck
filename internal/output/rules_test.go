package output

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/kaeawc/mcpcheck/internal/v2"
)

func sampleRules() []*v2.Rule {
	return []*v2.Rule{
		{
			ID:          "z-rule",
			Category:    v2.CatSpec,
			Severity:    v2.SevWarning,
			Description: "z description",
			Fix:         v2.FixCosmetic,
			Implementation: func(*v2.Context) {},
		},
		{
			ID:          "a-rule",
			Category:    v2.CatSafety,
			Severity:    v2.SevError,
			Description: "a description",
			Fix:         v2.FixNone,
			Implementation: func(*v2.Context) {},
		},
		{
			ID:          "m-rule",
			Category:    v2.CatSpec,
			Severity:    v2.SevError,
			Description: "m description",
			Fix:         v2.FixNone,
			ProjectImplementation: func(*v2.ProjectContext) {},
		},
	}
}

func TestWriteRulesText_SortedAndContainsFields(t *testing.T) {
	var buf bytes.Buffer
	if err := WriteRulesText(&buf, sampleRules()); err != nil {
		t.Fatalf("WriteRulesText: %v", err)
	}
	out := buf.String()
	a := strings.Index(out, "a-rule")
	m := strings.Index(out, "m-rule")
	z := strings.Index(out, "z-rule")
	if a < 0 || m < 0 || z < 0 {
		t.Fatalf("missing rule ids in output:\n%s", out)
	}
	if !(a < m && m < z) {
		t.Fatalf("rules not sorted by id: a=%d m=%d z=%d\n%s", a, m, z, out)
	}
	for _, want := range []string{"a-rule", "error", "safety-contract", "z-rule", "cosmetic"} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q\n%s", want, out)
		}
	}
}

func TestWriteRulesJSON_Shape(t *testing.T) {
	var buf bytes.Buffer
	if err := WriteRulesJSON(&buf, sampleRules()); err != nil {
		t.Fatalf("WriteRulesJSON: %v", err)
	}
	var arr []map[string]any
	if err := json.Unmarshal(buf.Bytes(), &arr); err != nil {
		t.Fatalf("invalid JSON: %v\n%s", err, buf.String())
	}
	if len(arr) != 3 {
		t.Fatalf("got %d rules, want 3", len(arr))
	}
	// Sorted: a, m, z.
	wantOrder := []string{"a-rule", "m-rule", "z-rule"}
	for i, want := range wantOrder {
		if arr[i]["id"] != want {
			t.Errorf("arr[%d].id = %v, want %q", i, arr[i]["id"], want)
		}
	}
	// m-rule has projectScope=true; a-rule and z-rule don't.
	if arr[0]["projectScope"] != nil && arr[0]["projectScope"] != false {
		t.Errorf("a-rule.projectScope should be omitted/false; got %v", arr[0]["projectScope"])
	}
	if arr[1]["projectScope"] != true {
		t.Errorf("m-rule.projectScope = %v, want true", arr[1]["projectScope"])
	}
}
