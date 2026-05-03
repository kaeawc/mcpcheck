package rules

import "testing"

func TestIsSnakeCase(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want bool
	}{
		{"plain lower", "fetch", true},
		{"snake", "fetch_user", true},
		{"multi snake", "send_pull_request_review", true},
		{"trailing digit", "fetch_v2", true},
		{"camel", "fetchUser", false},
		{"pascal", "FetchUser", false},
		{"kebab", "fetch-user", false},
		{"leading underscore", "_fetch", false},
		{"trailing underscore", "fetch_", false},
		{"leading digit", "1fetch", false},
		{"empty", "", false},
		{"space", "fetch user", false},
		{"unicode upper", "fetchÜser", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := isSnakeCase(tc.in); got != tc.want {
				t.Fatalf("isSnakeCase(%q) = %v, want %v", tc.in, got, tc.want)
			}
		})
	}
}
