// Literal parsing shared by the regex-free parts of the Python,
// TypeScript, and Rust intakes. It reads the literal subset of each
// language — strings, numbers, booleans, null, arrays, objects/dicts —
// plus call chains such as z.string().describe("x") or Field(default=1),
// which the Zod and Pydantic converters interpret. Anything else (a
// variable, an arrow function, a ternary) is skipped with bracket
// balancing and becomes an unknownValue, so one dynamic field doesn't
// hide the rest of a tool definition.
package scanner

import (
	"strconv"
	"strings"
)

type lang int

const (
	langPython lang = iota
	langJS
	langRust
)

// unknownValue stands for an expression the parser can't evaluate.
type unknownValue struct{}

// regexValue is a JavaScript regex literal, e.g. the argument of
// z.string().regex(/^https:/).
type regexValue struct{ pattern string }

// chainSegment is one link of a dotted / called chain: `z`, `string()`,
// `describe("x")`. Rust `::` paths are split the same way.
type chainSegment struct {
	name   string
	called bool
	args   []any
	kwargs map[string]any
	// index holds Python subscript arguments: Literal["a", "b"].
	index []any
}

// chainValue is an identifier chain with optional calls. base is set
// when the chain hangs off a literal, e.g. "x".into() in Rust.
type chainValue struct {
	base     any
	segments []chainSegment
}

// litParser reads values from src starting at pos.
type litParser struct {
	src  string
	pos  int
	lang lang
	// depth counts open brackets; outside them a Python newline ends
	// the statement.
	depth int
}

func newLitParser(src string, pos int, l lang) *litParser {
	return &litParser{src: src, pos: pos, lang: l}
}

func (p *litParser) eof() bool { return p.pos >= len(p.src) }

func (p *litParser) peek() byte {
	if p.eof() {
		return 0
	}
	return p.src[p.pos]
}

func (p *litParser) peekNext() byte {
	if p.pos+1 >= len(p.src) {
		return 0
	}
	return p.src[p.pos+1]
}

// ws skips whitespace and comments. Outside brackets in Python it stops
// at a newline, which ends the statement.
func (p *litParser) ws() {
	for !p.eof() {
		c := p.peek()
		switch {
		case c == '\n' && p.lang == langPython && p.depth == 0:
			return
		case c == ' ' || c == '\t' || c == '\n' || c == '\r':
			p.pos++
		case !p.skipComment():
			return
		}
	}
}

func (p *litParser) skipComment() bool {
	c, n := p.peek(), p.peekNext()
	switch {
	case p.lang == langPython && c == '#':
		p.skipTo("\n")
	case p.lang != langPython && c == '/' && n == '/':
		p.skipTo("\n")
	case p.lang != langPython && c == '/' && n == '*':
		p.pos += 2
		p.skipTo("*/")
		p.pos += 2
	default:
		return false
	}
	return true
}

func (p *litParser) skipTo(s string) {
	if i := strings.Index(p.src[p.pos:], s); i >= 0 {
		p.pos += i
		return
	}
	p.pos = len(p.src)
}

func isDigit(c byte) bool { return c >= '0' && c <= '9' }

// isQuote reports a string opener; backticks only quote in JS.
func (p *litParser) isQuote(c byte) bool {
	return c == '"' || c == '\'' || (c == '`' && p.lang == langJS)
}

func isIdentStart(c byte) bool {
	return c == '_' || c == '$' || (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z')
}

func isIdentChar(c byte) bool { return isIdentStart(c) || isDigit(c) }

func (p *litParser) ident() string {
	start := p.pos
	for !p.eof() && isIdentChar(p.peek()) {
		p.pos++
	}
	return p.src[start:p.pos]
}

// value parses one expression. It never fails: unparseable input is
// skipped and returned as unknownValue.
func (p *litParser) value() any {
	p.ws()
	v := p.primary()
	v = p.postfix(v)
	v = p.concat(v)
	p.ws()
	if !p.atValueEnd() {
		p.skipExpr()
		return unknownValue{}
	}
	return v
}

func (p *litParser) atValueEnd() bool {
	switch p.peek() {
	case 0, ',', ')', ']', '}', ';':
		return true
	case ':':
		return p.lang == langPython
	case '\n':
		return p.lang == langPython
	}
	return p.skipTypeSuffix()
}

// skipTypeSuffix consumes TypeScript `as const` / `satisfies T` after a
// value and reports whether the value then ends.
func (p *litParser) skipTypeSuffix() bool {
	if p.lang != langJS {
		return false
	}
	save := p.pos
	word := p.ident()
	if word != "as" && word != "satisfies" {
		p.pos = save
		return false
	}
	p.ws()
	p.ident()
	p.ws()
	switch p.peek() {
	case 0, ',', ')', ']', '}', ';':
		return true
	}
	p.pos = save
	return false
}

func (p *litParser) primary() any {
	c := p.peek()
	switch {
	case c == '{':
		return p.object()
	case c == '[':
		return p.array()
	case p.isQuote(c):
		return p.stringLit()
	case c == '-' || isDigit(c):
		return p.number()
	case c == '/' && p.lang == langJS:
		return p.regex()
	case isIdentStart(c):
		return p.identExpr()
	}
	p.skipExpr()
	return unknownValue{}
}

func (p *litParser) number() any {
	start := p.pos
	if p.peek() == '-' {
		p.pos++
	}
	for !p.eof() && strings.IndexByte("0123456789._eE+-xabcdefABCDEF", p.peek()) >= 0 {
		if (p.peek() == '+' || p.peek() == '-') && !strings.ContainsRune("eE", rune(p.src[p.pos-1])) {
			break
		}
		p.pos++
	}
	text := strings.ReplaceAll(p.src[start:p.pos], "_", "")
	if f, err := strconv.ParseFloat(text, 64); err == nil {
		return f
	}
	return unknownValue{}
}

// identExpr handles keywords, Python string prefixes, Rust macros and
// struct literals, and identifier chains.
func (p *litParser) identExpr() any {
	word := p.ident()
	if v, ok := p.keyword(word); ok {
		return v
	}
	if v, ok := p.prefixedLiteral(word); ok {
		return v
	}
	if p.lang == langJS && word == "new" {
		p.ws()
		word = p.ident()
	}
	ch := &chainValue{segments: []chainSegment{{name: word}}}
	if p.lang == langRust && p.peekWS() == '{' && isUpper(word) {
		p.ws()
		return p.object()
	}
	return ch
}

// prefixedLiteral handles a word that introduces a literal: a Python
// string prefix (r"..", f"..") or a Rust macro (json!, vec!).
func (p *litParser) prefixedLiteral(word string) (any, bool) {
	switch {
	case p.lang == langPython && isPyStringPrefix(word) && (p.peek() == '"' || p.peek() == '\''):
		return p.stringLit(), true
	case p.lang == langRust && p.peek() == '!':
		return p.rustMacro(word), true
	}
	return nil, false
}

func isUpper(s string) bool { return s != "" && s[0] >= 'A' && s[0] <= 'Z' }

func (p *litParser) peekWS() byte {
	save := p.pos
	p.ws()
	c := p.peek()
	p.pos = save
	return c
}

func (p *litParser) keyword(word string) (any, bool) {
	switch word {
	case "true", "True":
		return true, true
	case "false", "False":
		return false, true
	case "null", "None", "undefined":
		return nil, true
	}
	return nil, false
}

func isPyStringPrefix(w string) bool {
	switch strings.ToLower(w) {
	case "r", "u", "b", "f", "rb", "br", "fr", "rf":
		return true
	}
	return false
}

// rustMacro parses json!(...) / vec![...] as their literal content.
func (p *litParser) rustMacro(name string) any {
	p.pos++ // '!'
	p.ws()
	switch {
	case p.peek() == '{' || p.peek() == '[':
		return p.primary()
	case p.peek() == '(' && name == "json":
		p.pos++
		v := p.value()
		p.ws()
		if p.peek() == ')' {
			p.pos++
		}
		return v
	}
	p.skipExpr()
	return unknownValue{}
}

// postfix applies .name, ::name, and (args) links to v.
func (p *litParser) postfix(v any) any {
	for {
		p.wsInline()
		switch {
		case p.memberLink():
			v = appendSegment(v, chainSegment{name: p.ident()})
		case p.peek() == '(':
			v = p.callOn(v)
		case p.peek() == '[' && p.lang == langPython:
			v = p.subscriptOn(v)
		default:
			return v
		}
	}
}

// subscriptOn parses a Python subscript such as Annotated[str, Field()].
func (p *litParser) subscriptOn(v any) any {
	items, _ := p.array().([]any)
	ch, ok := v.(*chainValue)
	if !ok || len(ch.segments) == 0 {
		return unknownValue{}
	}
	ch.segments[len(ch.segments)-1].index = items
	return ch
}

// wsInline skips whitespace before a postfix link. Python chains don't
// continue across lines outside brackets, so there only blanks count.
func (p *litParser) wsInline() {
	if p.lang != langPython {
		p.ws()
		return
	}
	for p.peek() == ' ' || p.peek() == '\t' {
		p.pos++
	}
}

// memberLink consumes ".", "?.", or Rust "::" before a member name.
func (p *litParser) memberLink() bool {
	c, n := p.peek(), p.peekNext()
	switch {
	case c == '.' && isIdentStart(n):
		p.pos++
	case (c == '?' && n == '.') || (c == ':' && n == ':' && p.lang == langRust):
		p.pos += 2
	default:
		return false
	}
	return true
}

func appendSegment(v any, seg chainSegment) any {
	ch, ok := v.(*chainValue)
	if !ok {
		ch = &chainValue{base: v}
	}
	ch.segments = append(ch.segments, seg)
	return ch
}

func (p *litParser) callOn(v any) any {
	args, kwargs := p.callArgs()
	ch, ok := v.(*chainValue)
	if !ok || len(ch.segments) == 0 {
		return unknownValue{}
	}
	last := &ch.segments[len(ch.segments)-1]
	if last.called {
		ch.segments = append(ch.segments, chainSegment{name: "", called: true, args: args, kwargs: kwargs})
		return ch
	}
	last.called, last.args, last.kwargs = true, args, kwargs
	return ch
}

// callArgs parses "(a, b, key=value)". Keyword arguments are Python-only.
func (p *litParser) callArgs() ([]any, map[string]any) {
	p.pos++ // '('
	p.depth++
	defer func() { p.depth-- }()
	var args []any
	var kwargs map[string]any
	for {
		p.ws()
		if p.eof() {
			return args, kwargs
		}
		if p.peek() == ')' {
			p.pos++
			return args, kwargs
		}
		start := p.pos
		if name, ok := p.kwargName(); ok {
			if kwargs == nil {
				kwargs = map[string]any{}
			}
			kwargs[name] = p.value()
		} else {
			args = append(args, p.value())
		}
		p.separator(start)
	}
}

// separator consumes the ',' after a list element. When the element
// made no progress — a ';' as in Rust's [0; 4] or a TypeScript type
// literal, or a stray closer — it skips one byte so loops terminate.
func (p *litParser) separator(start int) {
	p.ws()
	switch {
	case p.peek() == ',':
		p.pos++
	case p.pos == start && !p.eof():
		p.pos++
	case p.peek() == ';':
		p.pos++
	}
}

func (p *litParser) kwargName() (string, bool) {
	if p.lang != langPython || !isIdentStart(p.peek()) {
		return "", false
	}
	save := p.pos
	name := p.ident()
	p.ws()
	if p.peek() == '=' && p.peekNext() != '=' {
		p.pos++
		return name, true
	}
	p.pos = save
	return "", false
}

// concat folds `"a" + "b"` and Python's implicit `"a" "b"`.
func (p *litParser) concat(v any) any {
	s, ok := v.(string)
	if !ok {
		return v
	}
	for {
		save := p.pos
		if !p.concatOperator() {
			p.pos = save
			return s
		}
		next, ok := p.stringLit().(string)
		if !ok {
			return unknownValue{}
		}
		s += next
	}
}

// concatOperator consumes a "+" (or, in Python, nothing) and reports
// whether a string literal follows.
func (p *litParser) concatOperator() bool {
	p.ws()
	if p.peek() == '+' && p.peekNext() != '+' {
		p.pos++
		p.ws()
		return p.isQuote(p.peek())
	}
	return p.lang == langPython && (p.peek() == '"' || p.peek() == '\'')
}

func (p *litParser) array() any {
	p.pos++ // '['
	p.depth++
	defer func() { p.depth-- }()
	out := []any{}
	for {
		p.ws()
		if p.eof() {
			return unknownValue{}
		}
		if p.peek() == ']' {
			p.pos++
			return out
		}
		start := p.pos
		out = append(out, p.value())
		p.separator(start)
	}
}

// object parses {key: value, ...}. JS shorthand properties become
// identifier chains; spreads and computed keys are recorded as unknown.
func (p *litParser) object() any {
	p.pos++ // '{'
	p.depth++
	defer func() { p.depth-- }()
	out := map[string]any{}
	for {
		p.ws()
		if p.eof() {
			return unknownValue{}
		}
		if p.peek() == '}' {
			p.pos++
			return out
		}
		start := p.pos
		p.objectEntry(out)
		p.separator(start)
	}
}

func (p *litParser) objectEntry(out map[string]any) {
	if strings.HasPrefix(p.src[p.pos:], "..") {
		p.skipExpr()
		out[spreadKey] = unknownValue{}
		return
	}
	key, ok := p.objectKey()
	p.ws()
	switch {
	case ok && p.peek() == ':' && p.peekNext() != ':':
		p.pos++
		out[key] = p.value()
	case ok && (p.peek() == ',' || p.peek() == '}'):
		out[key] = &chainValue{segments: []chainSegment{{name: key}}}
	default:
		p.skipExpr()
	}
}

// spreadKey marks an object built with ...spread, whose full contents
// are unknown.
const spreadKey = "\x00spread"

func (p *litParser) objectKey() (string, bool) {
	c := p.peek()
	switch {
	case c == '"' || c == '\'' || c == '`':
		s, ok := p.stringLit().(string)
		return s, ok
	case isIdentStart(c):
		return p.ident(), true
	case c >= '0' && c <= '9':
		start := p.pos
		p.number()
		return p.src[start:p.pos], true
	}
	return "", false
}

// stringLit parses a quoted string, including Python triple quotes and
// JS template literals without substitutions.
func (p *litParser) stringLit() any {
	q := p.peek()
	if p.lang == langPython && strings.HasPrefix(p.src[p.pos:], strings.Repeat(string(q), 3)) {
		return p.tripleString(q)
	}
	p.pos++
	var b strings.Builder
	for !p.eof() {
		if v, done := p.stringChar(q, &b); done {
			return v
		}
	}
	return unknownValue{}
}

// stringChar consumes one character (or escape) of a string quoted by q
// and reports the string's value once it ends.
func (p *litParser) stringChar(q byte, b *strings.Builder) (any, bool) {
	c := p.peek()
	switch {
	case c == q:
		p.pos++
		return b.String(), true
	case c == '\\':
		b.WriteString(p.escape())
	case c == '$' && q == '`' && p.peekNext() == '{':
		p.skipTemplateRest()
		return unknownValue{}, true
	case c == '\n' && q != '`':
		return unknownValue{}, true
	default:
		b.WriteByte(c)
		p.pos++
	}
	return nil, false
}

func (p *litParser) tripleString(q byte) any {
	delim := strings.Repeat(string(q), 3)
	p.pos += 3
	end := strings.Index(p.src[p.pos:], delim)
	if end < 0 {
		p.pos = len(p.src)
		return unknownValue{}
	}
	s := p.src[p.pos : p.pos+end]
	p.pos += end + 3
	return s
}

var escapes = map[byte]string{'n': "\n", 't': "\t", 'r': "\r", '\\': "\\", '"': "\"", '\'': "'", '`': "`", '0': "\x00", '\n': ""}

func (p *litParser) escape() string {
	p.pos++ // backslash
	if p.eof() {
		return ""
	}
	c := p.peek()
	p.pos++
	if s, ok := escapes[c]; ok {
		return s
	}
	if c == 'u' && p.pos+4 <= len(p.src) {
		if r, ok := hexRune(p.src[p.pos : p.pos+4]); ok {
			p.pos += 4
			return string(r)
		}
	}
	return "\\" + string(c)
}

func hexRune(digits string) (rune, bool) {
	var r rune
	for _, c := range digits {
		lower := c | 0x20
		switch {
		case c >= '0' && c <= '9':
			r = r*16 + (c - '0')
		case lower >= 'a' && lower <= 'f':
			r = r*16 + (lower - 'a' + 10)
		default:
			return 0, false
		}
	}
	return r, true
}

func (p *litParser) skipTemplateRest() {
	for !p.eof() {
		switch p.peek() {
		case '`':
			p.pos++
			return
		case '\\':
			p.pos += 2
		case '{':
			p.skipBalanced()
		default:
			p.pos++
		}
	}
}

func (p *litParser) regex() any {
	start := p.pos
	p.pos++
	inClass := false
	for !p.eof() {
		switch c := p.peek(); {
		case c == '\\':
			p.pos++
		case c == '[':
			inClass = true
		case c == ']':
			inClass = false
		case c == '/' && !inClass:
			pattern := p.src[start+1 : p.pos]
			p.pos++
			p.ident() // flags
			return regexValue{pattern: pattern}
		case c == '\n':
			return unknownValue{}
		}
		p.pos++
	}
	return unknownValue{}
}

// skipExpr advances to the next ',' or closing bracket at depth zero.
func (p *litParser) skipExpr() {
	for !p.eof() {
		switch p.peek() {
		case ',', ')', ']', '}', ';':
			return
		case '(', '[', '{':
			p.skipBalanced()
		default:
			p.skipAtom()
		}
	}
}

// skipAtom advances past one character, string, or comment.
func (p *litParser) skipAtom() {
	switch {
	case p.isQuote(p.peek()):
		p.stringLit()
	case p.skipComment():
	default:
		p.pos++
	}
}

// skipBalanced advances past a bracketed group starting at pos.
func (p *litParser) skipBalanced() {
	depth := 0
	for !p.eof() {
		switch p.peek() {
		case '(', '[', '{':
			depth++
			p.pos++
		case ')', ']', '}':
			depth--
			p.pos++
			if depth <= 0 {
				return
			}
		default:
			p.skipAtom()
		}
	}
}

// enclosingOpen returns, for each offset in targets (ascending), the
// offset of the innermost unclosed bracket at that point if it is a '{',
// or -1. Strings and comments are skipped so brackets inside them don't
// count.
func enclosingOpen(src string, l lang, targets []int) []int {
	out := make([]int, len(targets))
	p := newLitParser(src, 0, l)
	var stack []int
	ti := 0
	for !p.eof() && ti < len(targets) {
		for ti < len(targets) && targets[ti] <= p.pos {
			out[ti] = topOf(stack, src, '{')
			ti++
		}
		stack = p.stepStructure(stack)
	}
	for ; ti < len(targets); ti++ {
		out[ti] = -1
	}
	return out
}

// stepStructure advances one token, maintaining a stack of open
// bracket offsets.
func (p *litParser) stepStructure(stack []int) []int {
	switch c := p.peek(); c {
	case '(', '[', '{':
		stack = append(stack, p.pos)
		p.pos++
	case ')', ']', '}':
		if len(stack) > 0 {
			stack = stack[:len(stack)-1]
		}
		p.pos++
	default:
		p.skipAtom()
	}
	return stack
}

// topOf returns the innermost open bracket if it is of kind open.
func topOf(stack []int, src string, open byte) int {
	if len(stack) == 0 || src[stack[len(stack)-1]] != open {
		return -1
	}
	return stack[len(stack)-1]
}

// asString returns v as a string when it is a string literal, or a Rust
// conversion of one ("x".into(), "x".to_string(), String::from("x")).
func asString(v any) (string, bool) {
	switch t := v.(type) {
	case string:
		return t, true
	case *chainValue:
		return chainString(t)
	}
	return "", false
}

func chainString(ch *chainValue) (string, bool) {
	if s, ok := ch.base.(string); ok {
		for _, seg := range ch.segments {
			switch seg.name {
			case "into", "to_string", "to_owned", "into_owned":
			default:
				return "", false
			}
		}
		return s, true
	}
	n := len(ch.segments)
	if n >= 1 && ch.segments[n-1].called && len(ch.segments[n-1].args) == 1 {
		switch ch.chainName() {
		case "String::from", "String.from", "Some", "Cow::from", "Cow::Borrowed", "str", "String":
			return asString(ch.segments[n-1].args[0])
		}
	}
	return "", false
}

// chainName joins the segment names with "::" for Rust paths or "." for
// other languages, ignoring calls: z.string().describe → "z.string.describe".
func (ch *chainValue) chainName() string {
	names := make([]string, 0, len(ch.segments))
	for _, s := range ch.segments {
		names = append(names, s.name)
	}
	joined := strings.Join(names, ".")
	if strings.HasPrefix(joined, "String.") || strings.HasPrefix(joined, "Cow.") {
		return strings.Replace(joined, ".", "::", 1)
	}
	return joined
}

// toJSONValue converts a parsed literal to plain JSON data, reporting
// false if any part is not a literal.
func toJSONValue(v any) (any, bool) {
	switch t := v.(type) {
	case nil, bool, float64, string:
		return t, true
	case []any:
		out := make([]any, 0, len(t))
		for _, e := range t {
			ev, ok := toJSONValue(e)
			if !ok {
				return nil, false
			}
			out = append(out, ev)
		}
		return out, true
	case map[string]any:
		return toJSONObject(t)
	case *chainValue:
		if s, ok := chainString(t); ok {
			return s, true
		}
	}
	return nil, false
}

func toJSONObject(m map[string]any) (any, bool) {
	if _, spread := m[spreadKey]; spread {
		return nil, false
	}
	out := make(map[string]any, len(m))
	for k, e := range m {
		ev, ok := toJSONValue(e)
		if !ok {
			return nil, false
		}
		out[k] = ev
	}
	return out, true
}

// lineAt returns the 1-indexed line of offset in src.
func lineAt(src string, offset int) int {
	offset = max(0, min(offset, len(src)))
	return strings.Count(src[:offset], "\n") + 1
}
