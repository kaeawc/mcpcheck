// Command mcpcheck is the CLI frontend for the analyzer.
//
// Usage:
//
//	mcpcheck [--format text|json] <path>
//
// `path` may be a tools.json (MCP tools/list response or bare list), a
// single .py file, or a directory of Python sources. TypeScript intake
// and a tree-sitter-backed Python intake will land as future PRs.
package main

import (
	"flag"
	"fmt"
	"os"

	_ "github.com/kaeawc/mcpcheck/internal/rules"
	"github.com/kaeawc/mcpcheck/internal/output"
	"github.com/kaeawc/mcpcheck/internal/scanner"
	"github.com/kaeawc/mcpcheck/internal/v2"
)

func main() {
	format := flag.String("format", "text", "output format: text|json")
	flag.Usage = func() {
		fmt.Fprintf(os.Stderr, "usage: %s [--format text|json] <path>\n", os.Args[0])
		fmt.Fprintf(os.Stderr, "  path: .json (tools/list response), .py file, or directory of Python sources\n")
	}
	flag.Parse()

	if flag.NArg() != 1 {
		flag.Usage()
		os.Exit(2)
	}

	if err := run(flag.Arg(0), *format); err != nil {
		fmt.Fprintln(os.Stderr, "mcpcheck:", err)
		os.Exit(1)
	}
}

func run(path, format string) error {
	set, err := scanner.Load(path)
	if err != nil {
		return err
	}

	rules := v2.All()
	var findings []v2.Finding
	for i := range set.Tools {
		findings = append(findings, v2.Run(rules, &set.Tools[i])...)
	}
	findings = append(findings, v2.RunProject(rules, set)...)

	switch format {
	case "text":
		if err := output.WriteText(os.Stdout, findings); err != nil {
			return err
		}
	case "json":
		if err := output.WriteJSON(os.Stdout, findings); err != nil {
			return err
		}
	default:
		return fmt.Errorf("unknown --format %q (want text|json)", format)
	}

	// Exit 1 if any error-severity finding fired; otherwise 0.
	for _, f := range findings {
		if f.Severity == v2.SevError {
			os.Exit(1)
		}
	}
	return nil
}
