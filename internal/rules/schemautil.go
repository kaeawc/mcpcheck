package rules

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
