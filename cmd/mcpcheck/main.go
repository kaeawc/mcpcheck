// Command mcpcheck is the CLI frontend for the analyzer.
//
// Usage:
//
//	mcpcheck [--format text|json|sarif] <path>
//	mcpcheck [--format text|json|sarif] --live "<command>"
//	mcpcheck --list-rules [--format text|json]
//
// `path` may be a tools.json (MCP tools/list response or bare list), a
// .py / .ts / .tsx / .js / .mjs / .cjs file, or a directory.
//
// `--live "<command>"` boots the given command as an MCP subprocess,
// runs the initialize / tools/list handshake over stdio, and
// validates whatever tools the server actually advertises at runtime.
// Catches dynamically-registered tools that static intakes miss.
//
// `--list-rules` prints the registered rule set and exits without
// running an analysis.
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/kaeawc/mcpcheck/internal/mcpmodel"
	"github.com/kaeawc/mcpcheck/internal/output"
	_ "github.com/kaeawc/mcpcheck/internal/rules"
	"github.com/kaeawc/mcpcheck/internal/scanner"
	"github.com/kaeawc/mcpcheck/internal/v2"
)

func main() {
	format := flag.String("format", "text", "output format: text|json|sarif")
	listRules := flag.Bool("list-rules", false, "list registered rules and exit")
	live := flag.String("live", "", "boot the given command as an MCP subprocess and run tools/list")
	liveTimeout := flag.Duration("live-timeout", 30*time.Second, "deadline for the live-mode handshake")
	flag.Usage = usage
	flag.Parse()

	if *listRules {
		if err := writeRules(*format); err != nil {
			fmt.Fprintln(os.Stderr, "mcpcheck:", err)
			os.Exit(1)
		}
		return
	}

	if *live != "" {
		if flag.NArg() != 0 {
			fmt.Fprintln(os.Stderr, "mcpcheck: --live and positional <path> are mutually exclusive")
			os.Exit(2)
		}
		ctx, cancel := context.WithTimeout(context.Background(), *liveTimeout)
		defer cancel()
		if err := runLive(ctx, *live, *format); err != nil {
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

// runLive splits live by whitespace into argv. This is intentionally
// simplistic — quoted arguments and shell features are not supported.
// Callers needing them can wrap in `sh -c "..."`.
func runLive(ctx context.Context, live, format string) error {
	parts := strings.Fields(live)
	if len(parts) == 0 {
		return fmt.Errorf("--live: empty command")
	}
	set, err := scanner.LoadLive(ctx, parts)
	if err != nil {
		return err
	}
	return analyze(set, format)
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
	return analyze(set, format)
}

// analyze runs every registered rule against the tool set, writes the
// findings in the requested format, and exits 1 if any error-severity
// finding fired. Shared by both the file/directory path and the
// live-mode subprocess paths.
func analyze(set *mcpmodel.ToolSet, format string) error {
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

	for _, f := range findings {
		if f.Severity == v2.SevError {
			os.Exit(1)
		}
	}
	return nil
}
