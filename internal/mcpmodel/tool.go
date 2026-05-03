package mcpmodel

// Tool is the analyzer's normalized view of a single MCP tool advertisement.
//
// Different intake paths populate it differently: a JSON intake (an MCP
// tools/list response on disk) fills Name, Description, and InputSchema but
// leaves SourceFile/SourceLine empty; a tree-sitter intake of a Python or
// TS server can fill the source coordinates.
type Tool struct {
	Name        string
	Description string
	InputSchema map[string]any

	SourceFile string
	SourceLine int
}

// ToolSet is what an intake produces and what the dispatcher consumes.
type ToolSet struct {
	Tools  []Tool
	Source string
}
