// Package v2 is the rule registry and dispatch contract for mcpcheck.
//
// A rule lives in internal/rules/, declares its metadata as a *Rule, and
// calls Register from an init function. The dispatcher walks ToolSets and
// invokes each rule's Implementation with a *Context bound to the current
// tool. Rules report findings via Context.Report.
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
	CatAgreement  Category = "schema-handler-agreement"
	CatSpec       Category = "spec-compliance"
	CatSafety     Category = "safety-contract"
	CatExamples   Category = "examples"
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

type Rule struct {
	ID             string
	Category       Category
	Severity       Severity
	Description    string
	Fix            FixLevel
	Implementation func(*Context)
}

var registry []*Rule

func Register(r *Rule) {
	if r == nil {
		panic("v2.Register: nil rule")
	}
	if r.ID == "" {
		panic("v2.Register: rule has empty ID")
	}
	if r.Implementation == nil {
		panic("v2.Register: rule " + r.ID + " has no Implementation")
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

func Run(rules []*Rule, tool *mcpmodel.Tool) []Finding {
	var findings []Finding
	for _, r := range rules {
		ctx := &Context{Tool: tool, rule: r}
		r.Implementation(ctx)
		findings = append(findings, ctx.Findings()...)
	}
	return findings
}
