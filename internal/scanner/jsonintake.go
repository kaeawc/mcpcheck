// Package scanner loads MCP tool definitions from various sources and
// produces a normalized mcpmodel.ToolSet for the dispatcher.
//
// The JSON intake reads an MCP tools/list response (or a bare list of tool
// objects) from disk. Files written by `mcpcheck --save` also carry a
// "server" object describing the live session they were recorded from, so
// a recorded session can be re-checked offline.
package scanner

import (
	"encoding/json"
	"fmt"
	"io"
	"os"

	"github.com/kaeawc/mcpcheck/internal/mcpmodel"
)

// LoadToolsJSON reads a JSON file containing either:
//   - {"tools": [ ... ], "server": { ... }?} (a tools/list result, optionally
//     with a recorded session)
//   - [ ... ] (a bare list of tool objects)
//
// Tool objects are kept as advertised (mcpmodel.Tool.Raw), so rules treat
// them exactly like tools fetched from a live server.
func LoadToolsJSON(path string) (*mcpmodel.ToolSet, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	set, err := parseToolsJSON(raw)
	if err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	set.Source = path
	for i := range set.Tools {
		set.Tools[i].SourceFile = path
	}
	return set, nil
}

func parseToolsJSON(raw []byte) (*mcpmodel.ToolSet, error) {
	var envelope struct {
		Tools  []map[string]any `json:"tools"`
		Server *mcpmodel.Server `json:"server"`
	}
	if err := json.Unmarshal(raw, &envelope); err == nil && envelope.Tools != nil {
		return &mcpmodel.ToolSet{Tools: convertTools(envelope.Tools), Server: envelope.Server}, nil
	}
	var bare []map[string]any
	if err := json.Unmarshal(raw, &bare); err == nil {
		return &mcpmodel.ToolSet{Tools: convertTools(bare)}, nil
	}
	return nil, fmt.Errorf("expected {\"tools\": [...]} or a [...] array of tool objects")
}

func convertTools(in []map[string]any) []mcpmodel.Tool {
	out := make([]mcpmodel.Tool, 0, len(in))
	for _, m := range in {
		out = append(out, mcpmodel.ToolFromJSON(m))
	}
	return out
}

// WriteSession writes set in the format LoadToolsJSON reads back: the
// tools exactly as advertised plus the recorded server session.
func WriteSession(w io.Writer, set *mcpmodel.ToolSet) error {
	tools := make([]map[string]any, 0, len(set.Tools))
	for _, t := range set.Tools {
		tools = append(tools, t.Raw)
	}
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(struct {
		Tools  []map[string]any `json:"tools"`
		Server *mcpmodel.Server `json:"server,omitempty"`
	}{tools, set.Server})
}
