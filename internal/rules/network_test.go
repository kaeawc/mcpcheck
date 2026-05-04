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
