package rules

import "regexp"

// hasStringValueConstraint reports whether propSchema bounds the set of
// acceptable string values for a property via enum, pattern, or const.
//
// This is the shared "is this user-controlled string actually allowlisted?"
// check used by network and filesystem rules. Note that it does NOT count
// `format` as a constraint — JSON Schema's format keyword validates
// syntax (RFC-style URI shape, email shape, etc.) but does not bound
// the value space, so a `format: "uri"` field still accepts arbitrary
// hosts and a `format: "uri-reference"` field still accepts arbitrary
// paths.
func hasStringValueConstraint(propSchema map[string]any) bool {
	if _, ok := propSchema["enum"]; ok {
		return true
	}
	if _, ok := propSchema["pattern"]; ok {
		return true
	}
	if _, ok := propSchema["const"]; ok {
		return true
	}
	return false
}

// Phrases a tool uses to document that the server confines a path or URL
// at runtime, e.g. the official filesystem server's "Only works within
// allowed directories". The noun is required so that wording like
// "limited to 100 results" doesn't count.
var (
	pathConfinementRe = regexp.MustCompile(`(?i)\b(?:allowed|permitted|configured|approved|allowlisted|whitelisted)\s+(?:directories|directory|paths|folders|roots)\b` +
		`|\bsandbox(?:ed)?\s+(?:directory|dir|folder|root|path)\b` +
		`|\b(?:restricted|limited|confined|scoped)\s+to\s+(?:the\s+)?(?:\w+\s+){0,2}(?:directories|directory|paths|folders|roots|workspace|project)\b`)
	urlConfinementRe = regexp.MustCompile(`(?i)\b(?:allowed|permitted|configured|approved|allowlisted|whitelisted)\s+(?:domains?|hosts?|origins?|urls?|sites?|endpoints?)\b` +
		`|\b(?:restricted|limited|confined|scoped)\s+to\s+(?:the\s+)?(?:\w+\s+){0,2}(?:domains?|hosts?|origins?|urls?|sites?)\b`)
)

// documentsConfinement reports whether the tool's or the property's
// description says the server confines the value at runtime.
func documentsConfinement(re *regexp.Regexp, toolDescription string, propSchema map[string]any) bool {
	propDescription, _ := propSchema["description"].(string)
	return re.MatchString(toolDescription) || re.MatchString(propDescription)
}
