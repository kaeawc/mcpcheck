package rules

import "testing"

func TestEndsTruncated(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want bool
	}{
		{"clean sentence", "Fetch a user by id.", false},
		{"no terminator", "Fetch a user by id", false},
		{"three dots", "Fetch a user by...", true},
		{"unicode ellipsis", "Fetch a user by…", true},
		{"single dot", "Fetch a user.", false},
		{"two dots", "Fetch a user..", false},
		{"empty", "", false},
		{"trailing dots inside punctuation", "...and the rest", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := endsTruncated(tc.in); got != tc.want {
				t.Fatalf("endsTruncated(%q) = %v, want %v", tc.in, got, tc.want)
			}
		})
	}
}
