// Package scanner loads MCP tool definitions from various sources and
// produces a normalized mcpmodel.ToolSet for the dispatcher.
//
// The JSON intake reads an MCP tools/list response (or a bare list of tool
// objects) from disk. It is the simplest intake and exists so rules can be
// developed and tested before the tree-sitter Python/TS intakes land.
package scanner

import (
	"encoding/json"
	"fmt"
	"os"

	"github.com/kaeawc/mcpcheck/internal/mcpmodel"
)

// LoadToolsJSON reads a JSON file containing either:
//   - {"tools": [ ... ]} (an MCP tools/list response shape)
//   - [ ... ] (a bare list of tool objects)
//
// Each tool object must have at least "name". "description" and "inputSchema"
// are optional. Unknown fields are tolerated.
func LoadToolsJSON(path string) (*mcpmodel.ToolSet, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", path, err)
	}

	tools, err := parseToolsJSON(raw)
	if err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}

	return &mcpmodel.ToolSet{Tools: tools, Source: path}, nil
}

func parseToolsJSON(raw []byte) ([]mcpmodel.Tool, error) {
	type toolJSON struct {
		Name        string         `json:"name"`
		Description string         `json:"description"`
		InputSchema map[string]any `json:"inputSchema"`
	}

	var envelope struct {
		Tools []toolJSON `json:"tools"`
	}
	if err := json.Unmarshal(raw, &envelope); err == nil && envelope.Tools != nil {
		return convertTools(envelope.Tools), nil
	}

	var bare []toolJSON
	if err := json.Unmarshal(raw, &bare); err == nil {
		return convertTools(bare), nil
	}

	return nil, fmt.Errorf("expected {\"tools\": [...]} or a [...] array of tool objects")
}

func convertTools[T any](in []T) []mcpmodel.Tool {
	out := make([]mcpmodel.Tool, 0, len(in))
	for _, t := range in {
		// Re-marshal/unmarshal through map[string]any so the converter is
		// generic over the local toolJSON type without leaking it.
		b, _ := json.Marshal(t)
		var m struct {
			Name        string         `json:"name"`
			Description string         `json:"description"`
			InputSchema map[string]any `json:"inputSchema"`
		}
		_ = json.Unmarshal(b, &m)
		out = append(out, mcpmodel.Tool{
			Name:        m.Name,
			Description: m.Description,
			InputSchema: m.InputSchema,
		})
	}
	return out
}
