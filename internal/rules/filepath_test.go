package rules

import "testing"

func TestIsPathPropertyName(t *testing.T) {
	cases := []struct {
		in   string
		want bool
	}{
		{"path", true},
		{"PATH", true},
		{"file_path", true},
		{"filepath", true},
		{"filePath", true},
		{"file-path", true},
		{"filename", true},
		{"directory", true},
		{"dir", true},
		{"folder", true},
		{"source_path", true},
		{"target_path", true},
		{"output_path", true},
		{"input_path", true},
		{"dest_path", true},
		{"id", false},
		{"name", false},
		{"url", false}, // url-shaped — that's the network rule's territory
	}
	for _, tc := range cases {
		t.Run(tc.in, func(t *testing.T) {
			if got := isPathPropertyName(tc.in); got != tc.want {
				t.Fatalf("isPathPropertyName(%q) = %v, want %v", tc.in, got, tc.want)
			}
		})
	}
}
