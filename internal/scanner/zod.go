package scanner

// Zod → JSON Schema conversion for TypeScript tool schemas. The official
// SDK accepts a raw shape ({ city: z.string() }, v1) or a z.object(...)
// (v1 and v2) and converts it with zod's own JSON Schema support; this
// mirrors the common subset so source-level rules see the same
// properties, types, enums, patterns, and required list the server will
// advertise. Unrecognized expressions become {} (any) rather than
// failing the whole schema.

// zodConverter resolves identifiers against a file's const declarations.
type zodConverter struct {
	consts map[string]any
}

// zodBaseTypes maps z.<name>() constructors to their JSON Schema.
var zodBaseTypes = map[string]map[string]any{
	"string":  {"type": "string"},
	"number":  {"type": "number"},
	"int":     {"type": "integer"},
	"bigint":  {"type": "integer"},
	"boolean": {"type": "boolean"},
	"date":    {"type": "string", "format": "date-time"},
	"null":    {"type": "null"},
	"any":     {},
	"unknown": {},
	"url":     {"type": "string", "format": "uri"},
	"email":   {"type": "string", "format": "email"},
	"uuid":    {"type": "string", "format": "uuid"},
	"record":  {"type": "object"},
	"map":     {"type": "object"},
	"tuple":   {"type": "array"},
	"set":     {"type": "array", "uniqueItems": true},
}

// zodFormats maps string refinements to JSON Schema formats.
var zodFormats = map[string]string{
	"url": "uri", "email": "email", "uuid": "uuid", "datetime": "date-time", "date": "date", "ipv4": "ipv4", "ipv6": "ipv6",
}

// shapeSchema converts a raw shape object or a z.object(...) chain to an
// object schema. ok is false when v is neither.
func (zc *zodConverter) shapeSchema(v any) (map[string]any, bool) {
	v = zc.resolve(v)
	if m, isMap := v.(map[string]any); isMap {
		if isJSONSchemaObject(m) {
			js, ok := toJSONValue(m)
			obj, isObj := js.(map[string]any)
			return obj, ok && isObj
		}
		return zc.objectSchema(m), true
	}
	s, _, ok := zc.schema(v)
	if !ok || s["type"] != "object" {
		return nil, false
	}
	return s, true
}

// isJSONSchemaObject distinguishes a literal JSON Schema
// ({type: "object", properties: ...}) from a zod raw shape.
func isJSONSchemaObject(m map[string]any) bool {
	t, ok := m["type"].(string)
	return ok && t == "object"
}

func (zc *zodConverter) objectSchema(shape map[string]any) map[string]any {
	props := map[string]any{}
	var required []any
	for _, name := range sortedMapKeys(shape) {
		if name == spreadKey {
			continue
		}
		s, optional, ok := zc.schema(shape[name])
		if !ok {
			s, optional = map[string]any{}, false
		}
		props[name] = s
		if !optional {
			required = append(required, name)
		}
	}
	out := map[string]any{"type": "object", "properties": props}
	if len(required) > 0 {
		out["required"] = required
	}
	return out
}

func (zc *zodConverter) resolve(v any) any {
	for i := 0; i < 8; i++ {
		next, ok := zc.resolveStep(v)
		if !ok {
			return v
		}
		v = next
	}
	return v
}

// resolveStep replaces a leading const identifier in v with its value:
// Name, Name.shape, or Name.optional() chained onto the const's chain.
func (zc *zodConverter) resolveStep(v any) (any, bool) {
	ch, ok := v.(*chainValue)
	if !ok || ch.base != nil || len(ch.segments) == 0 || ch.segments[0].called {
		return v, false
	}
	target, found := zc.consts[ch.segments[0].name]
	if !found {
		return v, false
	}
	rest := ch.segments[1:]
	if len(rest) == 0 || (len(rest) == 1 && rest[0].name == "shape" && !rest[0].called) {
		return target, true
	}
	return &chainValue{segments: append(rootSegments(target), rest...)}, true
}

func rootSegments(v any) []chainSegment {
	if ch, ok := v.(*chainValue); ok && ch.base == nil {
		return append([]chainSegment(nil), ch.segments...)
	}
	return []chainSegment{{name: "\x00unresolved"}}
}

// schema converts one zod expression. optional reports .optional(),
// .nullish(), or .default(...).
func (zc *zodConverter) schema(v any) (map[string]any, bool, bool) {
	v = zc.resolve(v)
	ch, ok := v.(*chainValue)
	if !ok || ch.base != nil || len(ch.segments) < 2 || !ch.segments[1].called {
		return nil, false, false
	}
	s, ok := zc.base(ch.segments[1])
	if !ok {
		return nil, false, false
	}
	optional := false
	for _, seg := range ch.segments[2:] {
		if seg.called {
			s, optional = zc.modifier(s, seg, optional)
		}
	}
	return s, optional, true
}

func (zc *zodConverter) base(seg chainSegment) (map[string]any, bool) {
	if t, ok := zodBaseTypes[seg.name]; ok {
		return copySchema(t), true
	}
	arg := firstArg(seg)
	switch seg.name {
	case "object", "strictObject", "looseObject":
		m, ok := zc.resolve(arg).(map[string]any)
		if !ok {
			return map[string]any{"type": "object"}, true
		}
		return zc.objectSchema(m), true
	case "array":
		return zc.arraySchema(arg), true
	case "enum":
		return enumSchema(zc.resolve(arg)), true
	case "literal":
		if js, ok := toJSONValue(arg); ok {
			return map[string]any{"const": js}, true
		}
		return map[string]any{}, true
	case "union", "discriminatedUnion":
		return zc.unionSchema(seg), true
	case "optional", "nullable":
		s, _, ok := zc.schema(arg)
		return s, ok
	}
	return map[string]any{}, true
}

func (zc *zodConverter) arraySchema(item any) map[string]any {
	out := map[string]any{"type": "array"}
	if s, _, ok := zc.schema(item); ok {
		out["items"] = s
	}
	return out
}

func enumSchema(arg any) map[string]any {
	js, ok := toJSONValue(arg)
	values, isList := js.([]any)
	if !ok || !isList {
		return map[string]any{"type": "string"}
	}
	return map[string]any{"type": "string", "enum": values}
}

func (zc *zodConverter) unionSchema(seg chainSegment) map[string]any {
	list, ok := lastArg(seg).([]any)
	if !ok {
		return map[string]any{}
	}
	var anyOf []any
	for _, opt := range list {
		s, _, ok := zc.schema(opt)
		if !ok {
			return map[string]any{}
		}
		anyOf = append(anyOf, s)
	}
	return map[string]any{"anyOf": anyOf}
}

// modifier applies one chained call such as .describe("x") or .min(1).
func (zc *zodConverter) modifier(s map[string]any, seg chainSegment, optional bool) (map[string]any, bool) {
	arg := firstArg(seg)
	switch seg.name {
	case "optional", "nullish":
		return s, true
	case "default", "catch":
		if js, ok := toJSONValue(arg); ok {
			s["default"] = js
		}
		return s, true
	case "describe":
		if d, ok := arg.(string); ok {
			s["description"] = d
		}
	case "array":
		return map[string]any{"type": "array", "items": s}, optional
	case "or":
		if other, _, ok := zc.schema(arg); ok {
			return map[string]any{"anyOf": []any{s, other}}, optional
		}
	default:
		applyRefinement(s, seg.name, arg)
	}
	return s, optional
}

// applyRefinement maps value constraints onto JSON Schema keywords.
func applyRefinement(s map[string]any, name string, arg any) {
	if f, ok := zodFormats[name]; ok && s["type"] == "string" {
		s["format"] = f
		return
	}
	switch name {
	case "int":
		s["type"] = "integer"
	case "regex":
		if re, ok := arg.(regexValue); ok {
			s["pattern"] = re.pattern
		}
	case "min", "max", "length", "gte", "lte", "positive", "nonnegative":
		applyBound(s, name, arg)
	}
}

func applyBound(s map[string]any, name string, arg any) {
	n, isNum := arg.(float64)
	keyword := map[string]map[string]string{
		"string": {"min": "minLength", "max": "maxLength", "length": "minLength"},
		"array":  {"min": "minItems", "max": "maxItems", "length": "minItems"},
	}
	t, _ := s["type"].(string)
	if kw, ok := keyword[t][name]; ok && isNum {
		s[kw] = n
		return
	}
	switch name {
	case "min", "gte":
		if isNum {
			s["minimum"] = n
		}
	case "max", "lte":
		if isNum {
			s["maximum"] = n
		}
	case "positive":
		s["exclusiveMinimum"] = 0.0
	case "nonnegative":
		s["minimum"] = 0.0
	}
}

func firstArg(seg chainSegment) any {
	if len(seg.args) == 0 {
		return nil
	}
	return seg.args[0]
}

func lastArg(seg chainSegment) any {
	if len(seg.args) == 0 {
		return nil
	}
	return seg.args[len(seg.args)-1]
}

func copySchema(m map[string]any) map[string]any {
	out := make(map[string]any, len(m))
	for k, v := range m {
		out[k] = v
	}
	return out
}
