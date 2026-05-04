package rules

import "testing"

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
