package main

import (
	"reflect"
	"strings"
	"testing"

	v2 "github.com/kaeawc/mcpcheck/internal/v2"
)

func TestSplitCommand(t *testing.T) {
	cases := []struct {
		in   string
		want []string
	}{
		{"node dist/server.js", []string{"node", "dist/server.js"}},
		{"  uv   run  server.py ", []string{"uv", "run", "server.py"}},
		{`python -c 'print("hi there")'`, []string{"python", "-c", `print("hi there")`}},
		{`npx -y "@scope/my server"`, []string{"npx", "-y", "@scope/my server"}},
		{`cmd a\ b "x\"y" ''`, []string{"cmd", "a b", `x"y`, ""}},
	}
	for _, tc := range cases {
		got, err := splitCommand(tc.in)
		if err != nil || !reflect.DeepEqual(got, tc.want) {
			t.Errorf("splitCommand(%q) = %q, %v; want %q", tc.in, got, err, tc.want)
		}
	}
	for _, bad := range []string{"", "   ", `python -c 'oops`, `trailing\`} {
		if _, err := splitCommand(bad); err == nil {
			t.Errorf("splitCommand(%q) succeeded, want error", bad)
		}
	}
}

func TestHeaderFlag(t *testing.T) {
	var h headerFlag
	for _, v := range []string{"Authorization: Bearer abc:def", "X-Api-Key:k1", "x-api-key: k2"} {
		if err := h.Set(v); err != nil {
			t.Fatal(err)
		}
	}
	if got := h["Authorization"]; len(got) != 1 || got[0] != "Bearer abc:def" {
		t.Errorf("Authorization = %q", got)
	}
	if got := h["X-Api-Key"]; !reflect.DeepEqual(got, []string{"k1", "k2"}) {
		t.Errorf("X-Api-Key = %q", got)
	}
	if err := h.Set("no-colon"); err == nil {
		t.Error("expected error for header without colon")
	}
}

func TestParseFlagsValidation(t *testing.T) {
	cases := map[string][]string{
		"mutually exclusive": {"--live", "x", "--url", "http://x"},
		"nothing to check":   {},
		"--save records":     {"--save", "out.json", "tools.json"},
		"at most one path":   {"a", "b"},
	}
	for want, args := range cases {
		if _, err := parseFlags(args); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("parseFlags(%q) err = %v, want %q", args, err, want)
		}
	}
	c, err := parseFlags([]string{"--url", "http://localhost:3000/mcp", "--header", "Authorization: Bearer t", "src/"})
	if err != nil || c.path != "src/" || c.headers["Authorization"][0] != "Bearer t" {
		t.Errorf("parseFlags happy path: %+v, %v", c, err)
	}
}

func TestDedupeKeepsSourceLocatedFinding(t *testing.T) {
	findings := []v2.Finding{
		{RuleID: "r", ToolName: "t", Message: "m", SourceFile: "server.ts", SourceLine: 3},
		{RuleID: "r", ToolName: "t", Message: "m"},
		{RuleID: "r", ToolName: "u", Message: "m"},
		{RuleID: "s", Message: "server-level"},
		{RuleID: "s", Message: "server-level"},
	}
	got := dedupe(findings)
	if len(got) != 4 || got[0].SourceFile != "server.ts" || got[1].ToolName != "u" {
		t.Errorf("dedupe = %+v", got)
	}
}
