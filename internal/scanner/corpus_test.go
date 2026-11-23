package scanner

import (
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"testing"

	"github.com/kaeawc/mcpcheck/internal/mcpmodel"
)

// canonicalTool is what every reference server in tests/servers declares.
type canonicalTool struct {
	description string
	properties  []string
	required    []string
}

var canonicalTools = map[string]canonicalTool{
	"get_weather": {"Get the current weather for a city.", []string{"city", "units"}, []string{"city"}},
	"delete_file": {"Delete a file inside the sandbox directory.", []string{"confirm", "path"}, []string{"confirm", "path"}},
	"fetch_url":   {"Fetch a URL and return the response body.", []string{"url"}, []string{"url"}},
}

// assertCanonicalTools checks that tools are exactly the canonical three
// with matching descriptions, property names, and required lists.
func assertCanonicalTools(t *testing.T, tools []mcpmodel.Tool) {
	t.Helper()
	if len(tools) != len(canonicalTools) {
		names := make([]string, 0, len(tools))
		for _, tool := range tools {
			names = append(names, tool.Name)
		}
		t.Fatalf("got %d tools %v, want the 3 canonical tools", len(tools), names)
	}
	for _, tool := range tools {
		want, ok := canonicalTools[tool.Name]
		if !ok {
			t.Errorf("unexpected tool %q", tool.Name)
			continue
		}
		if tool.Description != want.description {
			t.Errorf("%s: description %q, want %q", tool.Name, tool.Description, want.description)
		}
		if tool.SourceFile == "" || tool.SourceLine <= 0 {
			t.Errorf("%s: missing source location %s:%d", tool.Name, tool.SourceFile, tool.SourceLine)
		}
		props, required := schemaShape(tool.InputSchema)
		if !reflect.DeepEqual(props, want.properties) || !reflect.DeepEqual(required, want.required) {
			t.Errorf("%s: properties %v required %v, want %v required %v", tool.Name, props, required, want.properties, want.required)
		}
	}
}

func schemaShape(schema map[string]any) (props, required []string) {
	p, _ := schema["properties"].(map[string]any)
	for name := range p {
		props = append(props, name)
	}
	r, _ := schema["required"].([]any)
	for _, name := range r {
		if s, ok := name.(string); ok {
			required = append(required, s)
		}
	}
	sort.Strings(props)
	sort.Strings(required)
	return props, required
}

func corpusDir(t *testing.T, id string) string {
	t.Helper()
	dir := filepath.Join("..", "..", "tests", "servers", id)
	if _, err := os.Stat(dir); err != nil {
		t.Skipf("corpus server %s not present", id)
	}
	return dir
}

// TestCorpusStaticIntake runs the directory dispatcher over each
// reference server's source and expects the canonical tools.
func TestCorpusStaticIntake(t *testing.T) {
	for _, id := range []string{
		"ts-sdk-tool", "ts-sdk-register", "ts-lowlevel", "ts-sdk-v2",
		"python-fastmcp", "python-fastmcp2", "python-mcpserver", "python-lowlevel", "python-handrolled",
	} {
		t.Run(id, func(t *testing.T) {
			set, err := Load(corpusDir(t, id))
			if err != nil {
				t.Fatal(err)
			}
			assertCanonicalTools(t, set.Tools)
		})
	}
}
