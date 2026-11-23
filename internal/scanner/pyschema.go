package scanner

import (
	"strings"
)

// pyParam is one parameter of a Python function signature.
type pyParam struct {
	name       string
	annotation []any // union members of the type hint; nil if unannotated
	def        any   // parsed default value
	hasDefault bool
}

// pyScalarTypes maps Python and Pydantic type names to JSON Schema.
var pyScalarTypes = map[string]map[string]any{
	"str": {"type": "string"}, "bytes": {"type": "string"},
	"int": {"type": "integer"}, "float": {"type": "number"}, "Decimal": {"type": "number"},
	"bool": {"type": "boolean"}, "None": {"type": "null"}, "NoneType": {"type": "null"},
	"dict": {"type": "object"}, "Dict": {"type": "object"}, "Mapping": {"type": "object"},
	"MutableMapping": {"type": "object"}, "TypedDict": {"type": "object"},
	"datetime": {"type": "string", "format": "date-time"}, "date": {"type": "string", "format": "date"},
	"time": {"type": "string", "format": "time"}, "UUID": {"type": "string", "format": "uuid"},
	"Path": {"type": "string", "format": "path"}, "AnyUrl": {"type": "string", "format": "uri"},
	"HttpUrl": {"type": "string", "format": "uri"}, "AnyHttpUrl": {"type": "string", "format": "uri"},
	"EmailStr": {"type": "string", "format": "email"},
}

// pyArrayTypes are generic containers advertised as JSON arrays.
var pyArrayTypes = map[string]bool{
	"list": true, "List": true, "Sequence": true, "Iterable": true, "tuple": true, "Tuple": true,
	"set": true, "Set": true, "frozenset": true, "FrozenSet": true,
}

// pyFieldKeywords maps pydantic Field() keywords to JSON Schema keywords.
var pyFieldKeywords = map[string]string{
	"description": "description", "title": "title", "pattern": "pattern", "examples": "examples",
	"ge": "minimum", "le": "maximum", "gt": "exclusiveMinimum", "lt": "exclusiveMaximum",
	"min_length": "minLength", "max_length": "maxLength", "multiple_of": "multipleOf",
}

// pyParams parses the parameter list whose '(' is at open and returns
// the parameters plus the offset where the function body starts.
func pyParams(src string, open int) ([]pyParam, int) {
	p := newLitParser(src, open+1, langPython)
	p.depth = 1
	var params []pyParam
	for {
		p.ws()
		if p.eof() {
			return params, len(src)
		}
		if p.peek() == ')' {
			p.pos++
			break
		}
		start := p.pos
		p.skipExpr()
		if param, ok := parsePyParam(src[start:p.pos]); ok {
			params = append(params, param)
		}
		if p.peek() == ',' {
			p.pos++
		}
	}
	return params, pyBodyStart(src, p.pos)
}

// pyBodyStart returns the offset after the ':' that ends a def header.
func pyBodyStart(src string, pos int) int {
	if i := topLevelIndex(src[pos:], ':'); i >= 0 {
		return pos + i + 1
	}
	return len(src)
}

func parsePyParam(text string) (pyParam, bool) {
	text = strings.TrimSpace(text)
	p := newLitParser(text, 0, langPython)
	name := p.ident()
	if name == "" || name == "self" || name == "cls" {
		return pyParam{}, false
	}
	param := pyParam{name: name}
	rest := strings.TrimSpace(text[p.pos:])
	annotation, defText, hasDefault := strings.TrimSpace(rest), "", false
	if i := topLevelIndex(rest, '='); i >= 0 {
		annotation, defText, hasDefault = strings.TrimSpace(rest[:i]), strings.TrimSpace(rest[i+1:]), true
	}
	if strings.HasPrefix(annotation, ":") {
		for _, part := range topLevelSplit(strings.TrimSpace(annotation[1:]), '|') {
			param.annotation = append(param.annotation, newLitParser(part, 0, langPython).value())
		}
	}
	if hasDefault {
		param.def, param.hasDefault = newLitParser(defText, 0, langPython).value(), true
	}
	return param, true
}

// topLevelIndex returns the index of the first sep outside brackets and
// strings (ignoring "==", "!=", "<=", ">=" for '='), or -1.
func topLevelIndex(text string, sep byte) int {
	p := newLitParser(text, 0, langPython)
	var stack []int
	for !p.eof() {
		if len(stack) == 0 && p.peek() == sep && !isComparison(text, p.pos, sep) {
			return p.pos
		}
		stack = p.stepStructure(stack)
	}
	return -1
}

func isComparison(text string, i int, sep byte) bool {
	if sep != '=' {
		return false
	}
	prev, next := byte(0), byte(0)
	if i > 0 {
		prev = text[i-1]
	}
	if i+1 < len(text) {
		next = text[i+1]
	}
	return next == '=' || strings.IndexByte("=!<>", prev) >= 0
}

func topLevelSplit(text string, sep byte) []string {
	var parts []string
	for {
		i := topLevelIndex(text, sep)
		if i < 0 {
			return append(parts, strings.TrimSpace(text))
		}
		parts = append(parts, strings.TrimSpace(text[:i]))
		text = text[i+1:]
	}
}

// pySchema builds the input schema FastMCP derives from a signature.
func pySchema(params []pyParam) map[string]any {
	props := map[string]any{}
	var required []any
	for _, param := range params {
		if param.isContext() {
			continue
		}
		s, hasDefault := param.schema()
		props[param.name] = s
		if !hasDefault {
			required = append(required, param.name)
		}
	}
	out := map[string]any{"type": "object", "properties": props}
	if len(required) > 0 {
		out["required"] = required
	}
	return out
}

// isContext reports parameters typed as the SDK's Context, which FastMCP
// injects and never advertises.
func (pp pyParam) isContext() bool {
	for _, a := range pp.annotation {
		if ch, ok := a.(*chainValue); ok && strings.HasSuffix(ch.segments[len(ch.segments)-1].name, "Context") {
			return true
		}
	}
	return false
}

func (pp pyParam) schema() (map[string]any, bool) {
	s, field := pyUnionSchema(pp.annotation)
	hasDefault := pp.hasDefault
	if fieldCall, ok := pyFieldCall(pp.def); ok {
		field = mergeKwargs(field, fieldCall.kwargs)
		hasDefault = pyFieldHasDefault(fieldCall)
	} else if pp.hasDefault {
		if js, ok := toJSONValue(pp.def); ok && js != nil {
			s["default"] = js
		}
	}
	applyPyField(s, field)
	return s, hasDefault
}

// pyUnionSchema converts the members of a type hint union, dropping None
// (Optional only affects nullability, not whether the field is required).
func pyUnionSchema(members []any) (map[string]any, map[string]any) {
	var schemas []any
	var field map[string]any
	for _, m := range members {
		if m == nil {
			continue
		}
		s, f := pyTypeSchema(m)
		schemas = append(schemas, s)
		field = mergeKwargs(field, f)
	}
	switch len(schemas) {
	case 0:
		return map[string]any{}, field
	case 1:
		return schemas[0].(map[string]any), field
	}
	return map[string]any{"anyOf": schemas}, field
}

// pyTypeSchema converts one type expression and returns any Field()
// keywords found in an Annotated[...] wrapper.
func pyTypeSchema(v any) (map[string]any, map[string]any) {
	ch, ok := v.(*chainValue)
	if !ok || ch.base != nil {
		return map[string]any{}, nil
	}
	seg := ch.segments[len(ch.segments)-1]
	switch seg.name {
	case "Annotated":
		return pyAnnotated(seg.index)
	case "Optional":
		return pyUnionSchema(seg.index)
	case "Union":
		return pyUnionSchema(seg.index)
	case "Literal":
		return pyLiteral(seg.index), nil
	}
	if pyArrayTypes[seg.name] {
		out := map[string]any{"type": "array"}
		if len(seg.index) > 0 {
			out["items"], _ = pyTypeSchema(seg.index[0])
		}
		return out, nil
	}
	if s, ok := pyScalarTypes[seg.name]; ok {
		return copySchema(s), nil
	}
	return map[string]any{}, nil
}

func pyAnnotated(index []any) (map[string]any, map[string]any) {
	if len(index) == 0 {
		return map[string]any{}, nil
	}
	s, field := pyTypeSchema(index[0])
	for _, meta := range index[1:] {
		if call, ok := pyFieldCall(meta); ok {
			field = mergeKwargs(field, call.kwargs)
		}
	}
	return s, field
}

func pyLiteral(values []any) map[string]any {
	var enum []any
	allStrings := true
	for _, v := range values {
		js, ok := toJSONValue(v)
		if !ok {
			return map[string]any{}
		}
		_, isStr := js.(string)
		allStrings = allStrings && isStr
		enum = append(enum, js)
	}
	out := map[string]any{"enum": enum}
	if allStrings {
		out["type"] = "string"
	}
	return out
}

// pyFieldCall returns the Field(...) call segment of v, if v is one.
func pyFieldCall(v any) (chainSegment, bool) {
	ch, ok := v.(*chainValue)
	if !ok || ch.base != nil {
		return chainSegment{}, false
	}
	last := ch.segments[len(ch.segments)-1]
	return last, last.called && last.name == "Field"
}

// pyFieldHasDefault reports whether Field(...) supplies a default: a
// default/default_factory keyword or a positional value other than `...`.
func pyFieldHasDefault(call chainSegment) bool {
	if _, ok := call.kwargs["default"]; ok {
		return true
	}
	if _, ok := call.kwargs["default_factory"]; ok {
		return true
	}
	if len(call.args) == 0 {
		return false
	}
	_, ellipsis := call.args[0].(unknownValue)
	return !ellipsis
}

func applyPyField(s map[string]any, field map[string]any) {
	for k, v := range field {
		kw, ok := pyFieldKeywords[k]
		if !ok {
			continue
		}
		if s["type"] == "array" && (kw == "minLength" || kw == "maxLength") {
			kw = strings.Replace(kw, "Length", "Items", 1)
		}
		if js, ok := toJSONValue(v); ok {
			s[kw] = js
		}
	}
}

func mergeKwargs(dst, src map[string]any) map[string]any {
	if len(src) == 0 {
		return dst
	}
	if dst == nil {
		dst = map[string]any{}
	}
	for k, v := range src {
		dst[k] = v
	}
	return dst
}
