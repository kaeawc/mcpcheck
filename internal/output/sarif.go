package output

import (
	"encoding/json"
	"io"
	"sort"

	"github.com/kaeawc/mcpcheck/internal/v2"
)

// SARIF version targeted by WriteSARIF. SARIF 2.1.0 is the common
// dialect: GitHub Code Scanning, Azure DevOps, and the SARIF VS Code
// viewer all consume it.
const sarifVersion = "2.1.0"

// sarifInformationURI is published in the run.tool.driver.informationUri
// so SARIF consumers can link back to the project.
const sarifInformationURI = "https://github.com/kaeawc/mcpcheck"

// sarifLog is the top-level SARIF document.
type sarifLog struct {
	Schema  string     `json:"$schema,omitempty"`
	Version string     `json:"version"`
	Runs    []sarifRun `json:"runs"`
}

type sarifRun struct {
	Tool    sarifTool      `json:"tool"`
	Results []sarifResult  `json:"results"`
}

type sarifTool struct {
	Driver sarifDriver `json:"driver"`
}

type sarifDriver struct {
	Name           string             `json:"name"`
	InformationURI string             `json:"informationUri,omitempty"`
	Rules          []sarifRuleDescr   `json:"rules,omitempty"`
}

type sarifRuleDescr struct {
	ID                   string                       `json:"id"`
	ShortDescription     sarifMultiformatMessage      `json:"shortDescription"`
	DefaultConfiguration *sarifReportingConfiguration `json:"defaultConfiguration,omitempty"`
	Properties           map[string]any               `json:"properties,omitempty"`
}

type sarifReportingConfiguration struct {
	Level string `json:"level,omitempty"`
}

type sarifMultiformatMessage struct {
	Text string `json:"text"`
}

type sarifResult struct {
	RuleID    string          `json:"ruleId"`
	Level     string          `json:"level,omitempty"`
	Message   sarifMessage    `json:"message"`
	Locations []sarifLocation `json:"locations,omitempty"`
}

type sarifMessage struct {
	Text string `json:"text"`
}

type sarifLocation struct {
	PhysicalLocation sarifPhysicalLocation `json:"physicalLocation"`
}

type sarifPhysicalLocation struct {
	ArtifactLocation sarifArtifactLocation `json:"artifactLocation"`
	Region           *sarifRegion          `json:"region,omitempty"`
}

type sarifArtifactLocation struct {
	URI string `json:"uri"`
}

type sarifRegion struct {
	StartLine int `json:"startLine,omitempty"`
}

// WriteSARIF emits a SARIF 2.1.0 log. The driver section includes every
// registered rule (queried via v2.All) so downstream consumers can render
// rule names and descriptions even for rules that produced no findings
// in this run.
func WriteSARIF(w io.Writer, findings []v2.Finding) error {
	log := sarifLog{
		Schema:  "https://json.schemastore.org/sarif-2.1.0.json",
		Version: sarifVersion,
		Runs: []sarifRun{
			{
				Tool: sarifTool{
					Driver: sarifDriver{
						Name:           "mcpcheck",
						InformationURI: sarifInformationURI,
						Rules:          buildSARIFRules(),
					},
				},
				Results: buildSARIFResults(findings),
			},
		},
	}

	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(log)
}

func buildSARIFRules() []sarifRuleDescr {
	all := v2.All()
	out := make([]sarifRuleDescr, 0, len(all))
	for _, r := range all {
		props := map[string]any{
			"category": string(r.Category),
		}
		if r.Fix != v2.FixNone {
			props["fix"] = string(r.Fix)
		}
		out = append(out, sarifRuleDescr{
			ID:               r.ID,
			ShortDescription: sarifMultiformatMessage{Text: r.Description},
			DefaultConfiguration: &sarifReportingConfiguration{
				Level: severityToSARIFLevel(r.Severity),
			},
			Properties: props,
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

func buildSARIFResults(findings []v2.Finding) []sarifResult {
	if findings == nil {
		return []sarifResult{}
	}
	out := make([]sarifResult, 0, len(findings))
	for _, f := range findings {
		res := sarifResult{
			RuleID:  f.RuleID,
			Level:   severityToSARIFLevel(f.Severity),
			Message: sarifMessage{Text: f.Message},
		}
		if f.SourceFile != "" {
			loc := sarifLocation{
				PhysicalLocation: sarifPhysicalLocation{
					ArtifactLocation: sarifArtifactLocation{URI: f.SourceFile},
				},
			}
			if f.SourceLine > 0 {
				loc.PhysicalLocation.Region = &sarifRegion{StartLine: f.SourceLine}
			}
			res.Locations = []sarifLocation{loc}
		}
		out = append(out, res)
	}
	return out
}

// severityToSARIFLevel maps mcpcheck severity to SARIF level. SARIF has
// no "info" level; "note" is the closest equivalent.
func severityToSARIFLevel(s v2.Severity) string {
	switch s {
	case v2.SevError:
		return "error"
	case v2.SevWarning:
		return "warning"
	case v2.SevInfo:
		return "note"
	}
	return "none"
}
