package rules

import (
	"fmt"
	"sort"

	"github.com/kaeawc/mcpcheck/internal/mcpmodel"
	"github.com/kaeawc/mcpcheck/internal/v2"
)

func init() {
	v2.Register(&v2.Rule{
		ID:          "tool-static-vs-live-mismatch",
		Category:    v2.CatSpec,
		Severity:    v2.SevWarning,
		Description: "Tools advertised by the static intake should match the tools the live server registers at runtime; drift here indicates dynamic registration that the static analysis can't see.",
		Fix:         v2.FixNone,
		ComparisonImplementation: func(cc *v2.ComparisonContext) {
			if cc.Static == nil || cc.Live == nil {
				return
			}
			staticByName := indexByName(cc.Static.Tools)
			liveByName := indexByName(cc.Live.Tools)

			// Static-only: tool advertised in source but not registered live.
			var staticOnly []string
			for name := range staticByName {
				if _, ok := liveByName[name]; !ok {
					staticOnly = append(staticOnly, name)
				}
			}
			sort.Strings(staticOnly)
			for _, name := range staticOnly {
				idx := staticByName[name]
				cc.ReportTool(&cc.Static.Tools[idx], fmt.Sprintf(
					"tool %q appears in static intake but the live server does not advertise it; possible conditional registration or build-time gating",
					name,
				))
			}

			// Live-only: tool registered at runtime but not visible in source.
			var liveOnly []string
			for name := range liveByName {
				if _, ok := staticByName[name]; !ok {
					liveOnly = append(liveOnly, name)
				}
			}
			sort.Strings(liveOnly)
			for _, name := range liveOnly {
				idx := liveByName[name]
				cc.ReportTool(&cc.Live.Tools[idx], fmt.Sprintf(
					"tool %q is registered at runtime but absent from the static intake; likely dynamic registration the static analysis missed",
					name,
				))
			}
		},
	})
}

// indexByName returns name → first index for tools in the slice. Duplicate
// names within one intake are the collision rule's territory, so we just
// keep the first occurrence here.
func indexByName(tools []mcpmodel.Tool) map[string]int {
	out := make(map[string]int, len(tools))
	for i, t := range tools {
		if _, ok := out[t.Name]; ok {
			continue
		}
		out[t.Name] = i
	}
	return out
}
