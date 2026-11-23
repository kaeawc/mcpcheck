// Rust intake — static extractor for MCP tools declared in Rust sources.
// It works on a normalized copy of each file (see rustSource) and the
// shared literal parser, without a full Rust parser.
//
// Recognized patterns:
//
//	#[tool_router]                          // rmcp (official SDK)
//	impl Server {
//	    /// Doc lines become the description when description= is absent.
//	    #[tool(name = "x", title = "X", description = "...",
//	           annotations(read_only_hint = true, ...))]
//	    async fn f(&self, Parameters(p): Parameters<Args>) -> ... { }
//	}
//	#[tool(tool_box)] impl ... with #[tool(param)] / #[tool(aggr)] args   // older rmcp
//	Tool::new("x", "desc", schema).with_title(..).with_annotations(..)   // rmcp model
//	Tool { name: "x".into(), description: Some(..), input_schema: .., .. }
//	json!({"name": "x", "description": "...", "inputSchema": {...}})    // hand-rolled
//
// Input schemas come from the Parameters<T> struct (fields, Option, serde
// rename/rename_all/default/skip/flatten, schemars description, /// docs,
// unit-variant enums), collected across every file of the crate, or from
// json!/object! literals and serde_json::from_str of a string literal,
// directly or through a let/const binding.
//
// Limitations:
//   - Option<T> is reported as T (not required) rather than schemars'
//     T-or-null; nested types are inlined instead of using $defs.
//   - Types from other crates, generic parameters, macro-generated items,
//     and enums with data or serde tagging are left unconstrained ({}).
//   - Tools registered through runtime values (a description built with
//     format!, a schema computed elsewhere) keep those fields empty.
//   - Items under #[cfg(test)] mod blocks are ignored.
package scanner

import (
	"encoding/json"
	"fmt"
	"os"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/kaeawc/mcpcheck/internal/mcpmodel"
)

func isRustSourceFile(path string) bool { return strings.HasSuffix(path, ".rs") }

// LoadRust scans a .rs file or a directory tree for tool registrations.
// Type definitions are gathered from every file before tools are
// resolved, since a parameter struct often lives in another module.
func LoadRust(path string) (*mcpmodel.ToolSet, error) {
	sc := newRustScan()
	err := walkSources(path, isRustSourceFile, func(p string) error {
		raw, err := os.ReadFile(p) // #nosec G304 -- scanning user-named sources is the point
		if err != nil {
			return fmt.Errorf("read %s: %w", p, err)
		}
		sc.addFile(p, string(raw))
		return nil
	})
	if err != nil {
		return nil, err
	}
	return &mcpmodel.ToolSet{Source: path, Tools: sc.tools()}, nil
}

// ---- normalization ----

// rustSource holds two views of a file with identical byte offsets and
// line breaks. norm is what the literal parser reads: lifetimes are
// blanked (the parser would take 'a as a char literal), and strings it
// can't read — raw strings, escapes, multi-line strings — are decoded here
// and replaced by an index into strs. code additionally blanks comments
// and string contents, so regex and bracket scans over it only see code.
type rustSource struct {
	path string
	norm string
	code string
	strs []string
}

const rustPlaceholder = '\x01'

type rustNormalizer struct {
	src        string
	i          int
	norm, code []byte
	strs       []string
}

func newRustSource(path, src string) *rustSource {
	n := &rustNormalizer{src: src, norm: []byte(src), code: []byte(src)}
	for n.i < len(src) {
		n.step()
	}
	n.unqualifyMacros()
	return &rustSource{path: path, norm: string(n.norm), code: string(n.code), strs: n.strs}
}

func (n *rustNormalizer) at(off int) byte {
	if n.i+off < len(n.src) {
		return n.src[n.i+off]
	}
	return 0
}

func (n *rustNormalizer) step() {
	c := n.src[n.i]
	switch {
	case c == '/' && n.at(1) == '/':
		n.blankCode(n.i, rustLineEnd(n.src, n.i))
		n.i = rustLineEnd(n.src, n.i)
	case c == '/' && n.at(1) == '*':
		end := n.blockEnd()
		n.blankCode(n.i, end)
		n.i = end
	case c == '"':
		n.plainString(n.i)
	case c == '\'':
		n.quote()
	case isIdentStart(c):
		n.word()
	default:
		n.i++
	}
}

func rustLineEnd(s string, pos int) int {
	if i := strings.IndexByte(s[pos:], '\n'); i >= 0 {
		return pos + i
	}
	return len(s)
}

// blockEnd returns the offset after the (possibly nested) block comment
// starting at i.
func (n *rustNormalizer) blockEnd() int {
	depth := 0
	for j := n.i; j+1 < len(n.src); j++ {
		switch n.src[j : j+2] {
		case "/*":
			depth++
			j++
		case "*/":
			depth--
			j++
			if depth == 0 {
				return j + 1
			}
		}
	}
	return len(n.src)
}

func (n *rustNormalizer) blankCode(from, to int) {
	for k := from; k < to && k < len(n.code); k++ {
		if n.code[k] != '\n' {
			n.code[k] = ' '
		}
	}
}

func (n *rustNormalizer) word() {
	start := n.i
	for n.i < len(n.src) && isIdentChar(n.src[n.i]) {
		n.i++
	}
	switch n.src[start:n.i] {
	case "r", "br", "cr":
		if n.at(0) == '"' || n.at(0) == '#' {
			n.rawString(start)
		}
	case "b", "c":
		if n.at(0) == '"' {
			n.plainString(n.i)
		}
	}
}

// rawString handles r"..." and r#"..."#; an r# not followed by a quote is
// a raw identifier and is left alone.
func (n *rustNormalizer) rawString(start int) {
	j := n.i
	for j < len(n.src) && n.src[j] == '#' {
		j++
	}
	if j >= len(n.src) || n.src[j] != '"' {
		return
	}
	closer := `"` + strings.Repeat("#", j-n.i)
	content, end := n.src[j+1:], len(n.src)
	if k := strings.Index(n.src[j+1:], closer); k >= 0 {
		content, end = n.src[j+1:j+1+k], j+1+k+len(closer)
	}
	n.emitString(start, end, content)
	n.i = end
}

func (n *rustNormalizer) plainString(q int) {
	j := q + 1
	for j < len(n.src) && n.src[j] != '"' {
		if n.src[j] == '\\' {
			j++
		}
		j++
	}
	end := min(j+1, len(n.src))
	content := n.src[q+1 : min(j, len(n.src))]
	if strings.ContainsAny(content, "\\\n") {
		n.emitString(q, end, rustUnescape(content))
	} else {
		n.blankCode(q+1, end-1)
	}
	n.i = end
}

// emitString rewrites src[start:end] as a plain literal the parser can
// read, keeping its length and newline count so offsets and lines after
// it are unchanged.
func (n *rustNormalizer) emitString(start, end int, s string) {
	span := n.src[start:end]
	nl := strings.Count(span, "\n")
	avail := len(span) - nl
	rep := `""`
	ph := `"` + string(rustPlaceholder) + strconv.Itoa(len(n.strs)) + `"`
	switch {
	case !strings.ContainsAny(s, "\"\\\n") && len(s)+2 <= avail:
		rep = `"` + s + `"`
	case len(ph) <= avail:
		n.strs = append(n.strs, s)
		rep = ph
	}
	fill := func(dst []byte, lit string) {
		copy(dst[start:], lit)
		k := start + len(lit)
		for ; k < start+len(lit)+nl; k++ {
			dst[k] = '\n'
		}
		for ; k < end; k++ {
			dst[k] = ' '
		}
	}
	fill(n.norm, rep)
	fill(n.code, `""`)
}

// quote tells char literals ('x', '\n') from lifetimes and labels ('a),
// blanking the latter in both views.
func (n *rustNormalizer) quote() {
	if n.at(1) == '\\' {
		if j := strings.IndexByte(n.src[min(n.i+3, len(n.src)):], '\''); j >= 0 && j < 12 {
			n.blankCode(n.i+1, n.i+3+j)
			n.i += 3 + j + 1
			return
		}
		n.i++
		return
	}
	r, sz := utf8.DecodeRuneInString(n.src[min(n.i+1, len(n.src)):])
	if sz > 0 && r != '\'' && n.at(1+sz) == '\'' {
		n.blankCode(n.i+1, n.i+1+sz)
		n.i += sz + 2
		return
	}
	end := n.i + 1
	for end < len(n.src) && isIdentChar(n.src[end]) {
		end++
	}
	for k := n.i; k < end; k++ {
		n.norm[k], n.code[k] = ' ', ' '
	}
	n.i = max(end, n.i+1)
}

var rustMacroPathRe = regexp.MustCompile(`((?:\b[A-Za-z_][A-Za-z0-9_]*\s*::\s*)*)\b(json|object)![ \t]*[({\[]`)

// unqualifyMacros rewrites serde_json::json!(...) and rmcp's object!(...)
// to json!(...), the only spelling the literal parser evaluates.
func (n *rustNormalizer) unqualifyMacros() {
	for _, m := range rustMacroPathRe.FindAllSubmatchIndex(n.code, -1) {
		for k := m[2]; k < m[3]; k++ {
			if n.code[k] != '\n' {
				n.norm[k], n.code[k] = ' ', ' '
			}
		}
		if string(n.code[m[4]:m[5]]) == "object" {
			copy(n.norm[m[4]:], "  json")
			copy(n.code[m[4]:], "  json")
		}
	}
}

var rustSimpleEscapes = map[byte]byte{'n': '\n', 't': '\t', 'r': '\r', '0': 0, '\\': '\\', '"': '"', '\'': '\''}

func rustUnescape(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		if s[i] != '\\' || i+1 >= len(s) {
			b.WriteByte(s[i])
			continue
		}
		i = rustEscape(&b, s, i+1)
	}
	return b.String()
}

// rustEscape decodes the escape whose letter is at s[i] and returns the
// index of its last byte.
func rustEscape(b *strings.Builder, s string, i int) int {
	c := s[i]
	if r, ok := rustSimpleEscapes[c]; ok {
		b.WriteByte(r)
		return i
	}
	if c == '\n' || c == '\r' {
		for i+1 < len(s) && isRustSpace(s[i+1]) {
			i++
		}
		return i
	}
	if r, last, ok := rustHexEscape(s, i); ok {
		b.WriteRune(r)
		return last
	}
	b.WriteByte('\\')
	b.WriteByte(c)
	return i
}

// rustHexEscape decodes \x41 and \u{1F600} escapes.
func rustHexEscape(s string, i int) (rune, int, bool) {
	digits, last := "", i
	switch {
	case s[i] == 'x' && i+2 < len(s):
		digits, last = s[i+1:i+3], i+2
	case strings.HasPrefix(s[i:], "u{"):
		end := strings.IndexByte(s[i:], '}')
		if end < 0 {
			return 0, i, false
		}
		digits, last = s[i+2:i+end], i+end
	}
	v, err := strconv.ParseInt(digits, 16, 32)
	if err != nil || v < 0 || v > utf8.MaxRune {
		return 0, i, false
	}
	return rune(v), last, true
}

func isRustSpace(c byte) bool { return c == ' ' || c == '\t' || c == '\n' || c == '\r' }

// resolve swaps string placeholders in a parsed value for their text.
func (rs *rustSource) resolve(v any) any {
	switch t := v.(type) {
	case string:
		return rs.resolveString(t)
	case []any:
		for i, e := range t {
			t[i] = rs.resolve(e)
		}
	case map[string]any:
		out := make(map[string]any, len(t))
		for k, e := range t {
			out[rs.resolveString(k)] = rs.resolve(e)
		}
		return out
	case *chainValue:
		t.base = rs.resolve(t.base)
		for i := range t.segments {
			seg := &t.segments[i]
			for j, a := range seg.args {
				seg.args[j] = rs.resolve(a)
			}
		}
	}
	return v
}

func (rs *rustSource) resolveString(s string) string {
	if len(s) < 2 || s[0] != rustPlaceholder {
		return s
	}
	if i, err := strconv.Atoi(s[1:]); err == nil && i < len(rs.strs) {
		return rs.strs[i]
	}
	return s
}

func (rs *rustSource) parseValue(pos int) any {
	return rs.resolve(newLitParser(rs.norm, pos, langRust).value())
}

// ---- structural helpers over code ----

func (rs *rustSource) skipBack(pos int) int {
	for pos > 0 && isRustSpace(rs.code[pos-1]) {
		pos--
	}
	return pos
}

func (rs *rustSource) skipFwd(pos int) int {
	for pos < len(rs.code) && isRustSpace(rs.code[pos]) {
		pos++
	}
	return pos
}

// matchClose returns the offset of the bracket closing the one at open.
func (rs *rustSource) matchClose(open int) int {
	depth := 0
	for i := open; i < len(rs.code); i++ {
		switch rs.code[i] {
		case '(', '[', '{':
			depth++
		case ')', ']', '}':
			depth--
			if depth == 0 {
				return i
			}
		}
	}
	return -1
}

// matchOpen returns the offset of the bracket opening the one at closeAt.
func (rs *rustSource) matchOpen(closeAt int) int {
	depth := 0
	for i := closeAt; i >= 0; i-- {
		switch rs.code[i] {
		case ')', ']', '}':
			depth++
		case '(', '[', '{':
			depth--
			if depth == 0 {
				return i
			}
		}
	}
	return -1
}

// wordBefore returns the identifier ending at end and its start.
func (rs *rustSource) wordBefore(end int) (string, int) {
	start := end
	for start > 0 && isIdentChar(rs.code[start-1]) {
		start--
	}
	return rs.code[start:end], start
}

// preludeStart walks back from an item keyword over visibility and
// qualifier words and attributes, returning the offset just after the
// previous token. Doc comments in between are read forward later.
func (rs *rustSource) preludeStart(kw int) int {
	pos := kw
	for {
		p := rs.skipBack(pos)
		next := rs.backOverModifier(p)
		if next < 0 {
			next = rs.backOverAttr(p)
		}
		if next < 0 {
			return p
		}
		pos = next
	}
}

func (rs *rustSource) backOverModifier(p int) int {
	if p > 0 && rs.code[p-1] == ')' {
		open := rs.matchOpen(p - 1)
		if open < 0 {
			return -1
		}
		if w, s := rs.wordBefore(rs.skipBack(open)); w == "pub" {
			return s
		}
		return -1
	}
	switch w, s := rs.wordBefore(p); w {
	case "pub", "async", "const", "unsafe", "extern", "default":
		return s
	}
	return -1
}

func (rs *rustSource) backOverAttr(p int) int {
	if p == 0 || rs.code[p-1] != ']' {
		return -1
	}
	open := rs.matchOpen(p - 1)
	if open < 1 || rs.code[open-1] != '#' {
		return -1
	}
	return open - 1
}

// splitTop returns the comma-separated pieces of code[start:end] at
// bracket depth zero, treating <...> as brackets.
func (rs *rustSource) splitTop(start, end int) [][2]int {
	var out [][2]int
	for _, r := range rustSplitRanges(rs.code[start:end], ',') {
		out = appendNonBlank(out, rs.code, start+r[0], start+r[1])
	}
	return out
}

// rustBracketDelta reports how s[i] changes bracket depth. Angle brackets
// count, except the > of ->.
func rustBracketDelta(s string, i int) int {
	switch s[i] {
	case '(', '[', '{', '<':
		return 1
	case ')', ']', '}':
		return -1
	case '>':
		if i > 0 && s[i-1] == '-' {
			return 0
		}
		return -1
	}
	return 0
}

// rustSplitRanges splits s at sep bytes outside brackets.
func rustSplitRanges(s string, sep byte) [][2]int {
	var out [][2]int
	depth, from := 0, 0
	for i := 0; i < len(s); i++ {
		depth += rustBracketDelta(s, i)
		if s[i] == sep && depth == 0 {
			out = append(out, [2]int{from, i})
			from = i + 1
		}
	}
	return append(out, [2]int{from, len(s)})
}

func appendNonBlank(out [][2]int, s string, from, to int) [][2]int {
	if strings.TrimSpace(s[from:to]) == "" {
		return out
	}
	return append(out, [2]int{from, to})
}

// ---- attributes and doc comments ----

// rustMeta is one item of an attribute argument list: `key`,
// `key = value`, or `key(list...)`.
type rustMeta struct {
	key      string
	value    any
	list     []rustMeta
	hasValue bool
	hasList  bool
}

type rustAttr struct {
	path   string
	offset int
	args   []rustMeta
	value  any
}

func (a *rustAttr) is(name string) bool { return rustLastSeg(a.path) == name }

// rustPreludeItem is a doc comment line or an attribute, in source order.
type rustPreludeItem struct {
	isDoc bool
	doc   string
	attr  *rustAttr
}

// readPrelude reads doc comments and attributes from pos up to end and
// returns them with the offset of the first other token.
func (rs *rustSource) readPrelude(pos, end int) ([]rustPreludeItem, int) {
	var items []rustPreludeItem
	for pos < end {
		next, item, ok := rs.preludeStep(pos, end)
		if !ok {
			break
		}
		if item != nil {
			items = append(items, *item)
		}
		pos = next
	}
	return items, pos
}

func (rs *rustSource) preludeStep(pos, end int) (int, *rustPreludeItem, bool) {
	rest := rs.norm[pos:]
	switch {
	case isRustSpace(rest[0]):
		return pos + 1, nil, true
	case strings.HasPrefix(rest, "///") && !strings.HasPrefix(rest, "////"):
		e := rustLineEnd(rs.norm, pos)
		return e, &rustPreludeItem{isDoc: true, doc: strings.TrimSuffix(rs.norm[pos+3:e], "\r")}, true
	case strings.HasPrefix(rest, "//"):
		return rustLineEnd(rs.norm, pos), nil, true
	case strings.HasPrefix(rest, "/*"):
		if e := strings.Index(rest, "*/"); e >= 0 {
			return pos + e + 2, nil, true
		}
		return end, nil, true
	case strings.HasPrefix(rest, "#["):
		closeAt := rs.matchClose(pos + 1)
		if closeAt < 0 || closeAt >= end {
			return pos, nil, false
		}
		return closeAt + 1, rs.attrItem(rs.parseAttr(pos)), true
	}
	return pos, nil, false
}

func (rs *rustSource) attrItem(a *rustAttr) *rustPreludeItem {
	if s, ok := a.value.(string); ok && a.path == "doc" {
		return &rustPreludeItem{isDoc: true, doc: s}
	}
	return &rustPreludeItem{attr: a}
}

// parseAttr parses the attribute whose `#` is at pos.
func (rs *rustSource) parseAttr(pos int) *rustAttr {
	p := newLitParser(rs.norm, pos+2, langRust)
	p.ws()
	a := &rustAttr{offset: pos, path: rustMetaPath(p)}
	p.ws()
	switch p.peek() {
	case '(':
		a.args = rs.metaList(p)
	case '=':
		p.pos++
		a.value = rs.resolve(p.value())
	}
	return a
}

func rustMetaPath(p *litParser) string {
	var b strings.Builder
	for {
		if strings.HasPrefix(p.src[p.pos:], "r#") {
			p.pos += 2
		}
		b.WriteString(p.ident())
		if p.peek() != ':' || p.peekNext() != ':' {
			return b.String()
		}
		p.pos += 2
		b.WriteString("::")
	}
}

func (rs *rustSource) metaList(p *litParser) []rustMeta {
	p.pos++ // '('
	var out []rustMeta
	for {
		p.ws()
		if p.eof() || strings.IndexByte(")]}", p.peek()) >= 0 {
			if p.peek() == ')' {
				p.pos++
			}
			return out
		}
		start := p.pos
		if m, ok := rs.metaItem(p); ok {
			out = append(out, m)
		}
		p.ws()
		if p.peek() == ',' {
			p.pos++
		} else if p.pos == start {
			p.skipExpr()
		}
	}
}

func (rs *rustSource) metaItem(p *litParser) (rustMeta, bool) {
	key := rustMetaPath(p)
	if key == "" {
		p.skipExpr()
		return rustMeta{}, false
	}
	p.ws()
	m := rustMeta{key: key}
	switch p.peek() {
	case '=':
		p.pos++
		m.value, m.hasValue = rs.resolve(p.value()), true
	case '(':
		m.list, m.hasList = rs.metaList(p), true
	}
	return m, true
}

func metaFind(list []rustMeta, key string) *rustMeta {
	for i := range list {
		if list[i].key == key {
			return &list[i]
		}
	}
	return nil
}

func metaString(list []rustMeta, key string) (string, bool) {
	if m := metaFind(list, key); m != nil {
		return asString(m.value)
	}
	return "", false
}

// metaRename reads `key = "x"` or `key(deserialize = "x")`.
func metaRename(list []rustMeta, key string) (string, bool) {
	m := metaFind(list, key)
	switch {
	case m == nil:
		return "", false
	case m.hasList:
		return metaString(m.list, "deserialize")
	}
	return asString(m.value)
}

func rustLastSeg(path string) string {
	if i := strings.LastIndex(path, "::"); i >= 0 {
		return strings.TrimSpace(path[i+2:])
	}
	return strings.TrimSpace(path)
}

// serdeMetas returns the arguments of every #[serde(...)] and
// #[schemars(...)] attribute in items.
func serdeMetas(items []rustPreludeItem) []rustMeta {
	var out []rustMeta
	for _, it := range items {
		if it.attr != nil && (it.attr.is("serde") || it.attr.is("schemars")) {
			out = append(out, it.attr.args...)
		}
	}
	return out
}

// schemarsDoc mirrors schemars: doc lines lose one leading space, are
// joined with newlines and trimmed; a leading `# Heading` is the title.
func schemarsDoc(items []rustPreludeItem) (title, desc string) {
	var lines []string
	for _, it := range items {
		if it.isDoc {
			lines = append(lines, strings.TrimPrefix(it.doc, " "))
		}
	}
	doc := strings.TrimSpace(strings.Join(lines, "\n"))
	if !strings.HasPrefix(doc, "#") {
		return "", doc
	}
	head, rest, _ := strings.Cut(doc, "\n")
	return strings.TrimSpace(strings.TrimLeft(head, "#")), strings.TrimSpace(rest)
}

// ---- types ----

type rustField struct {
	name, typ   string
	title, desc string
	renamed     bool
	defaulted   bool
	skip        bool
	flatten     bool
}

type rustStruct struct {
	file        string
	fields      []rustField
	renameAll   string
	defaulted   bool
	denyUnknown bool
}

type rustEnum struct {
	file    string
	values  []any
	complex bool
}

var (
	rustStructRe = regexp.MustCompile(`\bstruct\s+([A-Za-z_][A-Za-z0-9_]*)`)
	rustEnumRe   = regexp.MustCompile(`\benum\s+([A-Za-z_][A-Za-z0-9_]*)`)
	rustFieldRe  = regexp.MustCompile(`(?s)^\s*(?:pub\s*(?:\([^)]*\))?\s+)?(?:r#)?([A-Za-z_][A-Za-z0-9_]*)\s*:([^:].*)$`)
	rustUnitRe   = regexp.MustCompile(`(?s)^\s*(?:r#)?([A-Za-z_][A-Za-z0-9_]*)\s*(?:=.*)?$`)
)

// bracedBody returns the braces of the item body that follows a type
// name at pos, or -1 for tuple and unit items.
func (rs *rustSource) bracedBody(pos int) (int, int) {
	depth := 0
	for i := pos; i < len(rs.code); i++ {
		switch c := rs.code[i]; {
		case c == '<':
			depth++
		case c == '>' && rs.code[i-1] != '-':
			depth--
		case depth > 0:
		case c == '{':
			return i, rs.matchClose(i)
		case c == '(' || c == ';' || c == '}':
			return -1, -1
		}
	}
	return -1, -1
}

func (sc *rustScan) addStruct(rs *rustSource, m []int) {
	open, closeAt := rs.bracedBody(m[1])
	if open < 0 || closeAt < 0 {
		return
	}
	items, _ := rs.readPrelude(rs.preludeStart(m[0]), m[0])
	metas := serdeMetas(items)
	st := &rustStruct{
		file:        rs.path,
		defaulted:   metaFind(metas, "default") != nil,
		denyUnknown: metaFind(metas, "deny_unknown_fields") != nil,
	}
	st.renameAll, _ = metaRename(metas, "rename_all")
	for _, seg := range rs.splitTop(open+1, closeAt) {
		if f, ok := rs.parseField(seg); ok {
			st.fields = append(st.fields, f)
		}
	}
	name := rs.code[m[2]:m[3]]
	sc.structs[name] = append(sc.structs[name], st)
}

func (rs *rustSource) parseField(seg [2]int) (rustField, bool) {
	items, pos := rs.readPrelude(seg[0], seg[1])
	m := rustFieldRe.FindStringSubmatch(rs.code[pos:seg[1]])
	if m == nil {
		return rustField{}, false
	}
	f := rustField{name: m[1], typ: strings.TrimSpace(m[2])}
	applyFieldPrelude(&f, items)
	return f, true
}

func applyFieldPrelude(f *rustField, items []rustPreludeItem) {
	f.title, f.desc = schemarsDoc(items)
	metas := serdeMetas(items)
	if s, ok := metaRename(metas, "rename"); ok {
		f.name, f.renamed = s, true
	}
	if s, ok := metaString(metas, "description"); ok {
		f.desc = s
	}
	if s, ok := metaString(metas, "title"); ok {
		f.title = s
	}
	f.defaulted = metaFind(metas, "default") != nil
	f.skip = metaFind(metas, "skip") != nil || metaFind(metas, "skip_deserializing") != nil
	f.flatten = metaFind(metas, "flatten") != nil
}

func (sc *rustScan) addEnum(rs *rustSource, m []int) {
	open, closeAt := rs.bracedBody(m[1])
	if open < 0 || closeAt < 0 {
		return
	}
	items, _ := rs.readPrelude(rs.preludeStart(m[0]), m[0])
	metas := serdeMetas(items)
	en := &rustEnum{file: rs.path, values: []any{}}
	for _, k := range []string{"tag", "untagged", "content"} {
		en.complex = en.complex || metaFind(metas, k) != nil
	}
	renameAll, _ := metaRename(metas, "rename_all")
	for _, seg := range rs.splitTop(open+1, closeAt) {
		v, unit := rs.parseVariant(seg, renameAll)
		en.complex = en.complex || !unit
		if v != "" {
			en.values = append(en.values, v)
		}
	}
	name := rs.code[m[2]:m[3]]
	sc.enums[name] = append(sc.enums[name], en)
}

// parseVariant returns the serialized name of a unit variant ("" when
// skipped) and whether the variant is a unit variant.
func (rs *rustSource) parseVariant(seg [2]int, renameAll string) (string, bool) {
	items, pos := rs.readPrelude(seg[0], seg[1])
	m := rustUnitRe.FindStringSubmatch(rs.code[pos:seg[1]])
	if m == nil {
		return "", false
	}
	metas := serdeMetas(items)
	if metaFind(metas, "skip") != nil || metaFind(metas, "skip_deserializing") != nil {
		return "", true
	}
	if s, ok := metaRename(metas, "rename"); ok {
		return s, true
	}
	return rustRenameVariant(m[1], renameAll), true
}

// rustRenameVariant applies serde's rename_all rule to a PascalCase
// variant name.
func rustRenameVariant(v, rule string) string {
	switch rule {
	case "lowercase":
		return strings.ToLower(v)
	case "UPPERCASE":
		return strings.ToUpper(v)
	case "camelCase":
		return strings.ToLower(v[:1]) + v[1:]
	case "snake_case", "SCREAMING_SNAKE_CASE", "kebab-case", "SCREAMING-KEBAB-CASE":
		return rustRenameField(rustPascalToSnake(v), rule)
	}
	return v
}

func rustPascalToSnake(v string) string {
	var b strings.Builder
	for i, r := range v {
		if r >= 'A' && r <= 'Z' {
			if i > 0 {
				b.WriteByte('_')
			}
			r += 'a' - 'A'
		}
		b.WriteRune(r)
	}
	return b.String()
}

// rustRenameField applies serde's rename_all rule to a snake_case field
// name.
func rustRenameField(f, rule string) string {
	switch rule {
	case "UPPERCASE", "SCREAMING_SNAKE_CASE":
		return strings.ToUpper(f)
	case "PascalCase", "camelCase":
		return rustSnakeToCamel(f, rule == "PascalCase")
	case "kebab-case":
		return strings.ReplaceAll(f, "_", "-")
	case "SCREAMING-KEBAB-CASE":
		return strings.ToUpper(strings.ReplaceAll(f, "_", "-"))
	}
	return f
}

func rustSnakeToCamel(f string, upperFirst bool) string {
	var b strings.Builder
	upper := upperFirst
	for _, r := range f {
		switch {
		case r == '_':
			upper = true
		case upper:
			b.WriteString(strings.ToUpper(string(r)))
			upper = false
		default:
			b.WriteRune(r)
		}
	}
	return b.String()
}

// ---- type → JSON Schema ----

const rustMaxDepth = 3

var rustScalarTypes = map[string]string{
	"String": "string", "str": "string", "char": "string", "PathBuf": "string", "Path": "string",
	"OsString": "string", "Url": "string", "Uuid": "string",
	"bool": "boolean",
	"i8":   "integer", "i16": "integer", "i32": "integer", "i64": "integer", "i128": "integer", "isize": "integer",
	"u8": "integer", "u16": "integer", "u32": "integer", "u64": "integer", "u128": "integer", "usize": "integer",
	"NonZeroU32": "integer", "NonZeroU64": "integer", "NonZeroUsize": "integer",
	"f32": "number", "f64": "number",
}

var rustSeqTypes = map[string]bool{"Vec": true, "VecDeque": true, "HashSet": true, "BTreeSet": true, "IndexSet": true, "LinkedList": true}

var rustMapTypes = map[string]bool{"HashMap": true, "BTreeMap": true, "IndexMap": true, "Map": true, "JsonObject": true}

var rustWrapperTypes = map[string]bool{"Box": true, "Arc": true, "Rc": true, "Cow": true}

// splitRustType returns the last path segment of a type and its generic
// arguments, with references, lifetimes, and dyn/impl stripped.
func splitRustType(t string) (string, []string) {
	t = rustTrimType(t)
	lt := strings.IndexByte(t, '<')
	gt := strings.LastIndexByte(t, '>')
	if lt < 0 || gt < lt {
		return rustLastSeg(t), nil
	}
	var args []string
	for _, a := range rustSplitTypeArgs(t[lt+1 : gt]) {
		if a = strings.TrimSpace(a); a != "" {
			args = append(args, a)
		}
	}
	return rustLastSeg(t[:lt]), args
}

func rustTrimType(t string) string {
	t = strings.TrimSpace(t)
	for {
		next := strings.TrimSpace(strings.TrimLeft(t, "&"))
		for _, kw := range []string{"mut ", "dyn ", "impl "} {
			next = strings.TrimSpace(strings.TrimPrefix(next, kw))
		}
		if next == t {
			return t
		}
		t = next
	}
}

func rustSplitTypeArgs(s string) []string {
	var out []string
	for _, r := range rustSplitRanges(s, ',') {
		out = append(out, s[r[0]:r[1]])
	}
	return out
}

func (sc *rustScan) typeSchema(file, typ string, depth int) (map[string]any, bool) {
	typ = rustTrimType(typ)
	switch {
	case strings.HasPrefix(typ, "["):
		elem, _, _ := strings.Cut(strings.TrimSuffix(typ[1:], "]"), ";")
		return sc.arraySchema(file, elem, depth), false
	case typ == "()":
		return map[string]any{"type": "null"}, false
	case strings.HasPrefix(typ, "("):
		return map[string]any{"type": "array"}, false
	}
	name, args := splitRustType(typ)
	if name == "Option" && len(args) == 1 {
		s, _ := sc.typeSchema(file, args[0], depth)
		return s, true
	}
	if rustWrapperTypes[name] && len(args) > 0 {
		return sc.typeSchema(file, args[len(args)-1], depth)
	}
	return sc.namedTypeSchema(file, name, args, depth), false
}

func (sc *rustScan) namedTypeSchema(file, name string, args []string, depth int) map[string]any {
	if t, ok := rustScalarTypes[name]; ok {
		return map[string]any{"type": t}
	}
	switch {
	case rustSeqTypes[name] && len(args) > 0:
		return sc.arraySchema(file, args[0], depth)
	case rustMapTypes[name]:
		s := map[string]any{"type": "object"}
		if len(args) == 2 {
			if v, _ := sc.typeSchema(file, args[1], depth); len(v) > 0 {
				s["additionalProperties"] = v
			}
		}
		return s
	}
	return sc.userTypeSchema(file, name, depth)
}

func (sc *rustScan) arraySchema(file, elem string, depth int) map[string]any {
	items, _ := sc.typeSchema(file, elem, depth)
	return map[string]any{"type": "array", "items": items}
}

func (sc *rustScan) userTypeSchema(file, name string, depth int) map[string]any {
	if en := rustLookup(sc.enums[name], file, func(e *rustEnum) string { return e.file }); en != nil && !en.complex {
		return map[string]any{"type": "string", "enum": append([]any(nil), en.values...)}
	}
	st := rustLookup(sc.structs[name], file, func(s *rustStruct) string { return s.file })
	switch {
	case st == nil:
		return map[string]any{}
	case depth >= rustMaxDepth:
		return map[string]any{"type": "object"}
	}
	return sc.structSchema(st, depth+1)
}

// rustLookup prefers a definition from the same file when a name is
// defined more than once in the crate.
func rustLookup[T any](defs []*T, file string, fileOf func(*T) string) *T {
	for _, d := range defs {
		if fileOf(d) == file {
			return d
		}
	}
	if len(defs) > 0 {
		return defs[0]
	}
	return nil
}

func (sc *rustScan) structSchema(st *rustStruct, depth int) map[string]any {
	props := map[string]any{}
	var required []any
	for _, f := range st.fields {
		switch {
		case f.skip:
		case f.flatten:
			required = sc.mergeFlatten(st.file, f, props, required, depth)
		default:
			name, s, req := sc.fieldSchema(st, f, depth)
			props[name] = s
			if req {
				required = append(required, name)
			}
		}
	}
	out := map[string]any{"type": "object", "properties": props}
	if len(required) > 0 {
		out["required"] = required
	}
	if st.denyUnknown {
		out["additionalProperties"] = false
	}
	return out
}

func (sc *rustScan) fieldSchema(st *rustStruct, f rustField, depth int) (string, map[string]any, bool) {
	s, optional := sc.typeSchema(st.file, f.typ, depth)
	if f.desc != "" {
		s["description"] = f.desc
	}
	if f.title != "" {
		s["title"] = f.title
	}
	name := f.name
	if !f.renamed {
		name = rustRenameField(name, st.renameAll)
	}
	return name, s, !optional && !f.defaulted && !st.defaulted
}

func (sc *rustScan) mergeFlatten(file string, f rustField, props map[string]any, required []any, depth int) []any {
	s, optional := sc.typeSchema(file, f.typ, depth)
	inner, _ := s["properties"].(map[string]any)
	for k, v := range inner {
		props[k] = v
	}
	if req, ok := s["required"].([]any); ok && !optional {
		required = append(required, req...)
	}
	return required
}

// ---- scan driver ----

type rustScan struct {
	sources []*rustSource
	structs map[string][]*rustStruct
	enums   map[string][]*rustEnum
}

func newRustScan() *rustScan {
	return &rustScan{structs: map[string][]*rustStruct{}, enums: map[string][]*rustEnum{}}
}

func (sc *rustScan) addFile(path, src string) {
	rs := newRustSource(path, src)
	sc.sources = append(sc.sources, rs)
	for _, m := range rustStructRe.FindAllStringSubmatchIndex(rs.code, -1) {
		sc.addStruct(rs, m)
	}
	for _, m := range rustEnumRe.FindAllStringSubmatchIndex(rs.code, -1) {
		sc.addEnum(rs, m)
	}
}

func (sc *rustScan) tools() []mcpmodel.Tool {
	var out []mcpmodel.Tool
	for _, rs := range sc.sources {
		out = append(out, sc.fileTools(rs)...)
	}
	return out
}

// rustFound is a recognized tool and the source span it was read from,
// used to keep one definition from being reported by two patterns.
type rustFound struct {
	tool       mcpmodel.Tool
	start, end int
}

func (sc *rustScan) fileTools(rs *rustSource) []mcpmodel.Tool {
	claimed := rs.testModuleSpans()
	blocked := len(claimed)
	for _, pattern := range []func(*rustSource) []rustFound{sc.attrTools, sc.toolNewCalls, sc.toolLiterals, sc.jsonTools} {
		for _, f := range pattern(rs) {
			if !rustInSpans(claimed, f.start) {
				claimed = append(claimed, f)
			}
		}
	}
	found := claimed[blocked:]
	sort.SliceStable(found, func(i, j int) bool { return found[i].start < found[j].start })
	out := make([]mcpmodel.Tool, 0, len(found))
	for _, f := range found {
		out = append(out, f.tool)
	}
	return out
}

func rustInSpans(spans []rustFound, pos int) bool {
	for _, s := range spans {
		if pos >= s.start && pos < s.end {
			return true
		}
	}
	return false
}

var rustTestModRe = regexp.MustCompile(`#\[\s*cfg\s*\(\s*test\s*\)\s*\]\s*(?:pub\s+)?mod\s+[A-Za-z_][A-Za-z0-9_]*\s*\{`)

func (rs *rustSource) testModuleSpans() []rustFound {
	var out []rustFound
	for _, m := range rustTestModRe.FindAllStringIndex(rs.code, -1) {
		if end := rs.matchClose(m[1] - 1); end > 0 {
			out = append(out, rustFound{start: m[0], end: end})
		}
	}
	return out
}

// ---- pattern: #[tool] functions (rmcp) ----

var rustFnRe = regexp.MustCompile(`\bfn\s+(?:r#)?([A-Za-z_][A-Za-z0-9_]*)`)

type rustFnSig struct {
	params [][2]int
	ret    string
	end    int
}

func (sc *rustScan) attrTools(rs *rustSource) []rustFound {
	var out []rustFound
	for _, m := range rustFnRe.FindAllStringSubmatchIndex(rs.code, -1) {
		if f, ok := sc.attrTool(rs, m); ok {
			out = append(out, f)
		}
	}
	return out
}

func (sc *rustScan) attrTool(rs *rustSource, m []int) (rustFound, bool) {
	items, _ := rs.readPrelude(rs.preludeStart(m[0]), m[0])
	ti := rustToolAttrIndex(items)
	if ti < 0 {
		return rustFound{}, false
	}
	sig, ok := rs.fnSig(m[1])
	if !ok {
		return rustFound{}, false
	}
	attr := items[ti].attr
	t := mcpmodel.Tool{Name: rs.code[m[2]:m[3]], SourceFile: rs.path, SourceLine: lineAt(rs.norm, attr.offset)}
	if s, ok := metaString(attr.args, "name"); ok {
		t.Name = s
	}
	t.Title, _ = metaString(attr.args, "title")
	t.Description = rmcpDescription(attr.args, items, ti)
	t.Annotations = rmcpAnnotations(attr.args)
	t.InputSchema = sc.rmcpInputSchema(rs, attr, sig)
	t.OutputSchema = sc.rmcpOutputSchema(rs, attr, sig)
	return rustFound{tool: t, start: attr.offset, end: sig.end}, true
}

func rustToolAttrIndex(items []rustPreludeItem) int {
	for i, it := range items {
		if it.attr != nil && it.attr.is("tool") {
			return i
		}
	}
	return -1
}

// fnSig reads the parameter list and return type after a fn name.
func (rs *rustSource) fnSig(pos int) (rustFnSig, bool) {
	open := rs.skipFwd(pos)
	if open < len(rs.code) && rs.code[open] == '<' {
		open = rs.skipFwd(rs.angleEnd(open) + 1)
	}
	if open >= len(rs.code) || rs.code[open] != '(' {
		return rustFnSig{}, false
	}
	closeAt := rs.matchClose(open)
	if closeAt < 0 {
		return rustFnSig{}, false
	}
	sig := rustFnSig{params: rs.splitTop(open+1, closeAt), end: closeAt + 1}
	if r := rs.skipFwd(closeAt + 1); strings.HasPrefix(rs.code[r:], "->") {
		sig.end = rs.retEnd(r + 2)
		sig.ret, _, _ = strings.Cut(rs.code[r+2:sig.end], " where ")
		sig.ret = strings.TrimSpace(sig.ret)
	}
	return sig, true
}

func (rs *rustSource) angleEnd(open int) int {
	depth := 0
	for i := open; i < len(rs.code); i++ {
		switch {
		case rs.code[i] == '<':
			depth++
		case rs.code[i] == '>' && rs.code[i-1] != '-':
			depth--
			if depth == 0 {
				return i
			}
		}
	}
	return len(rs.code) - 1
}

func (rs *rustSource) retEnd(pos int) int {
	depth := 0
	for i := pos; i < len(rs.code); i++ {
		if c := rs.code[i]; depth <= 0 && (c == '{' || c == ';') {
			return i
		}
		depth += rustBracketDelta(rs.code, i)
	}
	return len(rs.code)
}

// rmcpDescription mirrors rmcp-macros: description= wins; otherwise the
// fn's doc lines, trimmed, blank ones dropped, joined with newlines. The
// macro folds attributes in order and any non-doc attribute resets the
// collected lines, so docs above e.g. #[allow(..)] are discarded.
func rmcpDescription(args []rustMeta, items []rustPreludeItem, toolIdx int) string {
	if m := metaFind(args, "description"); m != nil {
		s, _ := asString(m.value)
		return s
	}
	var lines []string
	for i, it := range items {
		switch {
		case i == toolIdx:
		case it.isDoc:
			if l := strings.TrimSpace(it.doc); l != "" {
				lines = append(lines, l)
			}
		default:
			lines = nil
		}
	}
	return strings.Join(lines, "\n")
}

var rustAnnotationKeys = map[string]string{
	"title":            "title",
	"read_only_hint":   "readOnlyHint",
	"destructive_hint": "destructiveHint",
	"idempotent_hint":  "idempotentHint",
	"open_world_hint":  "openWorldHint",
}

func rmcpAnnotations(args []rustMeta) map[string]any {
	m := metaFind(args, "annotations")
	if m == nil || !m.hasList {
		return nil
	}
	out := map[string]any{}
	for _, a := range m.list {
		if key, ok := rustAnnotationKeys[a.key]; ok && a.hasValue {
			rustSetAnnotation(out, key, a.value)
		}
	}
	return out
}

func rustSetAnnotation(out map[string]any, key string, v any) {
	v = rustUnwrapSome(v)
	if b, ok := v.(bool); ok {
		out[key] = b
	} else if s, ok := asString(v); ok {
		out[key] = s
	}
}

// rustEmptyInput is rmcp's schema_for_empty_input, used for tools
// without a Parameters argument.
func rustEmptyInput() map[string]any {
	return map[string]any{"type": "object", "properties": map[string]any{}}
}

func (sc *rustScan) rmcpInputSchema(rs *rustSource, attr *rustAttr, sig rustFnSig) map[string]any {
	if m := metaFind(attr.args, "input_schema"); m != nil {
		return rs.schemaValue(m.value, attr.offset, 0)
	}
	legacy := &rustStruct{file: rs.path}
	for _, seg := range sig.params {
		items, pos := rs.readPrelude(seg[0], seg[1])
		pat, typ, ok := rustSplitParam(rs.code[pos:seg[1]])
		if !ok {
			continue
		}
		name, args := splitRustType(typ)
		switch kind := rustLegacyArg(items); {
		case name == "Parameters" && len(args) > 0:
			return sc.rootSchema(rs.path, args[0])
		case kind == "aggr":
			return sc.rootSchema(rs.path, typ)
		case kind == "param":
			f := rustField{name: rustPatternName(pat), typ: typ}
			applyFieldPrelude(&f, items)
			legacy.fields = append(legacy.fields, f)
		}
	}
	if len(legacy.fields) > 0 {
		return sc.structSchema(legacy, 0)
	}
	return rustEmptyInput()
}

// rootSchema is the input schema for a parameter type; nil when the type
// can't be resolved to an object from the scanned sources.
func (sc *rustScan) rootSchema(file, typ string) map[string]any {
	s, _ := sc.typeSchema(file, typ, 0)
	if s["type"] != "object" {
		return nil
	}
	return s
}

// rustLegacyArg reports "param" or "aggr" for pre-Parameters rmcp
// arguments marked #[tool(param)] / #[tool(aggr)].
func rustLegacyArg(items []rustPreludeItem) string {
	for _, it := range items {
		if it.attr == nil || !it.attr.is("tool") {
			continue
		}
		for _, k := range []string{"param", "aggr"} {
			if metaFind(it.attr.args, k) != nil {
				return k
			}
		}
	}
	return ""
}

// rustSplitParam splits `pattern: Type` at the first top-level colon.
func rustSplitParam(s string) (string, string, bool) {
	depth := 0
	for i := 0; i < len(s); i++ {
		depth += rustBracketDelta(s, i)
		if s[i] == ':' && depth == 0 && !rustDoubleColon(s, i) {
			return strings.TrimSpace(s[:i]), strings.TrimSpace(s[i+1:]), true
		}
	}
	return "", "", false
}

func rustDoubleColon(s string, i int) bool {
	return (i+1 < len(s) && s[i+1] == ':') || (i > 0 && s[i-1] == ':')
}

func rustPatternName(pat string) string {
	fields := strings.Fields(pat)
	if len(fields) == 0 {
		return pat
	}
	return strings.TrimPrefix(fields[len(fields)-1], "r#")
}

// rmcpOutputSchema mirrors rmcp's output_schema= attribute and its
// derivation from a Json<T> or Result<Json<T>, E> return type.
func (sc *rustScan) rmcpOutputSchema(rs *rustSource, attr *rustAttr, sig rustFnSig) map[string]any {
	if m := metaFind(attr.args, "output_schema"); m != nil {
		return rs.schemaValue(m.value, attr.offset, 0)
	}
	name, args := splitRustType(sig.ret)
	if name == "Result" && len(args) > 0 {
		name, args = splitRustType(args[0])
	}
	if name != "Json" || len(args) == 0 {
		return nil
	}
	if s, _ := sc.typeSchema(rs.path, args[0], 0); len(s) > 0 {
		return s
	}
	return nil
}

// ---- pattern: Tool::new(...) and Tool { ... } (rmcp model) ----

var (
	rustToolNewRe = regexp.MustCompile(`\bTool\s*::\s*new(?:_with_raw)?\s*\(`)
	rustToolLitRe = regexp.MustCompile(`\bTool\s*\{`)
)

func (sc *rustScan) toolNewCalls(rs *rustSource) []rustFound {
	var out []rustFound
	for _, m := range rustToolNewRe.FindAllStringIndex(rs.code, -1) {
		if f, ok := sc.toolNew(rs, m[0]); ok {
			out = append(out, f)
		}
	}
	return out
}

func (sc *rustScan) toolNew(rs *rustSource, at int) (rustFound, bool) {
	p := newLitParser(rs.norm, at, langRust)
	ch, ok := rs.resolve(p.postfix(p.primary())).(*chainValue)
	if !ok || len(ch.segments) < 2 || len(ch.segments[1].args) < 2 {
		return rustFound{}, false
	}
	args := ch.segments[1].args
	name, ok := asString(args[0])
	if !ok {
		return rustFound{}, false
	}
	t := mcpmodel.Tool{Name: name, SourceFile: rs.path, SourceLine: lineAt(rs.norm, at)}
	t.Description, _ = asString(args[1])
	if len(args) > 2 {
		t.InputSchema = rs.schemaValue(args[2], at, 0)
	}
	for _, seg := range ch.segments[2:] {
		rs.applyToolBuilder(&t, seg, at)
	}
	return rustFound{tool: t, start: at, end: p.pos}, true
}

func (rs *rustSource) applyToolBuilder(t *mcpmodel.Tool, seg chainSegment, at int) {
	if !seg.called || len(seg.args) == 0 {
		return
	}
	switch seg.name {
	case "with_title":
		t.Title, _ = asString(seg.args[0])
	case "with_annotations", "annotate":
		t.Annotations = rustAnnotationsExpr(seg.args[0])
	case "with_raw_output_schema":
		t.OutputSchema = rs.schemaValue(seg.args[0], at, 0)
	}
}

func (sc *rustScan) toolLiterals(rs *rustSource) []rustFound {
	var out []rustFound
	for _, m := range rustToolLitRe.FindAllStringIndex(rs.code, -1) {
		if f, ok := sc.toolLiteral(rs, m[0]); ok {
			out = append(out, f)
		}
	}
	return out
}

func (sc *rustScan) toolLiteral(rs *rustSource, at int) (rustFound, bool) {
	switch w, _ := rs.wordBefore(rs.skipBack(at)); w {
	case "struct", "enum", "union", "impl", "for", "trait", "type":
		return rustFound{}, false
	}
	p := newLitParser(rs.norm, at, langRust)
	obj, ok := rs.resolve(p.primary()).(map[string]any)
	if !ok {
		return rustFound{}, false
	}
	t, ok := toolFromFields(obj, func(v any) (map[string]any, bool) {
		s := rs.schemaValue(rustUnwrapSome(v), at, 0)
		return s, s != nil
	})
	if !ok {
		return rustFound{}, false
	}
	t.SourceFile, t.SourceLine = rs.path, lineAt(rs.norm, at)
	t.Annotations = rustAnnotationsExpr(obj["annotations"])
	return rustFound{tool: t, start: at, end: p.pos}, true
}

func rustUnwrapSome(v any) any {
	ch, ok := v.(*chainValue)
	if ok && ch.base == nil && len(ch.segments) == 1 && ch.segments[0].name == "Some" && len(ch.segments[0].args) == 1 {
		return ch.segments[0].args[0]
	}
	return v
}

// rustAnnotationsExpr reads ToolAnnotations as a struct literal, a
// builder chain (ToolAnnotations::new().read_only(true)...), or from_raw.
func rustAnnotationsExpr(v any) map[string]any {
	out := map[string]any{}
	switch t := rustUnwrapSome(v).(type) {
	case map[string]any:
		for k, val := range t {
			if key, ok := rustAnnotationKeys[k]; ok {
				rustSetAnnotation(out, key, val)
			}
		}
	case *chainValue:
		for _, seg := range t.segments {
			rustAnnotationSegment(out, seg)
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

var rustAnnotationBuilders = map[string]string{
	"with_title":  "title",
	"read_only":   "readOnlyHint",
	"destructive": "destructiveHint",
	"idempotent":  "idempotentHint",
	"open_world":  "openWorldHint",
}

var rustAnnotationRawOrder = []string{"title", "readOnlyHint", "destructiveHint", "idempotentHint", "openWorldHint"}

func rustAnnotationSegment(out map[string]any, seg chainSegment) {
	if seg.name == "from_raw" {
		for i, a := range seg.args {
			if i < len(rustAnnotationRawOrder) {
				rustSetAnnotation(out, rustAnnotationRawOrder[i], a)
			}
		}
		return
	}
	if key, ok := rustAnnotationBuilders[seg.name]; ok && len(seg.args) > 0 {
		rustSetAnnotation(out, key, seg.args[0])
	}
}

// ---- schema expressions ----

// schemaValue evaluates a schema expression: a json!/object! literal,
// possibly wrapped (Arc::new(..), .as_object().unwrap().clone()), a
// serde_json::from_str of a string, or a let/const/static holding one.
// before is the offset of the use, for picking the nearest binding.
func (rs *rustSource) schemaValue(v any, before, depth int) map[string]any {
	if depth > 4 {
		return nil
	}
	switch t := v.(type) {
	case map[string]any:
		js, _ := toJSONValue(t)
		m, _ := js.(map[string]any)
		return m
	case *chainValue:
		return rs.schemaChain(t, before, depth)
	}
	return nil
}

func (rs *rustSource) schemaChain(ch *chainValue, before, depth int) map[string]any {
	if ch.base != nil {
		return rs.schemaValue(ch.base, before, depth+1)
	}
	if rustIsBindingUse(ch) {
		return rs.schemaValue(rs.bindingValue(ch.segments[0].name, before), before, depth+1)
	}
	for _, seg := range ch.segments {
		if !seg.called || len(seg.args) == 0 {
			continue
		}
		if seg.name == "from_str" {
			return rs.schemaFromText(seg.args[0], before)
		}
		if s := rs.schemaValue(seg.args[0], before, depth+1); s != nil {
			return s
		}
	}
	return nil
}

// rustIsBindingUse reports a variable optionally followed by argument-free
// conversions: `schema`, `schema.as_object().unwrap().clone()`.
func rustIsBindingUse(ch *chainValue) bool {
	if ch.base != nil || len(ch.segments) == 0 || ch.segments[0].called {
		return false
	}
	for _, seg := range ch.segments[1:] {
		if len(seg.args) > 0 {
			return false
		}
	}
	return true
}

func (rs *rustSource) schemaFromText(v any, before int) map[string]any {
	if ch, ok := v.(*chainValue); ok && rustIsBindingUse(ch) {
		v = rs.bindingValue(ch.segments[0].name, before)
	}
	s, ok := asString(v)
	if !ok {
		return nil
	}
	var m map[string]any
	if json.Unmarshal([]byte(s), &m) != nil {
		return nil
	}
	return m
}

// bindingValue parses the initializer of `let name = ...` (the last one
// before the use) or a const/static of that name.
func (rs *rustSource) bindingValue(name string, before int) any {
	re, err := regexp.Compile(`\b(?:let\s+(?:mut\s+)?|const\s+|static\s+)` + regexp.QuoteMeta(name) + `\s*(?::[^=;]*)?=`)
	if err != nil {
		return nil
	}
	pos := -1
	for _, m := range re.FindAllStringIndex(rs.code, -1) {
		if pos < 0 || m[0] < before {
			pos = m[1]
		}
	}
	if pos < 0 || (pos < len(rs.code) && rs.code[pos] == '=') {
		return nil
	}
	return rs.parseValue(pos)
}

// ---- pattern: json! object literals with name + inputSchema ----

func (sc *rustScan) jsonTools(rs *rustSource) []rustFound {
	var keys []int
	for off := 0; ; {
		i := strings.Index(rs.norm[off:], `"inputSchema"`)
		if i < 0 {
			break
		}
		pos := off + i
		if rs.code[pos] == '"' && rs.isKey(pos+len(`"inputSchema"`)) {
			keys = append(keys, pos)
		}
		off = pos + 1
	}
	var out []rustFound
	seen := map[int]bool{}
	for _, open := range enclosingOpen(rs.norm, langRust, '{', keys) {
		if open < 0 || seen[open] {
			continue
		}
		seen[open] = true
		if f, ok := rs.jsonTool(open); ok {
			out = append(out, f)
		}
	}
	return out
}

func (rs *rustSource) isKey(pos int) bool {
	p := rs.skipFwd(pos)
	return p < len(rs.code) && rs.code[p] == ':' && (p+1 >= len(rs.code) || rs.code[p+1] != ':')
}

func (rs *rustSource) jsonTool(open int) (rustFound, bool) {
	p := newLitParser(rs.norm, open, langRust)
	obj, ok := rs.resolve(p.primary()).(map[string]any)
	if !ok {
		return rustFound{}, false
	}
	t, ok := toolFromFields(obj, literalSchema)
	if !ok {
		return rustFound{}, false
	}
	t.SourceFile, t.SourceLine = rs.path, lineAt(rs.norm, open)
	return rustFound{tool: t, start: open, end: p.pos}, true
}
