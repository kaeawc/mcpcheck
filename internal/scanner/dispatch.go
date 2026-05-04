package scanner

import (
	"fmt"
	"os"
	"strings"

	"github.com/kaeawc/mcpcheck/internal/mcpmodel"
)

// Load chooses an intake based on path: a directory or .py file goes
// through the Python intake, .json goes through the JSON intake. Any
// other extension is rejected so misuse fails loudly.
func Load(path string) (*mcpmodel.ToolSet, error) {
	info, err := os.Stat(path)
	if err != nil {
		return nil, fmt.Errorf("stat %s: %w", path, err)
	}
	if info.IsDir() {
		return LoadPythonDir(path)
	}
	switch {
	case strings.HasSuffix(path, ".json"):
		return LoadToolsJSON(path)
	case strings.HasSuffix(path, ".py"):
		return LoadPythonFile(path)
	}
	return nil, fmt.Errorf("unsupported intake for %s (want .json, .py, or a directory of Python sources)", path)
}
