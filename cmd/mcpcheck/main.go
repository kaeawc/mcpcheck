// Command mcpcheck is the CLI frontend for the analyzer.
//
// Usage:
//
//	mcpcheck [--format text|json] <tools.json>
//
// The MVP intake is a tools.json file (an MCP tools/list response or a bare
// list of tool objects). Tree-sitter Python and TypeScript intakes will land
// later as additional input forms.
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
		fmt.Fprintf(os.Stderr, "usage: %s [--format text|json] <tools.json>\n", os.Args[0])
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
	set, err := scanner.LoadToolsJSON(path)
	if err != nil {
		return err
	}

	rules := v2.All()
	var findings []v2.Finding
	for i := range set.Tools {
		findings = append(findings, v2.Run(rules, &set.Tools[i])...)
	}

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
