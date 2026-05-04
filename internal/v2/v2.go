// Package v2 is the rule registry and dispatch contract for mcpcheck.
//
// A rule lives in internal/rules/, declares its metadata as a *Rule, and
// calls Register from an init function. Rules come in two flavors:
//
//   - Per-tool rules implement the Implementation callback. The dispatcher
//     invokes Implementation once per tool with a *Context bound to that
//     tool. Rules report findings via Context.Report.
//
//   - Project-scope rules implement the ProjectImplementation callback.
//     The dispatcher invokes ProjectImplementation once per ToolSet with
//     a *ProjectContext that has the whole set in view. Rules report
//     findings via ProjectContext.ReportTool, attaching each finding to
//     the specific tool it concerns.
//
// A rule may declare either or both — a rule that has both runs in both
// phases.
package v2

import "github.com/kaeawc/mcpcheck/internal/mcpmodel"

type Severity string

const (
	SevError   Severity = "error"
	SevWarning Severity = "warning"
	SevInfo    Severity = "info"
)

type FixLevel string

const (
	FixNone      FixLevel = ""
	FixCosmetic  FixLevel = "cosmetic"
	FixIdiomatic FixLevel = "idiomatic"
	FixSemantic  FixLevel = "semantic"
)

type Category string

const (
	CatAgreement Category = "schema-handler-agreement"
	CatSpec      Category = "spec-compliance"
	CatSafety    Category = "safety-contract"
	CatExamples  Category = "examples"
)

type Finding struct {
	RuleID     string
	Severity   Severity
	Category   Category
	Message    string
	ToolName   string
	SourceFile string
	SourceLine int
}

type Context struct {
	Tool     *mcpmodel.Tool
	findings []Finding
	rule     *Rule
}

func (c *Context) Report(message string) {
	c.findings = append(c.findings, Finding{
		RuleID:     c.rule.ID,
		Severity:   c.rule.Severity,
		Category:   c.rule.Category,
		Message:    message,
		ToolName:   c.Tool.Name,
		SourceFile: c.Tool.SourceFile,
		SourceLine: c.Tool.SourceLine,
	})
}

func (c *Context) Findings() []Finding { return c.findings }

// ProjectContext is the per-ToolSet context handed to project-scope rules.
type ProjectContext struct {
	ToolSet  *mcpmodel.ToolSet
	findings []Finding
	rule     *Rule
}

// ReportTool attaches a finding to the given tool. Source coordinates come
// from the tool itself.
func (pc *ProjectContext) ReportTool(tool *mcpmodel.Tool, message string) {
	pc.findings = append(pc.findings, Finding{
		RuleID:     pc.rule.ID,
		Severity:   pc.rule.Severity,
		Category:   pc.rule.Category,
		Message:    message,
		ToolName:   tool.Name,
		SourceFile: tool.SourceFile,
		SourceLine: tool.SourceLine,
	})
}

func (pc *ProjectContext) Findings() []Finding { return pc.findings }

type Rule struct {
	ID                    string
	Category              Category
	Severity              Severity
	Description           string
	Fix                   FixLevel
	Implementation        func(*Context)
	ProjectImplementation func(*ProjectContext)
}

var registry []*Rule

func Register(r *Rule) {
	if r == nil {
		panic("v2.Register: nil rule")
	}
	if r.ID == "" {
		panic("v2.Register: rule has empty ID")
	}
	if r.Implementation == nil && r.ProjectImplementation == nil {
		panic("v2.Register: rule " + r.ID + " declares neither Implementation nor ProjectImplementation")
	}
	for _, existing := range registry {
		if existing.ID == r.ID {
			panic("v2.Register: duplicate rule ID " + r.ID)
		}
	}
	registry = append(registry, r)
}

func All() []*Rule {
	out := make([]*Rule, len(registry))
	copy(out, registry)
	return out
}

// Run invokes per-tool rules against a single tool. Rules without an
// Implementation are skipped. To run project-scope rules, use RunProject.
func Run(rules []*Rule, tool *mcpmodel.Tool) []Finding {
	var findings []Finding
	for _, r := range rules {
		if r.Implementation == nil {
			continue
		}
		ctx := &Context{Tool: tool, rule: r}
		r.Implementation(ctx)
		findings = append(findings, ctx.Findings()...)
	}
	return findings
}

// RunProject invokes project-scope rules against the whole ToolSet.
// Rules without a ProjectImplementation are skipped.
func RunProject(rules []*Rule, set *mcpmodel.ToolSet) []Finding {
	var findings []Finding
	for _, r := range rules {
		if r.ProjectImplementation == nil {
			continue
		}
		pc := &ProjectContext{ToolSet: set, rule: r}
		r.ProjectImplementation(pc)
		findings = append(findings, pc.Findings()...)
	}
	return findings
}
