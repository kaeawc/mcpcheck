package rules

import "testing"

func TestDestructivePrefix(t *testing.T) {
	cases := []struct {
		name string
		want string
	}{
		{"delete_user", "delete"},
		{"send_email", "send"},
		{"transfer_funds", "transfer"},
		{"delete", "delete"},
		{"DELETE_user", "delete"}, // case-insensitive
		{"deletion", ""},          // not destructive: not exactly "delete" and not "delete_*"
		{"undelete_record", ""},
		{"fetch_user", ""},
		{"", ""},
		{"send", "send"},
		{"sender", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := destructivePrefix(tc.name); got != tc.want {
				t.Fatalf("destructivePrefix(%q) = %q, want %q", tc.name, got, tc.want)
			}
		})
	}
}

func TestHasConfirmationField(t *testing.T) {
	cases := []struct {
		name   string
		schema map[string]any
		want   bool
	}{
		{
			name:   "nil schema",
			schema: nil,
			want:   false,
		},
		{
			name:   "no properties",
			schema: map[string]any{"type": "object"},
			want:   false,
		},
		{
			name: "properties wrong type",
			schema: map[string]any{
				"type":       "object",
				"properties": "not a map",
			},
			want: false,
		},
		{
			name: "no confirmation field",
			schema: map[string]any{
				"properties": map[string]any{
					"id":   map[string]any{"type": "string"},
					"name": map[string]any{"type": "string"},
				},
			},
			want: false,
		},
		{
			name: "explicit confirm",
			schema: map[string]any{
				"properties": map[string]any{
					"id":      map[string]any{"type": "string"},
					"confirm": map[string]any{"type": "boolean"},
				},
			},
			want: true,
		},
		{
			name: "requires_confirmation snake",
			schema: map[string]any{
				"properties": map[string]any{
					"requires_confirmation": map[string]any{"type": "boolean"},
				},
			},
			want: true,
		},
		{
			name: "requiresConfirmation camel-stripped",
			schema: map[string]any{
				"properties": map[string]any{
					"requiresConfirmation": map[string]any{"type": "boolean"},
				},
			},
			want: true,
		},
		{
			name: "dry_run accepted",
			schema: map[string]any{
				"properties": map[string]any{
					"dry_run": map[string]any{"type": "boolean"},
				},
			},
			want: true,
		},
		{
			name: "force accepted",
			schema: map[string]any{
				"properties": map[string]any{
					"force": map[string]any{"type": "boolean"},
				},
			},
			want: true,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := hasConfirmationField(tc.schema); got != tc.want {
				t.Fatalf("hasConfirmationField = %v, want %v", got, tc.want)
			}
		})
	}
}
