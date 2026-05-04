// Command mcpcheck is the CLI frontend for the analyzer.
//
// Usage:
//
//	mcpcheck [--format text|json|sarif] <path>
//	mcpcheck [--format text|json|sarif] --live "<command>"
//	mcpcheck [--format text|json|sarif] --live "<command>" <path>
//	mcpcheck --list-rules [--format text|json]
//
// `path` may be a tools.json (MCP tools/list response or bare list), a
// .py / .ts / .tsx / .js / .mjs / .cjs file, or a directory.
//
// `--live "<command>"` boots the given command as an MCP subprocess,
// runs the initialize / tools/list handshake over stdio, and
// validates whatever tools the server actually advertises at runtime.
//
// Providing both `--live` and `<path>` runs both intakes plus a
// comparison phase that flags tools advertised by one but not the
// other (catches dynamic registration drift).
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

	hasLive := *live != ""
	hasPath := flag.NArg() == 1

	if !hasLive && !hasPath {
		usage()
		os.Exit(2)
	}
	if flag.NArg() > 1 {
		usage()
		os.Exit(2)
	}

	ctx, cancel := context.WithTimeout(context.Background(), *liveTimeout)
	defer cancel()

	var staticSet, liveSet *mcpmodel.ToolSet
	if hasPath {
		set, err := scanner.Load(flag.Arg(0))
		if err != nil {
			fmt.Fprintln(os.Stderr, "mcpcheck:", err)
			os.Exit(1)
		}
		staticSet = set
	}
	if hasLive {
		set, err := loadLiveCmd(ctx, *live)
		if err != nil {
			fmt.Fprintln(os.Stderr, "mcpcheck:", err)
			os.Exit(1)
		}
		liveSet = set
	}

	if err := analyzeAll(staticSet, liveSet, *format); err != nil {
		fmt.Fprintln(os.Stderr, "mcpcheck:", err)
		os.Exit(1)
	}
}

// loadLiveCmd splits live by whitespace and runs the live intake. The
// argv split is intentionally simplistic — quoted arguments and shell
// features are not supported. Callers needing them can wrap in
// `sh -c "..."`.
func loadLiveCmd(ctx context.Context, live string) (*mcpmodel.ToolSet, error) {
	parts := strings.Fields(live)
	if len(parts) == 0 {
		return nil, fmt.Errorf("--live: empty command")
	}
	return scanner.LoadLive(ctx, parts)
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

// analyzeAll runs every registered rule against whichever tool sets are
// non-nil and emits the combined findings in the requested format.
//
// Per-tool and project-scope rules run on each non-nil set. Comparison
// rules run only when both sets are present. Source-coordinate context
// is preserved per-finding so callers can distinguish static and live
// findings by file path even when the rule itself is set-agnostic.
func analyzeAll(static, live *mcpmodel.ToolSet, format string) error {
	rules := v2.All()
	var findings []v2.Finding

	for _, set := range []*mcpmodel.ToolSet{static, live} {
		if set == nil {
			continue
		}
		for i := range set.Tools {
			findings = append(findings, v2.Run(rules, &set.Tools[i])...)
		}
		findings = append(findings, v2.RunProject(rules, set)...)
	}

	if static != nil && live != nil {
		findings = append(findings, v2.RunComparison(rules, static, live)...)
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
