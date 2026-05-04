// Command mcpcheck is the CLI frontend for the analyzer.
//
// Usage:
//
//	mcpcheck [--format text|json|sarif] <path>
//	mcpcheck --list-rules [--format text|json]
//
// `path` may be a tools.json (MCP tools/list response or bare list), a
// .py / .ts / .tsx / .js / .mjs / .cjs file, or a directory.
//
// `--list-rules` prints the registered rule set and exits without
// running an analysis. Useful for documentation generators and CI
// integrations that need to know what rules exist.
package main

import (
	"flag"
	"fmt"
	"os"

	"github.com/kaeawc/mcpcheck/internal/output"
	_ "github.com/kaeawc/mcpcheck/internal/rules"
	"github.com/kaeawc/mcpcheck/internal/scanner"
	"github.com/kaeawc/mcpcheck/internal/v2"
)

func main() {
	format := flag.String("format", "text", "output format: text|json|sarif")
	listRules := flag.Bool("list-rules", false, "list registered rules and exit")
	flag.Usage = usage
	flag.Parse()

	if *listRules {
		if err := writeRules(*format); err != nil {
			fmt.Fprintln(os.Stderr, "mcpcheck:", err)
			os.Exit(1)
		}
		return
	}

	if flag.NArg() != 1 {
		usage()
		os.Exit(2)
	}

	if err := run(flag.Arg(0), *format); err != nil {
		fmt.Fprintln(os.Stderr, "mcpcheck:", err)
		os.Exit(1)
	}
}

func usage() {
	fmt.Fprintf(os.Stderr, "usage: %s [--format text|json|sarif] <path>\n", os.Args[0])
	fmt.Fprintf(os.Stderr, "       %s --list-rules [--format text|json]\n", os.Args[0])
	fmt.Fprintf(os.Stderr, "  path: .json (tools/list response), .py / .ts / .tsx / .js / .mjs / .cjs file, or a directory\n")
}

func writeRules(format string) error {
	rules := v2.All()
	switch format {
	case "text":
		return output.WriteRulesText(os.Stdout, rules)
	case "json":
		return output.WriteRulesJSON(os.Stdout, rules)
	case "sarif":
		return fmt.Errorf("--format sarif is not supported with --list-rules (use json or text)")
	default:
		return fmt.Errorf("unknown --format %q (want text|json)", format)
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
	case "sarif":
		if err := output.WriteSARIF(os.Stdout, findings); err != nil {
			return err
		}
	default:
		return fmt.Errorf("unknown --format %q (want text|json|sarif)", format)
	}

	// Exit 1 if any error-severity finding fired; otherwise 0.
	for _, f := range findings {
		if f.Severity == v2.SevError {
			os.Exit(1)
		}
	}
	return nil
}
