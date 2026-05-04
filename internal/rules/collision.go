package rules

import (
	"fmt"
	"sort"
	"strings"

	"github.com/kaeawc/mcpcheck/internal/mcpmodel"
	"github.com/kaeawc/mcpcheck/internal/v2"
)

func init() {
	v2.Register(&v2.Rule{
		ID:          "tool-name-collision",
		Category:    v2.CatSpec,
		Severity:    v2.SevError,
		Description: "Tool names within a single server must be unique; agent tool-call dispatch routes by name.",
		Fix:         v2.FixNone,
		ProjectImplementation: func(pc *v2.ProjectContext) {
			byName := make(map[string][]int)
			for i := range pc.ToolSet.Tools {
				name := pc.ToolSet.Tools[i].Name
				byName[name] = append(byName[name], i)
			}

			// Iterate names in sorted order so finding output is stable.
			collisions := make([]string, 0)
			for name, idxs := range byName {
				if name != "" && len(idxs) > 1 {
					collisions = append(collisions, name)
				}
			}
			sort.Strings(collisions)

			for _, name := range collisions {
				idxs := byName[name]
				for _, i := range idxs {
					tool := &pc.ToolSet.Tools[i]
					others := make([]string, 0, len(idxs)-1)
					for _, j := range idxs {
						if j == i {
							continue
						}
						others = append(others, locationOf(&pc.ToolSet.Tools[j]))
					}
					sort.Strings(others)
					msg := fmt.Sprintf(
						"duplicate tool name %q (also defined at: %s); names must be unique within a server",
						name, strings.Join(others, ", "),
					)
					pc.ReportTool(tool, msg)
				}
			}
		},
	})
}

// locationOf returns "file:line" for tools that have source coordinates,
// or a synthetic placeholder for JSON intakes that lack them. Falls back
// to the tool name to keep the message useful when nothing else is known.
func locationOf(t *mcpmodel.Tool) string {
	if t.SourceFile != "" {
		if t.SourceLine > 0 {
			return fmt.Sprintf("%s:%d", t.SourceFile, t.SourceLine)
		}
		return t.SourceFile
	}
	return "<source>"
}
