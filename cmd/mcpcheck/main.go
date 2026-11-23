// Command mcpcheck is the CLI frontend for the analyzer.
//
// Usage:
//
//	mcpcheck [flags] <path>
//	mcpcheck [flags] --live "<command>" [<path>]
//	mcpcheck [flags] --url <url> [<path>]
//	mcpcheck --list-rules [--format text|json]
//
// `path` may be a tools.json (MCP tools/list response or bare list), a
// Python / TypeScript / JavaScript / Go / Rust source file, or a
// directory.
//
// `--live "<command>"` boots the command as an MCP server over stdio;
// `--url` connects to one over Streamable HTTP or legacy HTTP+SSE. Either
// runs the initialize / tools/list handshake and validates the tools the
// server actually advertises, plus how it behaves on the wire.
//
// Providing a live server and a `<path>` runs both intakes plus a
// comparison phase that flags tools advertised by one but not the
// other (catches dynamic registration drift).
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"time"

	"github.com/kaeawc/mcpcheck/internal/mcpclient"
	"github.com/kaeawc/mcpcheck/internal/mcpmodel"
	"github.com/kaeawc/mcpcheck/internal/output"
	_ "github.com/kaeawc/mcpcheck/internal/rules"
	"github.com/kaeawc/mcpcheck/internal/scanner"
	v2 "github.com/kaeawc/mcpcheck/internal/v2"
)

// version is overridden at build time with -ldflags "-X main.version=...".
var version = "dev"

type config struct {
	format          string
	listRules       bool
	live            string
	url             string
	transport       string
	headers         headerFlag
	protocolVersion string
	clientCaps      string
	timeout         time.Duration
	trace           bool
	verbose         bool
	quiet           bool
	save            string
	path            string
}

func parseFlags(args []string) (*config, error) {
	c := &config{}
	fs := flag.NewFlagSet("mcpcheck", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	fs.StringVar(&c.format, "format", "text", "output format: text|json|sarif")
	fs.BoolVar(&c.listRules, "list-rules", false, "list registered rules and exit")
	fs.StringVar(&c.live, "live", "", "run the given command as an MCP server over stdio")
	fs.StringVar(&c.url, "url", "", "connect to an MCP server over HTTP at this URL")
	fs.StringVar(&c.transport, "transport", mcpclient.TransportAuto, "HTTP transport: auto|streamable-http|sse")
	fs.Var(&c.headers, "header", "HTTP header \"Name: value\" sent with every request (repeatable)")
	fs.StringVar(&c.protocolVersion, "protocol-version", "", "pin the protocol version (default: try "+mcpclient.LatestProtocolVersion+", fall back to "+mcpclient.LatestLegacyProtocolVersion+")")
	fs.StringVar(&c.clientCaps, "client-capabilities", "", "client capabilities to declare: roots,sampling,elicitation or all")
	fs.DurationVar(&c.timeout, "live-timeout", 30*time.Second, "deadline for the live session")
	fs.BoolVar(&c.trace, "trace", false, "log every JSON-RPC message to stderr")
	fs.BoolVar(&c.verbose, "verbose", false, "stream the stdio server's stderr")
	fs.BoolVar(&c.quiet, "quiet", false, "suppress the summary on stderr")
	fs.StringVar(&c.save, "save", "", "write the live session (tools + server observations) to this JSON file")
	if err := fs.Parse(args); err != nil {
		return nil, err
	}
	if fs.NArg() > 1 {
		return nil, fmt.Errorf("expected at most one path, got %d", fs.NArg())
	}
	c.path = fs.Arg(0)
	return c, c.validate()
}

func (c *config) validate() error {
	if c.listRules {
		return nil
	}
	hasLive := c.live != "" || c.url != ""
	switch {
	case c.live != "" && c.url != "":
		return fmt.Errorf("--live and --url are mutually exclusive")
	case !hasLive && c.path == "":
		return fmt.Errorf("nothing to check: give a path, --live, or --url")
	case c.save != "" && !hasLive:
		return fmt.Errorf("--save records a live session; use it with --live or --url")
	}
	warnUnknownVersion(c.protocolVersion)
	return nil
}

func warnUnknownVersion(v string) {
	if v != "" && !mcpclient.IsKnownProtocolVersion(v) {
		fmt.Fprintf(os.Stderr, "mcpcheck: note: %q is not a published protocol version; offering it anyway\n", v)
	}
}

func main() {
	c, err := parseFlags(os.Args[1:])
	if err != nil {
		if !errors.Is(err, flag.ErrHelp) {
			fmt.Fprintln(os.Stderr, "mcpcheck:", err)
		}
		usage()
		os.Exit(2)
	}
	os.Exit(run(c))
}

func run(c *config) int {
	if c.listRules {
		return report(writeRules(c.format))
	}
	ctx, cancel := context.WithTimeout(context.Background(), c.timeout)
	defer cancel()

	staticSet, liveSet, err := loadSets(ctx, c)
	if err != nil {
		fmt.Fprintln(os.Stderr, "mcpcheck:", err)
		return 1
	}
	if c.save != "" {
		if err := saveSession(c.save, liveSet); err != nil {
			return report(err)
		}
	}
	findings := analyze(staticSet, liveSet)
	if err := writeFindings(c.format, findings); err != nil {
		return report(err)
	}
	if !c.quiet {
		writeSummary(os.Stderr, staticSet, liveSet, findings)
	}
	for _, f := range findings {
		if f.Severity == v2.SevError {
			return 1
		}
	}
	return 0
}

func report(err error) int {
	if err == nil {
		return 0
	}
	fmt.Fprintln(os.Stderr, "mcpcheck:", err)
	return 1
}

// loadSets loads the static set from the positional path and the live
// set from --live or --url, each only when requested.
func loadSets(ctx context.Context, c *config) (staticSet, liveSet *mcpmodel.ToolSet, err error) {
	if c.path != "" {
		if staticSet, err = scanner.Load(c.path); err != nil {
			return nil, nil, err
		}
	}
	if c.live != "" || c.url != "" {
		opts, err := c.liveOptions()
		if err != nil {
			return nil, nil, err
		}
		if liveSet, err = scanner.LoadLive(ctx, opts); err != nil {
			return nil, nil, fmt.Errorf("live session failed during %w", err)
		}
	}
	return staticSet, liveSet, nil
}

func (c *config) liveOptions() (scanner.LiveOptions, error) {
	opts := scanner.LiveOptions{
		URL:             c.url,
		Transport:       c.transport,
		Header:          http.Header(c.headers),
		ProtocolVersion: c.protocolVersion,
		ClientVersion:   version,
	}
	caps, err := mcpclient.ParseClientCapabilities(c.clientCaps)
	if err != nil {
		return opts, fmt.Errorf("--client-capabilities: %w", err)
	}
	opts.ClientCapabilities = caps
	if c.live != "" {
		argv, err := splitCommand(c.live)
		if err != nil {
			return opts, fmt.Errorf("--live: %w", err)
		}
		opts.Command = argv
	}
	if c.trace {
		opts.Trace = os.Stderr
	}
	if c.verbose {
		opts.ServerStderr = os.Stderr
	}
	return opts, nil
}

func usage() {
	fmt.Fprintf(os.Stderr, `usage: mcpcheck [flags] <path>
       mcpcheck [flags] --live "<command>" [<path>]
       mcpcheck [flags] --url <url> [<path>]
       mcpcheck --list-rules [--format text|json]

  path      tools.json (tools/list response), a .py/.ts/.tsx/.js/.mjs/.cjs/.go/.rs
            file, or a directory (languages are detected per file)
  --live    run a command as an MCP server over stdio, e.g. --live "uv run server.py"
  --url     connect over HTTP; Streamable HTTP with automatic fallback to the
            legacy HTTP+SSE transport, e.g. --url http://localhost:8000/mcp

flags:
  --format text|json|sarif     output format (default text)
  --transport auto|streamable-http|sse
  --header "Name: value"       HTTP header for every request (repeatable)
  --protocol-version V         pin a protocol version; by default mcpcheck probes with
                               server/discover (%s) and falls back to initialize
                               (%s) for servers that only speak the legacy protocol
  --client-capabilities LIST   declare roots,sampling,elicitation (or all) to reveal
                               tools a server registers only for capable clients
  --live-timeout D             deadline for the live session (default 30s)
  --trace                      log every JSON-RPC message to stderr
  --verbose                    stream the stdio server's stderr
  --quiet                      no summary on stderr
  --save FILE                  record the live session; re-check it later with mcpcheck FILE
`, mcpclient.LatestProtocolVersion, mcpclient.LatestLegacyProtocolVersion)
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

// analyze runs every registered rule against whichever tool sets are
// non-nil. Per-tool and project-scope rules run on each set; comparison
// rules run only when both are present.
func analyze(static, live *mcpmodel.ToolSet) []v2.Finding {
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
	return dedupe(findings)
}

// dedupe drops a finding that repeats an earlier one for the same rule,
// tool, and message — the same problem seen in both the source and the
// live server — keeping the first, which carries the source location.
func dedupe(findings []v2.Finding) []v2.Finding {
	type key struct{ rule, tool, msg string }
	seen := map[key]bool{}
	out := findings[:0]
	for _, f := range findings {
		k := key{f.RuleID, f.ToolName, f.Message}
		if f.ToolName != "" && seen[k] {
			continue
		}
		seen[k] = true
		out = append(out, f)
	}
	return out
}

func writeFindings(format string, findings []v2.Finding) error {
	switch format {
	case "text":
		return output.WriteText(os.Stdout, findings)
	case "json":
		return output.WriteJSON(os.Stdout, findings)
	case "sarif":
		return output.WriteSARIF(os.Stdout, findings)
	default:
		return fmt.Errorf("unknown --format %q (want text|json|sarif)", format)
	}
}

func saveSession(path string, set *mcpmodel.ToolSet) error {
	f, err := os.Create(path) // #nosec G304 -- the user names the output file
	if err != nil {
		return fmt.Errorf("--save: %w", err)
	}
	if err := scanner.WriteSession(f, set); err != nil {
		_ = f.Close()
		return fmt.Errorf("--save: %w", err)
	}
	return f.Close()
}
