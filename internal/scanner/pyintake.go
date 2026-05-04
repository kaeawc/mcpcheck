// Python intake — regex-based extractor for FastMCP-style tool
// declarations. This is intentionally a stopgap: tree-sitter Python
// parsing is the eventual home for this code, but rules need a Python
// intake before tree-sitter is wired in, and the regex shape is precise
// enough to bootstrap the rule pipeline against real Python servers.
//
// Recognized patterns:
//
//	@mcp.tool()                      # @<receiver>.tool with optional args
//	def fetch_user(id: str) -> dict:
//	    """Fetch a user by id."""    # docstring used as description
//	    ...
//
//	@server.tool(name="custom_name") # explicit name= kwarg overrides def name
//	def _internal(...):
//	    """Tool with a custom name."""
//	    ...
//
// Limitations to be removed when tree-sitter lands:
//   - Decorator + `def` must be on consecutive non-blank lines.
//   - Description is just the first line of the docstring.
//   - InputSchema is left nil; argument introspection happens later.
//   - The older `@server.list_tools()` returning a list of Tool(...) is
//     intentionally not handled here; that pattern needs proper AST work.
package scanner

import (
	"bufio"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/kaeawc/mcpcheck/internal/mcpmodel"
)

// pyToolDecoratorRe matches lines like `@mcp.tool()`, `@server.tool`,
// `@app.tool(name="x", description="y")`. The receiver is captured in
// group 1, the optional argument list in group 2.
var pyToolDecoratorRe = regexp.MustCompile(
	`^\s*@\s*([A-Za-z_][A-Za-z0-9_]*)\.tool\b(\s*\(([^)]*)\))?\s*$`,
)

// pyDefRe matches `def <name>(...)` and `async def <name>(...)`. The
// function name is captured in group 1.
var pyDefRe = regexp.MustCompile(
	`^\s*(?:async\s+)?def\s+([A-Za-z_][A-Za-z0-9_]*)\s*\(`,
)

// pyKwargStringRe extracts a `key="value"` (or single-quoted) pair from
// the decorator argument list. Used for `name=...` and `description=...`.
var pyKwargStringRe = regexp.MustCompile(
	`(\w+)\s*=\s*("([^"]*)"|'([^']*)')`,
)

// pyDocstringStartRe matches the start of a triple-quoted docstring
// directly inside a function body. Quote style captured in group 1, any
// content on the opening line captured in group 2.
var pyDocstringStartRe = regexp.MustCompile(
	`^\s*(?:[ru]?(?:b)?|b?(?:r|u)?)?("""|''')(.*)$`,
)

// LoadPythonFile parses a single .py file and returns a ToolSet.
func LoadPythonFile(path string) (*mcpmodel.ToolSet, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open %s: %w", path, err)
	}
	defer f.Close()

	tools, err := scanPythonReader(path, f)
	if err != nil {
		return nil, fmt.Errorf("scan %s: %w", path, err)
	}
	return &mcpmodel.ToolSet{Tools: tools, Source: path}, nil
}

// LoadPythonDir walks dir recursively and parses every .py file. Files
// inside common throwaway directories (`__pycache__`, `.venv`, `venv`,
// `node_modules`, `.git`) are skipped.
func LoadPythonDir(dir string) (*mcpmodel.ToolSet, error) {
	var all []mcpmodel.Tool
	err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if d.IsDir() {
			if shouldSkipDir(d.Name()) {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".py") {
			return nil
		}
		set, err := LoadPythonFile(path)
		if err != nil {
			return err
		}
		all = append(all, set.Tools...)
		return nil
	})
	if err != nil {
		return nil, err
	}
	return &mcpmodel.ToolSet{Tools: all, Source: dir}, nil
}

func shouldSkipDir(name string) bool {
	switch name {
	case "__pycache__", ".venv", "venv", "env", "node_modules", ".git", ".tox", "build", "dist":
		return true
	}
	return false
}

// scanPythonReader reads source from r and returns the tools it found.
// Exposed at package level (lowercase) so tests can drive it from a
// strings.Reader without writing fixture files.
func scanPythonReader(sourcePath string, r interface{ Read([]byte) (int, error) }) ([]mcpmodel.Tool, error) {
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 1024*1024), 1024*1024)

	var lines []string
	for scanner.Scan() {
		lines = append(lines, scanner.Text())
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}

	var tools []mcpmodel.Tool

	for i := 0; i < len(lines); i++ {
		decoratorMatch := pyToolDecoratorRe.FindStringSubmatch(lines[i])
		if decoratorMatch == nil {
			continue
		}
		decoratorArgs := decoratorMatch[3]

		defLine, defLineIdx := nextDefLine(lines, i+1)
		if defLineIdx < 0 {
			continue
		}
		defMatch := pyDefRe.FindStringSubmatch(defLine)
		if defMatch == nil {
			continue
		}
		funcName := defMatch[1]

		name := funcName
		var description string
		for _, kv := range pyKwargStringRe.FindAllStringSubmatch(decoratorArgs, -1) {
			value := kv[3]
			if value == "" {
				value = kv[4]
			}
			switch kv[1] {
			case "name":
				name = value
			case "description":
				description = value
			}
		}
		if description == "" {
			description = extractDocstringFirstLine(lines, defLineIdx+1)
		}

		tools = append(tools, mcpmodel.Tool{
			Name:        name,
			Description: description,
			SourceFile:  sourcePath,
			SourceLine:  i + 1, // decorator line, 1-indexed
		})
	}

	return tools, nil
}

// nextDefLine walks forward from `start`, skipping blank lines, comments,
// and additional decorator lines, and returns the first line that should
// contain a `def`. This lets us accept stacked decorators like
//
//	@mcp.tool()
//	@cached_property
//	def fetch_user(...):
func nextDefLine(lines []string, start int) (string, int) {
	for i := start; i < len(lines); i++ {
		trimmed := strings.TrimSpace(lines[i])
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}
		if strings.HasPrefix(trimmed, "@") {
			continue
		}
		return lines[i], i
	}
	return "", -1
}

// extractDocstringFirstLine looks at the first statement line of a
// function body (line index `start`) and returns the first line of a
// triple-quoted docstring if present, else "".
func extractDocstringFirstLine(lines []string, start int) string {
	if start >= len(lines) {
		return ""
	}
	first := lines[start]
	m := pyDocstringStartRe.FindStringSubmatch(first)
	if m == nil {
		return ""
	}
	quote := m[1]
	rest := m[2]

	// Single-line docstring: """foo bar"""
	if idx := strings.Index(rest, quote); idx >= 0 {
		return strings.TrimSpace(rest[:idx])
	}

	// First line of body of multi-line docstring is in `rest`. If `rest`
	// is empty the convention is that the description starts on the next
	// line — return that one.
	if strings.TrimSpace(rest) != "" {
		return strings.TrimSpace(rest)
	}
	for j := start + 1; j < len(lines); j++ {
		trimmed := strings.TrimSpace(lines[j])
		if trimmed == "" {
			continue
		}
		if strings.HasSuffix(trimmed, quote) {
			return strings.TrimSpace(strings.TrimSuffix(trimmed, quote))
		}
		return trimmed
	}
	return ""
}
