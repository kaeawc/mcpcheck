package main

import (
	"fmt"
	"io"
	"strings"

	"github.com/kaeawc/mcpcheck/internal/mcpmodel"
	"github.com/kaeawc/mcpcheck/internal/scanner"
	v2 "github.com/kaeawc/mcpcheck/internal/v2"
)

// writeSummary prints what was analyzed and the finding totals, so a
// clean run is distinguishable from one that silently found no tools.
func writeSummary(w io.Writer, static, live *mcpmodel.ToolSet, findings []v2.Finding) {
	if static != nil {
		if static.Server != nil {
			fmt.Fprintf(w, "mcpcheck: recorded session %s: %s\n", static.Source, describeServer(static))
		} else {
			fmt.Fprintf(w, "mcpcheck: source %s: %s\n", static.Source, plural(len(static.Tools), "tool"))
		}
		if len(static.Tools) == 0 {
			fmt.Fprintf(w, "mcpcheck: no tool registrations recognized. Supported patterns:\n")
			for _, p := range scanner.SupportedPatterns() {
				fmt.Fprintf(w, "    %s\n", p)
			}
			fmt.Fprintf(w, "  If tools are registered dynamically, check the running server with --live or --url.\n")
		}
	}
	if live != nil {
		fmt.Fprintf(w, "mcpcheck: live %s\n", describeServer(live))
	}
	fmt.Fprintf(w, "mcpcheck: %s\n", countFindings(findings))
}

func describeServer(set *mcpmodel.ToolSet) string {
	s := set.Server
	if s == nil {
		return fmt.Sprintf("%s: %s", set.Source, plural(len(set.Tools), "tool"))
	}
	name := s.Name
	if name == "" {
		name = "(unnamed server)"
	}
	if s.Version != "" {
		name += " " + s.Version
	}
	proto := s.ProtocolVersion
	if proto != s.OfferedProtocolVersion {
		proto += " (offered " + s.OfferedProtocolVersion + ")"
	}
	pages := ""
	if s.ToolPages > 1 {
		pages = fmt.Sprintf(" across %d pages", s.ToolPages)
	}
	return fmt.Sprintf("%s via %s (%s): protocol %s, %s%s",
		name, s.Transport, s.Endpoint, proto, plural(len(set.Tools), "tool"), pages)
}

func countFindings(findings []v2.Finding) string {
	if len(findings) == 0 {
		return "no findings"
	}
	counts := map[v2.Severity]int{}
	for _, f := range findings {
		counts[f.Severity]++
	}
	var parts []string
	for _, sev := range []v2.Severity{v2.SevError, v2.SevWarning, v2.SevInfo} {
		if counts[sev] > 0 {
			parts = append(parts, plural(counts[sev], string(sev)))
		}
	}
	return strings.Join(parts, ", ")
}

func plural(n int, noun string) string {
	if n == 1 {
		return fmt.Sprintf("1 %s", noun)
	}
	return fmt.Sprintf("%d %ss", n, noun)
}
