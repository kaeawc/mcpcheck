package rules

import "testing"

func TestIsURLPropertyName(t *testing.T) {
	cases := []struct {
		in   string
		want bool
	}{
		{"url", true},
		{"URL", true},
		{"uri", true},
		{"endpoint", true},
		{"webhook", true},
		{"webhook_url", true},
		{"webhookUrl", true},
		{"webhook-url", true},
		{"target_url", true},
		{"redirectUrl", true},
		{"request-url", true},
		{"name", false},
		{"id", false},
		{"path", false},
		{"hostname", false}, // intentionally not in the set; harder to constrain
	}
	for _, tc := range cases {
		t.Run(tc.in, func(t *testing.T) {
			if got := isURLPropertyName(tc.in); got != tc.want {
				t.Fatalf("isURLPropertyName(%q) = %v, want %v", tc.in, got, tc.want)
			}
		})
	}
}

func TestHasURLConstraint(t *testing.T) {
	cases := []struct {
		name string
		in   map[string]any
		want bool
	}{
		{
			name: "enum",
			in:   map[string]any{"type": "string", "enum": []any{"https://api.example.com"}},
			want: true,
		},
		{
			name: "pattern",
			in:   map[string]any{"type": "string", "pattern": "^https://api\\.example\\.com/"},
			want: true,
		},
		{
			name: "const",
			in:   map[string]any{"type": "string", "const": "https://api.example.com"},
			want: true,
		},
		{
			name: "format only",
			in:   map[string]any{"type": "string", "format": "uri"},
			want: false, // format validates syntax, not destination
		},
		{
			name: "bare type",
			in:   map[string]any{"type": "string"},
			want: false,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := hasURLConstraint(tc.in); got != tc.want {
				t.Fatalf("hasURLConstraint(%v) = %v, want %v", tc.in, got, tc.want)
			}
		})
	}
}
