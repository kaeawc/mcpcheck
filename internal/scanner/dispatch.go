package scanner

import (
	"fmt"
	"os"
	"strings"

	"github.com/kaeawc/mcpcheck/internal/mcpmodel"
)

// Load chooses an intake based on path. For directories: if the directory
// has any .py file it's treated as Python; otherwise as TypeScript. (A
// future heuristic might inspect package.json or setup.py for stronger
// signal.) For single files: `.json` → JSON, `.py` → Python, `.ts` /
// `.tsx` / `.js` / `.mjs` / `.cjs` → TypeScript.
func Load(path string) (*mcpmodel.ToolSet, error) {
	info, err := os.Stat(path)
	if err != nil {
		return nil, fmt.Errorf("stat %s: %w", path, err)
	}
	if info.IsDir() {
		if hasFile, _ := dirHasExtension(path, ".py"); hasFile {
			return LoadPythonDir(path)
		}
		return LoadTypeScriptDir(path)
	}
	switch {
	case strings.HasSuffix(path, ".json"):
		return LoadToolsJSON(path)
	case strings.HasSuffix(path, ".py"):
		return LoadPythonFile(path)
	case isTypeScriptSourceFile(path):
		return LoadTypeScriptFile(path)
	}
	return nil, fmt.Errorf("unsupported intake for %s (want .json, .py, .ts/.tsx/.js/.mjs/.cjs, or a directory)", path)
}

// dirHasExtension returns true if any file directly under dir (not
// recursive) has the given extension.
func dirHasExtension(dir, ext string) (bool, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return false, err
	}
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ext) {
			return true, nil
		}
	}
	return false, nil
}
