package rules

import (
	"reflect"
	"sort"
	"testing"
)

func TestExtractRequired(t *testing.T) {
	cases := []struct {
		name string
		in   map[string]any
		want []string
	}{
		{"missing", map[string]any{"type": "object"}, nil},
		{"empty", map[string]any{"required": []any{}}, []string{}},
		{
			name: "string entries",
			in:   map[string]any{"required": []any{"a", "b"}},
			want: []string{"a", "b"},
		},
		{
			name: "skips non-strings",
			in:   map[string]any{"required": []any{"a", 42, true, "b"}},
			want: []string{"a", "b"},
		},
		{
			name: "wrong type",
			in:   map[string]any{"required": "a,b"},
			want: nil,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := extractRequired(tc.in)
			if tc.want == nil {
				if got != nil {
					t.Fatalf("got %v, want nil", got)
				}
				return
			}
			sort.Strings(got)
			sort.Strings(tc.want)
			if !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("got %v, want %v", got, tc.want)
			}
		})
	}
}

func TestPropertyNames(t *testing.T) {
	cases := []struct {
		name string
		in   map[string]any
		want []string
	}{
		{"missing", map[string]any{"type": "object"}, nil},
		{"empty", map[string]any{"properties": map[string]any{}}, []string{}},
		{
			name: "two",
			in: map[string]any{"properties": map[string]any{
				"id":   map[string]any{"type": "string"},
				"name": map[string]any{"type": "string"},
			}},
			want: []string{"id", "name"},
		},
		{
			name: "wrong type",
			in:   map[string]any{"properties": "not a map"},
			want: nil,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := propertyNames(tc.in)
			if tc.want == nil {
				if got != nil {
					t.Fatalf("got %v, want nil", got)
				}
				return
			}
			gotKeys := make([]string, 0, len(got))
			for k := range got {
				gotKeys = append(gotKeys, k)
			}
			sort.Strings(gotKeys)
			sort.Strings(tc.want)
			if !reflect.DeepEqual(gotKeys, tc.want) {
				t.Fatalf("got %v, want %v", gotKeys, tc.want)
			}
		})
	}
}
