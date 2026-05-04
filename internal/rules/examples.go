package rules

import (
	"encoding/json"
	"fmt"

	"github.com/santhosh-tekuri/jsonschema/v6"

	"github.com/kaeawc/mcpcheck/internal/v2"
)

func init() {
	v2.Register(&v2.Rule{
		ID:          "tool-example-fails-schema",
		Category:    v2.CatExamples,
		Severity:    v2.SevWarning,
		Description: "Examples declared on a tool's inputSchema must validate against that schema; stale examples mislead agents and reviewers.",
		Fix:         v2.FixNone,
		Implementation: func(ctx *v2.Context) {
			examples := extractExamples(ctx.Tool.InputSchema)
			if len(examples) == 0 {
				return
			}
			compiled, err := compileSchema(ctx.Tool.InputSchema)
			if err != nil {
				// A malformed schema is the schema-validity rule's
				// territory, not ours; don't double-report.
				return
			}
			for i, ex := range examples {
				if err := compiled.Validate(ex); err != nil {
					ctx.Report(fmt.Sprintf(
						"inputSchema.examples[%d] does not validate against the schema: %s",
						i, summarizeValidationError(err),
					))
				}
			}
		},
	})
}

// extractExamples returns the JSON Schema "examples" array from the input
// schema, or nil if absent / malformed.
func extractExamples(schema map[string]any) []any {
	if schema == nil {
		return nil
	}
	raw, ok := schema["examples"]
	if !ok {
		return nil
	}
	arr, ok := raw.([]any)
	if !ok {
		return nil
	}
	return arr
}

// compileSchema round-trips the user-supplied map through JSON to feed it
// into santhosh-tekuri/jsonschema, which expects either a string URL or a
// parsed any-shape value. We don't need a network resolver — the schema
// is fully inline.
func compileSchema(schema map[string]any) (*jsonschema.Schema, error) {
	raw, err := json.Marshal(schema)
	if err != nil {
		return nil, fmt.Errorf("marshal schema: %w", err)
	}
	var doc any
	if err := json.Unmarshal(raw, &doc); err != nil {
		return nil, fmt.Errorf("re-parse schema: %w", err)
	}
	c := jsonschema.NewCompiler()
	const url = "mem://tool/inputSchema"
	if err := c.AddResource(url, doc); err != nil {
		return nil, fmt.Errorf("add schema: %w", err)
	}
	compiled, err := c.Compile(url)
	if err != nil {
		return nil, fmt.Errorf("compile schema: %w", err)
	}
	return compiled, nil
}

// summarizeValidationError pulls a useful one-line message out of
// jsonschema's nested ValidationError tree. The library's top-level
// Error() is generic ("jsonschema validation failed with '<url>#'");
// the actionable detail lives in the deepest leaf cause. Falls back to
// the first line of the full message for any non-ValidationError.
func summarizeValidationError(err error) string {
	if ve, ok := err.(*jsonschema.ValidationError); ok {
		leaf := deepestCause(ve)
		return leaf.Error()
	}
	msg := err.Error()
	for i, r := range msg {
		if r == '\n' {
			return msg[:i]
		}
	}
	return msg
}

func deepestCause(ve *jsonschema.ValidationError) *jsonschema.ValidationError {
	if len(ve.Causes) == 0 {
		return ve
	}
	return deepestCause(ve.Causes[0])
}
