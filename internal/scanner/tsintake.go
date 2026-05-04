// TypeScript intake — regex-based extractor for the
// @modelcontextprotocol/sdk's most common tool-registration shape.
// Stopgap analogous to the Python regex intake; tree-sitter TS will land
// later.
//
// Recognized pattern:
//
//	server.tool("name", "description", { ...inputShape }, handler)
//	mcp.tool('name', 'description', schema, handler)
//	app.tool("name", "description", ...)
//
// The receiver name is captured (server/mcp/app/etc.) but unused beyond
// confirming the .tool method shape. The first string-literal argument
// is the tool name; the second string-literal argument is the
// description. InputSchema is left nil — schema extraction is the
// tree-sitter intake's job.
//
// Limitations:
//   - Single- and double-quoted strings only; template literals and
//     concatenations not handled.
//   - Embedded escapes inside the strings are passed through verbatim.
//   - server.setRequestHandler(ListToolsRequestSchema, ...) returning
//     a literal tools array is intentionally not handled — that pattern
//     needs an AST.
package scanner

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/kaeawc/mcpcheck/internal/mcpmodel"
)

// tsToolCallRe matches `<receiver>.tool("name", "description", ...)` over
// possibly-multiline whitespace between tokens. The (?s) flag makes `.`
// match newlines so the description argument can sit on a different line
// than the call open.
var tsToolCallRe = regexp.MustCompile(
	`(?s)\b([A-Za-z_$][A-Za-z0-9_$]*)\.tool\s*\(\s*["']([^"']*)["']\s*,\s*["']([^"']*)["']`,
)

// LoadTypeScriptFile parses a single .ts or .js file.
func LoadTypeScriptFile(path string) (*mcpmodel.ToolSet, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", path, err)
	}

	tools := scanTypeScript(path, string(raw))
	return &mcpmodel.ToolSet{Tools: tools, Source: path}, nil
}

// LoadTypeScriptDir walks dir and parses every .ts / .tsx / .js / .mjs
// file, skipping common throwaway directories (`node_modules`, `dist`,
// `build`, `.git`, `__tests__`-only folders are kept since they may
// contain example servers).
func LoadTypeScriptDir(dir string) (*mcpmodel.ToolSet, error) {
	var all []mcpmodel.Tool
	err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if d.IsDir() {
			if shouldSkipTSDir(d.Name()) {
				return filepath.SkipDir
			}
			return nil
		}
		if !isTypeScriptSourceFile(path) {
			return nil
		}
		set, err := LoadTypeScriptFile(path)
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

func shouldSkipTSDir(name string) bool {
	switch name {
	case "node_modules", "dist", "build", ".git", ".turbo", "coverage", ".next":
		return true
	}
	return false
}

func isTypeScriptSourceFile(path string) bool {
	switch filepath.Ext(path) {
	case ".ts", ".tsx", ".js", ".mjs", ".cjs":
		return true
	}
	return false
}

// scanTypeScript runs the extractor over an in-memory source string. The
// inline form is exposed so tests can drive it from a literal.
func scanTypeScript(sourcePath, src string) []mcpmodel.Tool {
	matches := tsToolCallRe.FindAllStringSubmatchIndex(src, -1)
	if len(matches) == 0 {
		return nil
	}
	tools := make([]mcpmodel.Tool, 0, len(matches))
	for _, idx := range matches {
		// idx layout: 0..1 whole match, 2..3 receiver, 4..5 name, 6..7 description
		name := src[idx[4]:idx[5]]
		description := src[idx[6]:idx[7]]
		startOffset := idx[0]
		tools = append(tools, mcpmodel.Tool{
			Name:        name,
			Description: description,
			SourceFile:  sourcePath,
			SourceLine:  byteOffsetToLine(src, startOffset),
		})
	}
	return tools
}

// byteOffsetToLine returns the 1-indexed line number containing the byte
// at offset.
func byteOffsetToLine(src string, offset int) int {
	if offset < 0 {
		return 0
	}
	if offset > len(src) {
		offset = len(src)
	}
	return strings.Count(src[:offset], "\n") + 1
}
