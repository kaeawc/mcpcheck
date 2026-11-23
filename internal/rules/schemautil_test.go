package rules

import (
	"regexp"
	"testing"
)

func TestHasStringValueConstraint(t *testing.T) {
	cases := []struct {
		name string
		in   map[string]any
		want bool
	}{
		{
			name: "enum",
			in:   map[string]any{"type": "string", "enum": []any{"a", "b"}},
			want: true,
		},
		{
			name: "pattern",
			in:   map[string]any{"type": "string", "pattern": "^/safe/"},
			want: true,
		},
		{
			name: "const",
			in:   map[string]any{"type": "string", "const": "fixed"},
			want: true,
		},
		{
			name: "format only",
			in:   map[string]any{"type": "string", "format": "uri"},
			want: false, // format validates syntax, not value space
		},
		{
			name: "bare type",
			in:   map[string]any{"type": "string"},
			want: false,
		},
		{
			name: "empty",
			in:   map[string]any{},
			want: false,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := hasStringValueConstraint(tc.in); got != tc.want {
				t.Fatalf("hasStringValueConstraint(%v) = %v, want %v", tc.in, got, tc.want)
			}
		})
	}
}

func TestDocumentsConfinement(t *testing.T) {
	cases := []struct {
		name      string
		re        *regexp.Regexp
		tool      string
		prop      string
		confining bool
	}{
		{"filesystem server", pathConfinementRe, "Read a file. Only works within allowed directories.", "", true},
		{"both within allowed", pathConfinementRe, "Both source and destination must be within allowed directories.", "", true},
		{"sandbox", pathConfinementRe, "Delete a file inside the sandbox directory.", "", true},
		{"restricted to workspace", pathConfinementRe, "", "Path, restricted to the current workspace.", true},
		{"limited count", pathConfinementRe, "List files, limited to 100 results.", "", false},
		{"plain path", pathConfinementRe, "Read the file at path.", "Absolute path to the file.", false},
		{"allowed hosts", urlConfinementRe, "Fetch a page from one of the allowed hosts.", "", true},
		{"restricted to domains", urlConfinementRe, "", "URL; restricted to configured domains.", true},
		{"rate limited", urlConfinementRe, "Fetch a URL. Requests are limited to 10 per minute.", "", false},
		{"path phrase on url", urlConfinementRe, "Only works within allowed directories.", "", false},
	}
	for _, tc := range cases {
		prop := map[string]any{"type": "string"}
		if tc.prop != "" {
			prop["description"] = tc.prop
		}
		if got := documentsConfinement(tc.re, tc.tool, prop); got != tc.confining {
			t.Errorf("%s: documentsConfinement = %v, want %v", tc.name, got, tc.confining)
		}
	}
}
