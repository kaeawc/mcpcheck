package rules

import "testing"

func TestScanStringForPII(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		// Real-looking values fire.
		{"real email", "Send to alice@google.com", "real-looking email"},
		{"real ssn", "SSN: 555-12-9876", "real-looking ssn"},
		{"real phone non-555", "Call 415-867-5309", "real-looking phone"},
		{"real phone parens", "Call (415) 867-5309", "real-looking phone"},

		// Placeholders pass.
		{"example.com email", "user@example.com", ""},
		{"example.org email", "Contact support@example.org", ""},
		{"test.com email", "ALICE@test.com", ""},
		{"placeholder ssn 123", "123-45-6789", ""},
		{"placeholder ssn 000", "000-00-0000", ""},
		{"placeholder phone 555 area", "555-867-5309", ""},
		{"placeholder phone 555 exchange", "415-555-0100", ""},

		// Unrelated content.
		{"plain text", "Hello world.", ""},
		{"numbers but not patterns", "id: 12345", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := scanStringForPII(tc.in); got != tc.want {
				t.Fatalf("scanStringForPII(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

func TestScanForPII_Recursive(t *testing.T) {
	cases := []struct {
		name string
		in   any
		want string
	}{
		{
			name: "map with nested email",
			in:   map[string]any{"user": map[string]any{"email": "real@google.com"}},
			want: "real-looking email",
		},
		{
			name: "array element",
			in:   []any{"safe", "555-12-3456"},
			want: "real-looking ssn",
		},
		{
			name: "deep nesting placeholder",
			in:   map[string]any{"contacts": []any{map[string]any{"email": "user@example.com"}}},
			want: "",
		},
		{
			name: "non-string leaf",
			in:   map[string]any{"id": 42, "active": true},
			want: "",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := scanForPII(tc.in); got != tc.want {
				t.Fatalf("got %q, want %q", got, tc.want)
			}
		})
	}
}

func TestEmailIsPlaceholder(t *testing.T) {
	yes := []string{"a@example.com", "user@example.org", "x@example.net", "b@test.com"}
	no := []string{"a@gmail.com", "x@anthropic.com", "user@my-domain.io"}
	for _, e := range yes {
		if !emailIsPlaceholder(e) {
			t.Errorf("expected placeholder: %q", e)
		}
	}
	for _, e := range no {
		if emailIsPlaceholder(e) {
			t.Errorf("expected real: %q", e)
		}
	}
}

func TestPhoneIsPlaceholder(t *testing.T) {
	placeholder := []string{
		"555-867-5309",        // 555 area code, 7-digit-style
		"(555) 867-5309",      // 555 area code with parens
		"415-555-0100",        // 555 exchange
		"212-555-3000",        // 555 exchange, different area
		"555-1234",            // 7-digit local form
	}
	genuine := []string{
		"415-867-5309",
		"(415) 867-5309",
		"617-867-5309",
	}
	for _, p := range placeholder {
		if !phoneIsPlaceholder(p) {
			t.Errorf("expected placeholder: %q", p)
		}
	}
	for _, p := range genuine {
		if phoneIsPlaceholder(p) {
			t.Errorf("expected real: %q", p)
		}
	}
}
