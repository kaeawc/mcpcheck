package scanner

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/kaeawc/mcpcheck/internal/mcpmodel"
)

// sourceIntake loads tools from a file or directory for one language.
type sourceIntake struct {
	language string
	matches  func(path string) bool
	load     func(path string) (*mcpmodel.ToolSet, error)
}

// sourceIntakes lists every language intake. A directory is scanned by
// all of them and the results merged, so polyglot repositories work.
var sourceIntakes = []sourceIntake{
	{"python", isPythonSourceFile, LoadPython},
	{"typescript", isTypeScriptSourceFile, LoadTypeScript},
	{"go", isGoSourceFile, LoadGo},
	{"rust", isRustSourceFile, LoadRust},
}

// Load chooses an intake based on path. Directories are scanned by every
// source intake; single files are routed by extension (`.json` is a
// tools/list response or a session recorded with --save).
func Load(path string) (*mcpmodel.ToolSet, error) {
	info, err := os.Stat(path)
	if err != nil {
		return nil, fmt.Errorf("stat %s: %w", path, err)
	}
	if info.IsDir() {
		return loadDir(path)
	}
	if strings.HasSuffix(path, ".json") {
		return LoadToolsJSON(path)
	}
	for _, in := range sourceIntakes {
		if in.matches(path) {
			return in.load(path)
		}
	}
	return nil, fmt.Errorf("unsupported intake for %s (want .json, .py, .ts/.tsx/.js/.mjs/.cjs, .go, .rs, or a directory)", path)
}

func loadDir(dir string) (*mcpmodel.ToolSet, error) {
	merged := &mcpmodel.ToolSet{Source: dir}
	for _, in := range sourceIntakes {
		set, err := in.load(dir)
		if err != nil {
			return nil, fmt.Errorf("%s intake: %w", in.language, err)
		}
		merged.Tools = append(merged.Tools, set.Tools...)
	}
	sort.SliceStable(merged.Tools, func(i, j int) bool {
		a, b := merged.Tools[i], merged.Tools[j]
		if a.SourceFile != b.SourceFile {
			return a.SourceFile < b.SourceFile
		}
		return a.SourceLine < b.SourceLine
	})
	return merged, nil
}

// walkSources calls visit for every file under root accepted by match,
// skipping dependency, build-output, and VCS directories and test files.
// root may itself be a file.
func walkSources(root string, match func(string) bool, visit func(path string) error) error {
	return filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if path != root && skipDir(d.Name()) {
				return filepath.SkipDir
			}
			return nil
		}
		if !match(path) || (path != root && isTestFile(path)) {
			return nil
		}
		return visit(path)
	})
}

// skipDir reports directories that never hold first-party server source.
func skipDir(name string) bool {
	switch name {
	case "node_modules", "dist", "build", "out", "coverage", ".next", ".turbo",
		"__pycache__", ".venv", "venv", "env", ".tox", ".mypy_cache", ".pytest_cache", "site-packages",
		"target", "vendor", "testdata", ".git", ".hg", ".svn":
		return true
	}
	return false
}

// isTestFile reports test sources, whose tool registrations are fixtures
// rather than what the server ships.
func isTestFile(path string) bool {
	base := filepath.Base(path)
	switch {
	case strings.HasSuffix(base, "_test.go"):
		return true
	case strings.HasPrefix(base, "test_") && strings.HasSuffix(base, ".py"), strings.HasSuffix(base, "_test.py"):
		return true
	case strings.Contains(base, ".test.") || strings.Contains(base, ".spec."):
		return true
	}
	return false
}

// SupportedPatterns lists the tool-registration shapes the source
// intakes recognize, for feedback when a scan finds nothing.
func SupportedPatterns() []string {
	return []string{
		"Python:     @mcp.tool / @mcp.tool(name=..., description=...) on a def (official SDK FastMCP, fastmcp 2.x)",
		"Python:     types.Tool(name=..., description=..., inputSchema={...}) (official SDK low-level server)",
		"TypeScript: server.registerTool(\"name\", { description, inputSchema }, handler) and server.tool(\"name\", \"description\", ...)",
		"Go:         mcp.AddTool(server, &mcp.Tool{Name: ..., Description: ...}, handler) (official go-sdk)",
		"Go:         mcp.NewTool(\"name\", mcp.WithDescription(...), ...) (mark3labs/mcp-go)",
		"Rust:       #[tool(description = \"...\")] fn inside a #[tool_router] impl (rmcp)",
		"Any:        object literals with name + inputSchema, e.g. a hand-rolled tools/list result",
	}
}
