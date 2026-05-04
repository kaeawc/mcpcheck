package rules

import "testing"

func TestExtractExamples(t *testing.T) {
	cases := []struct {
		name string
		in   map[string]any
		want int
	}{
		{"nil schema", nil, 0},
		{"no examples", map[string]any{"type": "object"}, 0},
		{
			name: "valid examples array",
			in: map[string]any{
				"type":     "object",
				"examples": []any{map[string]any{"id": "x"}, map[string]any{"id": "y"}},
			},
			want: 2,
		},
		{
			name: "examples not an array",
			in: map[string]any{
				"type":     "object",
				"examples": map[string]any{"id": "x"},
			},
			want: 0,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := len(extractExamples(tc.in)); got != tc.want {
				t.Fatalf("got %d, want %d", got, tc.want)
			}
		})
	}
}

func TestCompileAndValidate_StaleExample(t *testing.T) {
	schema := map[string]any{
		"type": "object",
		"properties": map[string]any{
			"id": map[string]any{"type": "string"},
		},
		"required": []any{"id"},
	}
	compiled, err := compileSchema(schema)
	if err != nil {
		t.Fatalf("compileSchema: %v", err)
	}

	// Valid example.
	if err := compiled.Validate(map[string]any{"id": "abc"}); err != nil {
		t.Errorf("expected valid example to pass, got: %v", err)
	}

	// Missing required field.
	if err := compiled.Validate(map[string]any{}); err == nil {
		t.Errorf("expected missing-required-field example to fail")
	}

	// Wrong type.
	if err := compiled.Validate(map[string]any{"id": 42}); err == nil {
		t.Errorf("expected wrong-type example to fail")
	}
}

func TestSummarizeValidationError(t *testing.T) {
	err := fakeErr("first line\nsecond line\nthird line")
	if got := summarizeValidationError(err); got != "first line" {
		t.Fatalf("got %q, want %q", got, "first line")
	}

	err = fakeErr("just one line")
	if got := summarizeValidationError(err); got != "just one line" {
		t.Fatalf("got %q, want %q", got, "just one line")
	}
}

type fakeErr string

func (e fakeErr) Error() string { return string(e) }
