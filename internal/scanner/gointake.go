// Go intake — go/ast-based extractor for MCP tool registrations in Go
// servers. Files are parsed per directory and grouped by package clause so
// a handler's input struct declared in a sibling file is still found. Only
// the standard library parser is used: there is no type checker, so names
// are resolved syntactically (import aliases, local and package-level
// variables and constants).
//
// Recognized patterns:
//
//	mcp.AddTool(server, &mcp.Tool{Name: "x", Description: "...",  // official go-sdk
//	    Annotations: &mcp.ToolAnnotations{ReadOnlyHint: true}}, handler)
//	server.AddTool(&mcp.Tool{...}, rawHandler)
//
//	    The input schema is an explicit literal (&jsonschema.Schema{...},
//	    map[string]any{...}, json.RawMessage(`...`)), a helper function that
//	    returns jsonschema.For[T] followed by simple field assignments, or is
//	    inferred from the handler's input struct following jsonschema-go's
//	    rules (json tag names, required unless omitempty/omitzero, the
//	    jsonschema tag as description, pointers and slices nullable).
//
//	mcp.NewTool("x", mcp.WithDescription("..."),                   // mark3labs/mcp-go
//	    mcp.WithString("city", mcp.Required(), mcp.Enum("a", "b")))
//	mcp.NewToolWithRawSchema("x", "...", json.RawMessage(`{...}`))
//
//	Tool{Name: "x", Description: "...", InputSchema: ...}         // hand-rolled
//	map[string]any{"name": "x", "inputSchema": map[string]any{...}}
//
// Annotations mirror what each SDK puts on the wire: go-sdk always sends
// readOnlyHint and idempotentHint once Annotations is set, and mcp-go's
// NewTool starts from default hints (destructive, open-world).
//
// Limitations:
//   - Values must be literals, constants, or variables assigned from them.
//     Anything computed at runtime leaves the field unset, and an
//     unevaluable piece of a schema drops the whole schema rather than
//     reporting a partial one that rules would misjudge.
//   - Tools whose name cannot be resolved statically are skipped.
//   - Types from other packages are not followed, and mcp-go's extra
//     struct-tag handling for WithInputSchema[T] is not modeled.
//   - Tool literals collected into slices are found, but their handlers
//     (and so inferred schemas) are not linked.
package scanner

import (
	"encoding/json"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"maps"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/kaeawc/mcpcheck/internal/mcpmodel"
)

const (
	goSDKModule = "github.com/modelcontextprotocol/go-sdk"
	mcpgoModule = "github.com/mark3labs/mcp-go"

	// maxGoResolve bounds identifier chasing so self-referential
	// assignments cannot loop.
	maxGoResolve = 16
)

func isGoSourceFile(path string) bool { return filepath.Ext(path) == ".go" }

// LoadGo scans a .go file or a directory tree for tool registrations.
func LoadGo(path string) (*mcpmodel.ToolSet, error) {
	groups, err := groupGoFiles(path)
	if err != nil {
		return nil, fmt.Errorf("scan go sources %s: %w", path, err)
	}
	set := &mcpmodel.ToolSet{Source: path}
	for _, g := range groups {
		set.Tools = append(set.Tools, scanGoFiles(g.files, g.report)...)
	}
	return set, nil
}

// goFileGroup is one directory's sources. report, when set, limits which
// files tools are reported from; the rest only supply declarations.
type goFileGroup struct {
	files  []string
	report func(path string) bool
}

func groupGoFiles(root string) ([]goFileGroup, error) {
	byDir := map[string]int{}
	var groups []goFileGroup
	err := walkSources(root, isGoSourceFile, func(path string) error {
		dir := filepath.Dir(path)
		i, ok := byDir[dir]
		if !ok {
			i = len(groups)
			byDir[dir] = i
			groups = append(groups, goFileGroup{})
		}
		groups[i].files = append(groups[i].files, path)
		return nil
	})
	if err != nil {
		return nil, err
	}
	if len(groups) == 1 && len(groups[0].files) == 1 && groups[0].files[0] == root {
		groups[0] = singleGoFileGroup(root)
	}
	return groups, nil
}

// singleGoFileGroup pairs a lone file with its package siblings so input
// structs declared next to it still resolve.
func singleGoFileGroup(path string) goFileGroup {
	g := goFileGroup{files: []string{path}, report: func(p string) bool { return p == path }}
	dir := filepath.Dir(path)
	entries, err := os.ReadDir(dir)
	if err != nil {
		return g
	}
	for _, e := range entries {
		sibling := filepath.Join(dir, e.Name())
		if e.IsDir() || !isGoSourceFile(sibling) || isTestFile(sibling) || sibling == filepath.Clean(path) {
			continue
		}
		g.files = append(g.files, sibling)
	}
	return g
}

func scanGoFiles(paths []string, report func(string) bool) []mcpmodel.Tool {
	fset := token.NewFileSet()
	pkgs := map[string]*goPkg{}
	var order []string
	for _, path := range paths {
		f, err := parser.ParseFile(fset, path, nil, parser.SkipObjectResolution)
		if err != nil {
			continue
		}
		p, ok := pkgs[f.Name.Name]
		if !ok {
			p = newGoPkg(fset)
			pkgs[f.Name.Name] = p
			order = append(order, f.Name.Name)
		}
		p.addFile(path, f)
	}
	var tools []mcpmodel.Tool
	for _, name := range order {
		tools = append(tools, pkgs[name].tools(report)...)
	}
	sort.SliceStable(tools, func(i, j int) bool {
		if tools[i].SourceFile != tools[j].SourceFile {
			return tools[i].SourceFile < tools[j].SourceFile
		}
		return tools[i].SourceLine < tools[j].SourceLine
	})
	return tools
}

// goPkg holds the package-level declarations of one package's files.
type goPkg struct {
	fset    *token.FileSet
	files   []*goFile
	types   map[string]ast.Expr
	funcs   map[string]*ast.FuncDecl
	methods map[string]*ast.FuncDecl
	values  map[string]ast.Expr
	links   map[*ast.CompositeLit]goHandler
}

// goFile records the local names a file gives the MCP SDK imports; "."
// marks a dot import.
type goFile struct {
	path  string
	file  *ast.File
	sdk   string
	mcpgo string
}

func newGoPkg(fset *token.FileSet) *goPkg {
	return &goPkg{
		fset:    fset,
		types:   map[string]ast.Expr{},
		funcs:   map[string]*ast.FuncDecl{},
		methods: map[string]*ast.FuncDecl{},
		values:  map[string]ast.Expr{},
		links:   map[*ast.CompositeLit]goHandler{},
	}
}

func (p *goPkg) addFile(path string, f *ast.File) {
	gf := &goFile{path: path, file: f}
	for _, imp := range f.Imports {
		gf.addImport(imp)
	}
	p.files = append(p.files, gf)
	for _, d := range f.Decls {
		switch d := d.(type) {
		case *ast.FuncDecl:
			p.addFunc(d)
		case *ast.GenDecl:
			p.addGenDecl(d)
		}
	}
}

func (f *goFile) addImport(imp *ast.ImportSpec) {
	path, err := strconv.Unquote(imp.Path.Value)
	if err != nil {
		return
	}
	local := "mcp"
	if imp.Name != nil {
		local = imp.Name.Name
	}
	if local == "_" {
		return
	}
	switch {
	case isMCPImport(path, goSDKModule):
		f.sdk = local
	case isMCPImport(path, mcpgoModule):
		f.mcpgo = local
	}
}

func isMCPImport(path, module string) bool {
	return strings.HasPrefix(path, module+"/") && strings.HasSuffix(path, "/mcp")
}

func (p *goPkg) addFunc(d *ast.FuncDecl) {
	if d.Recv == nil {
		p.funcs[d.Name.Name] = d
		return
	}
	if _, ok := p.methods[d.Name.Name]; !ok {
		p.methods[d.Name.Name] = d
	}
}

func (p *goPkg) addGenDecl(d *ast.GenDecl) {
	for _, spec := range d.Specs {
		switch s := spec.(type) {
		case *ast.TypeSpec:
			p.types[s.Name.Name] = s.Type
		case *ast.ValueSpec:
			addValueSpec(p.values, s)
		}
	}
}

func addValueSpec(into map[string]ast.Expr, s *ast.ValueSpec) {
	if len(s.Values) != len(s.Names) {
		return
	}
	for i, n := range s.Names {
		addBinding(into, n, s.Values[i])
	}
}

// addBinding keeps the first assignment to a name: tool variables are
// almost always initialized once and never reassigned.
func addBinding(into map[string]ast.Expr, lhs, rhs ast.Expr) {
	id, ok := lhs.(*ast.Ident)
	if !ok || id.Name == "_" {
		return
	}
	if _, exists := into[id.Name]; !exists {
		into[id.Name] = rhs
	}
}

// collectScope gathers every local binding in a function body. Block
// scoping is ignored; first assignment wins.
func collectScope(body ast.Node) map[string]ast.Expr {
	scope := map[string]ast.Expr{}
	ast.Inspect(body, func(n ast.Node) bool {
		switch v := n.(type) {
		case *ast.AssignStmt:
			if len(v.Lhs) == len(v.Rhs) {
				for i := range v.Lhs {
					addBinding(scope, v.Lhs[i], v.Rhs[i])
				}
			}
		case *ast.ValueSpec:
			addValueSpec(scope, v)
		}
		return true
	})
	return scope
}

// walk visits every node of every declaration with a context carrying the
// declaration's local scope.
func (p *goPkg) walk(visit func(c *goCtx, n ast.Node)) {
	for _, f := range p.files {
		for _, d := range f.file.Decls {
			c := &goCtx{pkg: p, file: f}
			if fn, ok := d.(*ast.FuncDecl); ok && fn.Body != nil {
				c.scope = collectScope(fn.Body)
			}
			ast.Inspect(d, func(n ast.Node) bool {
				if n != nil {
					visit(c, n)
				}
				return true
			})
		}
	}
}

// tools links go-sdk tool literals to their handlers in a first pass, so
// literals declared before (or in another file than) the AddTool call
// still get an inferred schema, then extracts tools in a second pass.
func (p *goPkg) tools(report func(string) bool) []mcpmodel.Tool {
	p.walk(func(c *goCtx, n ast.Node) { c.linkHandler(n) })
	var out []mcpmodel.Tool
	p.walk(func(c *goCtx, n ast.Node) {
		if report == nil || report(c.file.path) {
			out = append(out, c.toolsAt(n)...)
		}
	})
	return out
}

// goCtx evaluates expressions within one declaration of one file.
type goCtx struct {
	pkg   *goPkg
	file  *goFile
	scope map[string]ast.Expr
}

// goHandler is the handler argument of a generic mcp.AddTool call.
type goHandler struct {
	ctx      *goCtx
	fn       ast.Expr
	typeArgs []ast.Expr
}

func (c *goCtx) line(n ast.Node) int { return c.pkg.fset.Position(n.Pos()).Line }

func (c *goCtx) lookup(name string) ast.Expr {
	if e, ok := c.scope[name]; ok {
		return e
	}
	return c.pkg.values[name]
}

// resolve strips parentheses and follows identifiers to the expression
// they were bound to.
func (c *goCtx) resolve(e ast.Expr) ast.Expr {
	for i := 0; i < maxGoResolve && e != nil; i++ {
		var next ast.Expr
		switch v := e.(type) {
		case *ast.ParenExpr:
			next = v.X
		case *ast.Ident:
			next = c.lookup(v.Name)
		}
		if next == nil {
			return e
		}
		e = next
	}
	return e
}

func unwrapIndex(e ast.Expr) (ast.Expr, []ast.Expr) {
	switch v := e.(type) {
	case *ast.IndexExpr:
		return v.X, []ast.Expr{v.Index}
	case *ast.IndexListExpr:
		return v.X, v.Indices
	}
	return e, nil
}

// pkgMember returns the member name when e refers to a member of the
// package imported under the local name pkg.
func pkgMember(e ast.Expr, pkg string) string {
	switch v := e.(type) {
	case *ast.Ident:
		if pkg == "." {
			return v.Name
		}
	case *ast.SelectorExpr:
		if x, ok := v.X.(*ast.Ident); ok && pkg != "" && x.Name == pkg {
			return v.Sel.Name
		}
	}
	return ""
}

func stripStar(e ast.Expr) ast.Expr {
	if s, ok := e.(*ast.StarExpr); ok {
		return s.X
	}
	return e
}

func typeName(e ast.Expr) string {
	switch v := e.(type) {
	case *ast.Ident:
		return v.Name
	case *ast.SelectorExpr:
		return v.Sel.Name
	case *ast.IndexExpr:
		return typeName(v.X)
	}
	return ""
}

func (c *goCtx) isSDKToolType(typ ast.Expr) bool {
	return pkgMember(stripStar(typ), c.file.sdk) == "Tool"
}

// linkHandler records the handler of mcp.AddTool(server, tool, handler)
// against the tool literal it registers.
func (c *goCtx) linkHandler(n ast.Node) {
	call, ok := n.(*ast.CallExpr)
	if !ok || len(call.Args) != 3 {
		return
	}
	fun, typeArgs := unwrapIndex(call.Fun)
	if pkgMember(fun, c.file.sdk) != "AddTool" {
		return
	}
	if lit := c.sdkToolLit(call.Args[1]); lit != nil {
		c.pkg.links[lit] = goHandler{ctx: c, fn: call.Args[2], typeArgs: typeArgs}
	}
}

func (c *goCtx) sdkToolLit(e ast.Expr) *ast.CompositeLit {
	e = c.resolve(e)
	if u, ok := e.(*ast.UnaryExpr); ok && u.Op == token.AND {
		e = c.resolve(u.X)
	}
	lit, ok := e.(*ast.CompositeLit)
	if !ok || !c.isSDKToolType(lit.Type) {
		return nil
	}
	return lit
}

func (c *goCtx) toolsAt(n ast.Node) []mcpmodel.Tool {
	switch v := n.(type) {
	case *ast.CompositeLit:
		return c.toolsFromLit(v)
	case *ast.CallExpr:
		if t, ok := c.mcpgoTool(v); ok {
			return []mcpmodel.Tool{t}
		}
	}
	return nil
}

// toolsFromLit handles typed literals directly and, for slice literals,
// the type-elided element literals the walker cannot type on its own.
func (c *goCtx) toolsFromLit(lit *ast.CompositeLit) []mcpmodel.Tool {
	if lit.Type == nil {
		return nil
	}
	if t, ok := c.toolFromTypedLit(lit, lit.Type); ok {
		return []mcpmodel.Tool{t}
	}
	arr, ok := lit.Type.(*ast.ArrayType)
	if !ok {
		return nil
	}
	var out []mcpmodel.Tool
	for _, elt := range lit.Elts {
		el, ok := elt.(*ast.CompositeLit)
		if !ok || el.Type != nil {
			continue
		}
		if t, ok := c.toolFromTypedLit(el, arr.Elt); ok {
			out = append(out, t)
		}
	}
	return out
}

func (c *goCtx) toolFromTypedLit(lit *ast.CompositeLit, typ ast.Expr) (mcpmodel.Tool, bool) {
	typ = stripStar(typ)
	switch {
	case c.isSDKToolType(typ):
		return c.sdkTool(lit)
	case typeName(typ) == "Tool":
		return c.structTool(lit)
	}
	if _, ok := c.underlying(typ).(*ast.MapType); ok {
		return c.mapTool(lit)
	}
	return mcpmodel.Tool{}, false
}

func (c *goCtx) baseTool(n ast.Node, name, title, desc ast.Expr) (mcpmodel.Tool, bool) {
	toolName, ok := c.str(name)
	if !ok {
		return mcpmodel.Tool{}, false
	}
	t := mcpmodel.Tool{Name: toolName, SourceFile: c.file.path, SourceLine: c.line(n)}
	t.Title, _ = c.str(title)
	t.Description, _ = c.str(desc)
	return t, true
}

// finishTitle applies the spec's display-name fallback, as ToolFromJSON
// does for protocol intakes.
func finishTitle(t *mcpmodel.Tool) {
	if t.Title == "" {
		t.Title, _ = t.Annotations["title"].(string)
	}
}

func keyedFields(lit *ast.CompositeLit) map[string]ast.Expr {
	out := map[string]ast.Expr{}
	for _, elt := range lit.Elts {
		kv, ok := elt.(*ast.KeyValueExpr)
		if !ok {
			continue
		}
		if id, ok := kv.Key.(*ast.Ident); ok {
			out[id.Name] = kv.Value
		}
	}
	return out
}

func (c *goCtx) stringKeyedFields(lit *ast.CompositeLit) map[string]ast.Expr {
	out := map[string]ast.Expr{}
	for _, elt := range lit.Elts {
		kv, ok := elt.(*ast.KeyValueExpr)
		if !ok {
			continue
		}
		if k, ok := c.str(kv.Key); ok {
			out[k] = kv.Value
		}
	}
	return out
}

func (c *goCtx) sdkTool(lit *ast.CompositeLit) (mcpmodel.Tool, bool) {
	f := keyedFields(lit)
	t, ok := c.baseTool(lit, f["Name"], f["Title"], f["Description"])
	if !ok {
		return t, false
	}
	t.InputSchema = c.sdkSchema(f["InputSchema"])
	t.OutputSchema = c.sdkSchema(f["OutputSchema"])
	if h, linked := c.pkg.links[lit]; linked {
		h.inferSchemas(&t, f["InputSchema"] != nil)
	}
	t.Annotations = sdkAnnotations(c.objectValue(f["Annotations"]))
	finishTitle(&t)
	return t, true
}

func (c *goCtx) sdkSchema(e ast.Expr) map[string]any {
	if e == nil {
		return nil
	}
	if m := c.objectValue(e); m != nil {
		return m
	}
	if call, ok := c.resolve(e).(*ast.CallExpr); ok {
		return c.schemaFromHelper(call)
	}
	return nil
}

// sdkAnnotations adds the plain-bool hints go-sdk (since v1.7.0) always
// marshals once a ToolAnnotations value is present.
func sdkAnnotations(m map[string]any) map[string]any {
	if m == nil {
		return nil
	}
	for _, k := range []string{"readOnlyHint", "idempotentHint"} {
		if _, ok := m[k]; !ok {
			m[k] = false
		}
	}
	return m
}

// inferSchemas fills schemas the tool literal left unset (or set to
// something unevaluable) from the handler's In/Out types, as AddTool does.
func (h goHandler) inferSchemas(t *mcpmodel.Tool, explicitInput bool) {
	in, out := h.ctx.handlerTypes(h.fn)
	if len(h.typeArgs) > 0 {
		in = h.typeArgs[0]
	}
	if len(h.typeArgs) > 1 {
		out = h.typeArgs[1]
	}
	if t.InputSchema == nil && in != nil {
		t.InputSchema = h.ctx.inferInput(in, explicitInput)
	}
	if t.OutputSchema == nil && out != nil && !isAnyType(stripStar(out)) {
		t.OutputSchema = h.ctx.inferType(out)
	}
}

// handlerTypes returns In and Out from a ToolHandlerFor signature:
// func(context.Context, *mcp.CallToolRequest, In) (*mcp.CallToolResult, Out, error).
func (c *goCtx) handlerTypes(fn ast.Expr) (in, out ast.Expr) {
	ft := c.funcType(fn)
	if ft == nil {
		return nil, nil
	}
	if params := fieldTypes(ft.Params); len(params) == 3 {
		in = params[2]
	}
	if results := fieldTypes(ft.Results); len(results) == 3 {
		out = results[1]
	}
	return in, out
}

func (c *goCtx) funcType(fn ast.Expr) *ast.FuncType {
	var decl *ast.FuncDecl
	switch v := c.resolve(fn).(type) {
	case *ast.FuncLit:
		return v.Type
	case *ast.Ident:
		decl = c.pkg.funcs[v.Name]
	case *ast.SelectorExpr:
		decl = c.pkg.methods[v.Sel.Name]
	}
	if decl == nil {
		return nil
	}
	return decl.Type
}

func fieldTypes(fl *ast.FieldList) []ast.Expr {
	if fl == nil {
		return nil
	}
	var out []ast.Expr
	for _, f := range fl.List {
		for range max(1, len(f.Names)) {
			out = append(out, f.Type)
		}
	}
	return out
}

func isAnyType(t ast.Expr) bool {
	switch v := t.(type) {
	case *ast.Ident:
		return v.Name == "any"
	case *ast.InterfaceType:
		return v.Methods == nil || len(v.Methods.List) == 0
	}
	return false
}

// inferInput mirrors AddTool: an `any` input with no schema becomes an
// empty object schema; anything else is inferred from the type.
func (c *goCtx) inferInput(t ast.Expr, explicit bool) map[string]any {
	t = stripStar(t)
	if !isAnyType(t) {
		return c.inferType(t)
	}
	if explicit {
		return nil
	}
	return map[string]any{"type": "object"}
}

func (c *goCtx) inferType(t ast.Expr) map[string]any {
	s, ok := c.typeSchema(stripStar(t), map[string]bool{})
	if !ok {
		return nil
	}
	return s
}

// schemaFromHelper handles InputSchema: helper(), where helper builds the
// schema with jsonschema.For[T] and then adjusts it with plain field
// assignments such as s.Properties["units"].Enum = []any{...}. Any
// assignment to the schema it cannot apply abandons the helper.
func (c *goCtx) schemaFromHelper(call *ast.CallExpr) map[string]any {
	id, ok := call.Fun.(*ast.Ident)
	if !ok {
		return nil
	}
	fn := c.pkg.funcs[id.Name]
	if fn == nil || fn.Body == nil {
		return nil
	}
	hc := &goCtx{pkg: c.pkg, file: c.file, scope: collectScope(fn.Body)}
	var root string
	var schema map[string]any
	for _, stmt := range fn.Body.List {
		as, ok := stmt.(*ast.AssignStmt)
		switch {
		case !ok:
			continue
		case schema == nil:
			root, schema = hc.jsonschemaFor(as)
		case !hc.applySchemaAssign(schema, root, as):
			return nil
		}
	}
	return schema
}

func (c *goCtx) jsonschemaFor(as *ast.AssignStmt) (string, map[string]any) {
	if len(as.Rhs) != 1 {
		return "", nil
	}
	call, ok := as.Rhs[0].(*ast.CallExpr)
	if !ok {
		return "", nil
	}
	fun, typeArgs := unwrapIndex(call.Fun)
	sel, ok := fun.(*ast.SelectorExpr)
	if !ok || sel.Sel.Name != "For" || len(typeArgs) != 1 {
		return "", nil
	}
	id, ok := as.Lhs[0].(*ast.Ident)
	if !ok {
		return "", nil
	}
	return id.Name, c.inferType(typeArgs[0])
}

func (c *goCtx) applySchemaAssign(schema map[string]any, root string, as *ast.AssignStmt) bool {
	path, rooted := schemaPath(as.Lhs[0], root)
	if !rooted {
		return true
	}
	if len(as.Lhs) != 1 || len(as.Rhs) != 1 || as.Tok != token.ASSIGN {
		return false
	}
	v, ok := c.eval(as.Rhs[0], nil)
	return ok && setSchemaPath(schema, path, v)
}

// schemaPath turns s.Properties["units"].Enum into its JSON path
// [properties units enum]. A nil path with rooted=true means the
// expression touches the schema in a way that cannot be followed.
func schemaPath(e ast.Expr, root string) (path []string, rooted bool) {
	switch v := e.(type) {
	case *ast.Ident:
		return []string{}, v.Name == root
	case *ast.ParenExpr:
		return schemaPath(v.X, root)
	case *ast.StarExpr:
		return schemaPath(v.X, root)
	case *ast.SelectorExpr:
		p, ok := schemaPath(v.X, root)
		return appendPath(p, goFieldJSONKey(v.Sel.Name), true), ok
	case *ast.IndexExpr:
		p, ok := schemaPath(v.X, root)
		key, isLit := stringLit(v.Index)
		return appendPath(p, key, isLit), ok
	}
	return nil, false
}

func appendPath(p []string, key string, ok bool) []string {
	if p == nil || !ok || key == "" {
		return nil
	}
	return append(p, key)
}

func stringLit(e ast.Expr) (string, bool) {
	lit, ok := e.(*ast.BasicLit)
	if !ok || lit.Kind != token.STRING {
		return "", false
	}
	s, err := strconv.Unquote(lit.Value)
	return s, err == nil
}

func setSchemaPath(m map[string]any, path []string, v any) bool {
	if len(path) == 0 {
		return false
	}
	for _, k := range path[:len(path)-1] {
		next, ok := m[k].(map[string]any)
		if !ok {
			return false
		}
		m = next
	}
	m[path[len(path)-1]] = v
	return true
}

func (c *goCtx) structTool(lit *ast.CompositeLit) (mcpmodel.Tool, bool) {
	f := keyedFields(lit)
	t, ok := c.baseTool(lit, f["Name"], f["Title"], f["Description"])
	if !ok {
		return t, false
	}
	t.InputSchema = c.objectValue(f["InputSchema"])
	t.OutputSchema = c.objectValue(f["OutputSchema"])
	t.Annotations = c.objectValue(f["Annotations"])
	finishTitle(&t)
	return t, true
}

func (c *goCtx) mapTool(lit *ast.CompositeLit) (mcpmodel.Tool, bool) {
	f := c.stringKeyedFields(lit)
	if f["inputSchema"] == nil {
		return mcpmodel.Tool{}, false
	}
	t, ok := c.baseTool(lit, f["name"], f["title"], f["description"])
	if !ok {
		return t, false
	}
	t.InputSchema = c.objectValue(f["inputSchema"])
	t.OutputSchema = c.objectValue(f["outputSchema"])
	t.Annotations = c.objectValue(f["annotations"])
	finishTitle(&t)
	return t, true
}

// --- mark3labs/mcp-go ---

// mcpgoBuilder accumulates what NewTool's options would set.
type mcpgoBuilder struct {
	tool       mcpmodel.Tool
	props      map[string]any
	required   []any
	additional any
	raw        map[string]any
	rawSet     bool
	bad        bool
	ann        map[string]any
}

type mcpgoToolOption func(c *goCtx, b *mcpgoBuilder, args, typeArgs []ast.Expr)

var mcpgoToolOptions = map[string]mcpgoToolOption{
	"WithDescription":                mcpgoDescription,
	"WithToolTitle":                  mcpgoTitle,
	"WithTitleAnnotation":            mcpgoTitleAnnotation,
	"WithReadOnlyHintAnnotation":     mcpgoHint("readOnlyHint"),
	"WithDestructiveHintAnnotation":  mcpgoHint("destructiveHint"),
	"WithIdempotentHintAnnotation":   mcpgoHint("idempotentHint"),
	"WithOpenWorldHintAnnotation":    mcpgoHint("openWorldHint"),
	"WithoutDefaultAnnotations":      mcpgoClearAnnotations,
	"WithToolAnnotation":             mcpgoAnnotation,
	"WithString":                     mcpgoProperty("string"),
	"WithNumber":                     mcpgoProperty("number"),
	"WithInteger":                    mcpgoProperty("integer"),
	"WithBoolean":                    mcpgoProperty("boolean"),
	"WithArray":                      mcpgoProperty("array"),
	"WithObject":                     mcpgoProperty("object"),
	"WithAny":                        mcpgoProperty(""),
	"WithRawInputSchema":             mcpgoRawInput,
	"WithInputSchema":                mcpgoTypedInput,
	"WithRawOutputSchema":            mcpgoRawOutput,
	"WithOutputSchema":               mcpgoTypedOutput,
	"WithSchemaAdditionalProperties": mcpgoAdditional,
}

// mcpgoPropKeys maps single-argument PropertyOptions to the JSON Schema
// keyword they set.
var mcpgoPropKeys = map[string]string{
	"Description":          "description",
	"Title":                "title",
	"DefaultString":        "default",
	"DefaultNumber":        "default",
	"DefaultBool":          "default",
	"DefaultArray":         "default",
	"MaxLength":            "maxLength",
	"MinLength":            "minLength",
	"Pattern":              "pattern",
	"Max":                  "maximum",
	"Min":                  "minimum",
	"MultipleOf":           "multipleOf",
	"Properties":           "properties",
	"AdditionalProperties": "additionalProperties",
	"MinProperties":        "minProperties",
	"MaxProperties":        "maxProperties",
	"PropertyNames":        "propertyNames",
	"Items":                "items",
	"MinItems":             "minItems",
	"MaxItems":             "maxItems",
	"UniqueItems":          "uniqueItems",
}

var mcpgoItemKinds = map[string]string{
	"WithStringItems":  "string",
	"WithNumberItems":  "number",
	"WithIntegerItems": "integer",
	"WithBooleanItems": "boolean",
}

func (c *goCtx) mcpgoTool(call *ast.CallExpr) (mcpmodel.Tool, bool) {
	fun, _ := unwrapIndex(call.Fun)
	switch pkgMember(fun, c.file.mcpgo) {
	case "NewTool":
		return c.mcpgoNewTool(call)
	case "NewToolWithRawSchema":
		return c.mcpgoRawTool(call)
	}
	return mcpmodel.Tool{}, false
}

func (c *goCtx) mcpgoRawTool(call *ast.CallExpr) (mcpmodel.Tool, bool) {
	if len(call.Args) != 3 {
		return mcpmodel.Tool{}, false
	}
	t, ok := c.baseTool(call, call.Args[0], nil, call.Args[1])
	if ok {
		t.InputSchema = c.objectValue(call.Args[2])
	}
	return t, ok
}

func (c *goCtx) mcpgoNewTool(call *ast.CallExpr) (mcpmodel.Tool, bool) {
	if len(call.Args) == 0 {
		return mcpmodel.Tool{}, false
	}
	t, ok := c.baseTool(call, call.Args[0], nil, nil)
	if !ok {
		return t, false
	}
	b := &mcpgoBuilder{tool: t, props: map[string]any{}, ann: map[string]any{
		"readOnlyHint": false, "destructiveHint": true, "idempotentHint": false, "openWorldHint": true,
	}}
	for _, opt := range call.Args[1:] {
		c.applyMcpgoToolOption(b, opt)
	}
	return b.build(), true
}

func (c *goCtx) applyMcpgoToolOption(b *mcpgoBuilder, opt ast.Expr) {
	call, ok := c.resolve(opt).(*ast.CallExpr)
	if !ok {
		return
	}
	fun, typeArgs := unwrapIndex(call.Fun)
	if apply, ok := mcpgoToolOptions[pkgMember(fun, c.file.mcpgo)]; ok {
		apply(c, b, call.Args, typeArgs)
	}
}

func (b *mcpgoBuilder) build() mcpmodel.Tool {
	t := b.tool
	t.InputSchema = b.inputSchema()
	if len(b.ann) > 0 {
		t.Annotations = b.ann
	}
	finishTitle(&t)
	return t
}

func (b *mcpgoBuilder) inputSchema() map[string]any {
	switch {
	case b.rawSet:
		return b.raw
	case b.bad:
		return nil
	}
	s := map[string]any{"type": "object", "properties": b.props}
	if len(b.required) > 0 {
		s["required"] = b.required
	}
	if b.additional != nil {
		s["additionalProperties"] = b.additional
	}
	return s
}

// setProperty moves a property-level required flag into the object's
// required list, as mcp-go's setInputProperty does.
func (b *mcpgoBuilder) setProperty(name string, schema map[string]any) {
	if req, _ := schema["required"].(bool); req {
		delete(schema, "required")
		b.required = appendUniqueName(b.required, name)
	}
	b.props[name] = schema
}

func appendUniqueName(list []any, name string) []any {
	for _, v := range list {
		if v == name {
			return list
		}
	}
	return append(list, name)
}

func arg0(args []ast.Expr) ast.Expr {
	if len(args) == 0 {
		return nil
	}
	return args[0]
}

func mcpgoDescription(c *goCtx, b *mcpgoBuilder, args, _ []ast.Expr) {
	b.tool.Description, _ = c.str(arg0(args))
}

func mcpgoTitle(c *goCtx, b *mcpgoBuilder, args, _ []ast.Expr) {
	b.tool.Title, _ = c.str(arg0(args))
}

func mcpgoTitleAnnotation(c *goCtx, b *mcpgoBuilder, args, _ []ast.Expr) {
	if s, ok := c.str(arg0(args)); ok && s != "" {
		b.ann["title"] = s
	}
}

func mcpgoHint(key string) mcpgoToolOption {
	return func(c *goCtx, b *mcpgoBuilder, args, _ []ast.Expr) {
		v, _ := c.eval(arg0(args), nil)
		if hint, ok := v.(bool); ok {
			b.ann[key] = hint
			return
		}
		delete(b.ann, key)
	}
}

func mcpgoClearAnnotations(_ *goCtx, b *mcpgoBuilder, _, _ []ast.Expr) {
	b.ann = map[string]any{}
}

func mcpgoAnnotation(c *goCtx, b *mcpgoBuilder, args, _ []ast.Expr) {
	b.ann = c.objectValue(arg0(args))
	if b.ann == nil {
		b.ann = map[string]any{}
	}
}

func mcpgoProperty(kind string) mcpgoToolOption {
	return func(c *goCtx, b *mcpgoBuilder, args, _ []ast.Expr) {
		name, ok := c.str(arg0(args))
		if !ok {
			b.bad = true
			return
		}
		schema := map[string]any{}
		if kind != "" {
			schema["type"] = kind
		}
		if kind == "object" {
			schema["properties"] = map[string]any{}
		}
		if !c.applyPropOptions(schema, args[1:]) {
			b.bad = true
		}
		b.setProperty(name, schema)
	}
}

func mcpgoRawInput(c *goCtx, b *mcpgoBuilder, args, _ []ast.Expr) {
	b.raw, b.rawSet = c.objectValue(arg0(args)), true
}

func mcpgoTypedInput(c *goCtx, b *mcpgoBuilder, _, typeArgs []ast.Expr) {
	b.raw, b.rawSet = nil, true
	if len(typeArgs) == 1 {
		b.raw = c.inferType(typeArgs[0])
	}
}

func mcpgoRawOutput(c *goCtx, b *mcpgoBuilder, args, _ []ast.Expr) {
	b.tool.OutputSchema = c.objectValue(arg0(args))
}

// mcpgoTypedOutput mirrors WithOutputSchema[T], which forces type object.
func mcpgoTypedOutput(c *goCtx, b *mcpgoBuilder, _, typeArgs []ast.Expr) {
	b.tool.OutputSchema = nil
	if len(typeArgs) != 1 {
		return
	}
	if s := c.inferType(typeArgs[0]); s != nil {
		s["type"] = "object"
		b.tool.OutputSchema = s
	}
}

func mcpgoAdditional(c *goCtx, b *mcpgoBuilder, args, _ []ast.Expr) {
	v, ok := c.eval(arg0(args), nil)
	if !ok {
		b.bad = true
	}
	b.additional = v
}

// applyPropOptions applies PropertyOptions to a property schema and
// reports whether every option was understood.
func (c *goCtx) applyPropOptions(schema map[string]any, opts []ast.Expr) bool {
	ok := true
	for _, opt := range opts {
		if !c.applyPropOption(schema, opt) {
			ok = false
		}
	}
	return ok
}

func (c *goCtx) applyPropOption(schema map[string]any, opt ast.Expr) bool {
	call, ok := c.resolve(opt).(*ast.CallExpr)
	if !ok {
		return false
	}
	fun, _ := unwrapIndex(call.Fun)
	name := pkgMember(fun, c.file.mcpgo)
	if key, ok := mcpgoPropKeys[name]; ok {
		v, ok := c.eval(arg0(call.Args), nil)
		schema[key] = v
		return ok
	}
	if kind, ok := mcpgoItemKinds[name]; ok {
		items := map[string]any{"type": kind}
		ok := c.applyPropOptions(items, call.Args)
		delete(items, "required")
		schema["items"] = items
		return ok
	}
	return c.applySpecialPropOption(schema, name, call)
}

func (c *goCtx) applySpecialPropOption(schema map[string]any, name string, call *ast.CallExpr) bool {
	switch name {
	case "Required":
		schema["required"] = true
		return true
	case "Enum":
		enum, ok := c.variadicValues(call)
		schema["enum"] = enum
		return ok
	case "WithStringEnumItems":
		enum, ok := c.eval(arg0(call.Args), nil)
		schema["items"] = map[string]any{"type": "string", "enum": enum}
		return ok
	}
	return false
}

// variadicValues evaluates f(a, b, c) or f(list...) arguments to a list.
func (c *goCtx) variadicValues(call *ast.CallExpr) ([]any, bool) {
	if call.Ellipsis.IsValid() {
		v, ok := c.eval(arg0(call.Args), nil)
		list, isList := v.([]any)
		return list, ok && isList
	}
	out := make([]any, 0, len(call.Args))
	for _, a := range call.Args {
		v, ok := c.eval(a, nil)
		if !ok {
			return nil, false
		}
		out = append(out, v)
	}
	return out, true
}

// --- literal evaluation ---

func (c *goCtx) str(e ast.Expr) (string, bool) {
	v, ok := c.eval(e, nil)
	s, isStr := v.(string)
	return s, ok && isStr
}

// objectValue evaluates e to a JSON object, or nil.
func (c *goCtx) objectValue(e ast.Expr) map[string]any {
	v, _ := c.eval(e, nil)
	m, _ := v.(map[string]any)
	return m
}

// eval converts a literal Go expression to the value encoding/json would
// decode its marshaled form into (numbers as float64, lists as []any).
// hint is the element type for type-elided composite literals.
func (c *goCtx) eval(e, hint ast.Expr) (any, bool) {
	switch v := c.resolve(e).(type) {
	case *ast.BasicLit:
		return basicLitValue(v)
	case *ast.Ident:
		return identValue(v.Name)
	case *ast.BinaryExpr:
		return c.evalBinary(v)
	case *ast.UnaryExpr:
		return c.evalUnary(v, hint)
	case *ast.CompositeLit:
		return c.evalComposite(v, hint)
	case *ast.CallExpr:
		return c.evalCall(v)
	}
	return nil, false
}

func basicLitValue(lit *ast.BasicLit) (any, bool) {
	switch lit.Kind {
	case token.STRING:
		s, err := strconv.Unquote(lit.Value)
		return s, err == nil
	case token.INT:
		n, err := strconv.ParseInt(lit.Value, 0, 64)
		return float64(n), err == nil
	case token.FLOAT:
		f, err := strconv.ParseFloat(lit.Value, 64)
		return f, err == nil
	}
	return nil, false
}

func identValue(name string) (any, bool) {
	switch name {
	case "true":
		return true, true
	case "false":
		return false, true
	case "nil":
		return nil, true
	}
	return nil, false
}

func (c *goCtx) evalBinary(v *ast.BinaryExpr) (any, bool) {
	if v.Op != token.ADD {
		return nil, false
	}
	x, okX := c.eval(v.X, nil)
	y, okY := c.eval(v.Y, nil)
	if !okX || !okY {
		return nil, false
	}
	switch a := x.(type) {
	case string:
		b, ok := y.(string)
		return a + b, ok
	case float64:
		b, ok := y.(float64)
		return a + b, ok
	}
	return nil, false
}

func (c *goCtx) evalUnary(v *ast.UnaryExpr, hint ast.Expr) (any, bool) {
	switch v.Op {
	case token.AND:
		return c.eval(v.X, hint)
	case token.SUB:
		x, ok := c.eval(v.X, nil)
		f, isNum := x.(float64)
		return -f, ok && isNum
	}
	return nil, false
}

func (c *goCtx) evalComposite(lit *ast.CompositeLit, hint ast.Expr) (any, bool) {
	typ := lit.Type
	if typ == nil {
		typ = hint
	}
	typ = stripStar(typ)
	switch t := c.underlying(typ).(type) {
	case *ast.MapType:
		return c.evalMapLit(lit, t.Value)
	case *ast.ArrayType:
		return c.evalSliceLit(lit, t.Elt)
	}
	if isSchemaLikeType(typ) {
		return c.evalStructLit(lit)
	}
	return nil, false
}

// underlying resolves package-local named types (type Props map[string]any).
func (c *goCtx) underlying(t ast.Expr) ast.Expr {
	for i := 0; i < maxGoResolve; i++ {
		id, ok := t.(*ast.Ident)
		if !ok {
			return t
		}
		next, ok := c.pkg.types[id.Name]
		if !ok {
			return t
		}
		t = next
	}
	return t
}

// isSchemaLikeType reports struct types whose keyed literals convert to
// JSON by lower-casing field names: jsonschema.Schema, mcp-go's
// ToolInputSchema, and the SDKs' tool annotation structs.
func isSchemaLikeType(t ast.Expr) bool {
	name := typeName(t)
	return strings.HasSuffix(name, "Schema") || strings.HasSuffix(name, "Annotations") || strings.HasSuffix(name, "Annotation")
}

func (c *goCtx) evalMapLit(lit *ast.CompositeLit, valueHint ast.Expr) (any, bool) {
	out := make(map[string]any, len(lit.Elts))
	for _, elt := range lit.Elts {
		kv, ok := elt.(*ast.KeyValueExpr)
		if !ok {
			return nil, false
		}
		key, ok := c.str(kv.Key)
		if !ok {
			return nil, false
		}
		v, ok := c.eval(kv.Value, valueHint)
		if !ok {
			return nil, false
		}
		out[key] = v
	}
	return out, true
}

func (c *goCtx) evalSliceLit(lit *ast.CompositeLit, elemHint ast.Expr) (any, bool) {
	out := make([]any, 0, len(lit.Elts))
	for _, elt := range lit.Elts {
		if _, keyed := elt.(*ast.KeyValueExpr); keyed {
			return nil, false
		}
		v, ok := c.eval(elt, elemHint)
		if !ok {
			return nil, false
		}
		out = append(out, v)
	}
	return out, true
}

func (c *goCtx) evalStructLit(lit *ast.CompositeLit) (any, bool) {
	out := map[string]any{}
	for _, elt := range lit.Elts {
		kv, ok := elt.(*ast.KeyValueExpr)
		if !ok {
			return nil, false
		}
		key, ok := kv.Key.(*ast.Ident)
		if !ok {
			return nil, false
		}
		v, ok := c.eval(kv.Value, nil)
		if !ok {
			return nil, false
		}
		setStructField(out, key.Name, v)
	}
	return out, true
}

// goSchemaFieldKeys lists Go field names whose JSON key is not simply the
// lower-cased name; "" marks fields that are not marshaled. Extra and
// AdditionalFields are merged into the object.
var goSchemaFieldKeys = map[string]string{
	"ID": "$id", "Schema": "$schema", "Ref": "$ref", "Comment": "$comment",
	"Defs": "$defs", "Anchor": "$anchor", "DynamicAnchor": "$dynamicAnchor",
	"DynamicRef": "$dynamicRef", "Vocabulary": "$vocabulary",
	"Types": "type", "ItemsArray": "items",
	"PropertyOrder": "", "DependencySchemas": "", "DependencyStrings": "",
}

func goFieldJSONKey(name string) string {
	if k, ok := goSchemaFieldKeys[name]; ok {
		return k
	}
	return strings.ToLower(name[:1]) + name[1:]
}

func setStructField(out map[string]any, goName string, v any) {
	if goName == "Extra" || goName == "AdditionalFields" {
		if m, ok := v.(map[string]any); ok {
			maps.Copy(out, m)
		}
		return
	}
	if key := goFieldJSONKey(goName); key != "" && v != nil {
		out[key] = v
	}
}

func (c *goCtx) evalCall(call *ast.CallExpr) (any, bool) {
	if len(call.Args) != 1 {
		return nil, false
	}
	fun, _ := unwrapIndex(call.Fun)
	switch {
	case isJSONBytesConversion(fun):
		return c.evalJSON(call.Args[0])
	case isPointerHelper(fun), typeName(fun) == "string":
		return c.eval(call.Args[0], nil)
	}
	return nil, false
}

// isJSONBytesConversion matches json.RawMessage(x) and []byte(x).
func isJSONBytesConversion(fun ast.Expr) bool {
	if sel, ok := fun.(*ast.SelectorExpr); ok {
		return sel.Sel.Name == "RawMessage"
	}
	arr, ok := fun.(*ast.ArrayType)
	return ok && arr.Len == nil && typeName(arr.Elt) == "byte"
}

// isPointerHelper matches the one-argument helpers used for optional
// fields: ptr(true), boolPtr(true), jsonschema.Ptr(5), mcp.ToBoolPtr(x),
// and Go 1.26's new(expr).
func isPointerHelper(fun ast.Expr) bool {
	name := typeName(fun)
	return name == "new" || strings.Contains(strings.ToLower(name), "ptr")
}

func (c *goCtx) evalJSON(arg ast.Expr) (any, bool) {
	v, ok := c.eval(arg, nil)
	s, isStr := v.(string)
	if !ok || !isStr {
		return v, ok
	}
	var out any
	if err := json.Unmarshal([]byte(s), &out); err != nil {
		return nil, false
	}
	return out, true
}

// --- schema inference from Go types (jsonschema-go's ForType rules) ---

var goBasicSchemas = map[string]map[string]any{
	"string":  {"type": "string"},
	"bool":    {"type": "boolean"},
	"int":     {"type": "integer"},
	"int64":   {"type": "integer"},
	"uint":    {"type": "integer", "minimum": 0.0},
	"uint64":  {"type": "integer", "minimum": 0.0},
	"uintptr": {"type": "integer", "minimum": 0.0},
	"int8":    {"type": "integer", "minimum": -128.0, "maximum": 127.0},
	"uint8":   {"type": "integer", "minimum": 0.0, "maximum": 255.0},
	"byte":    {"type": "integer", "minimum": 0.0, "maximum": 255.0},
	"int16":   {"type": "integer", "minimum": -32768.0, "maximum": 32767.0},
	"uint16":  {"type": "integer", "minimum": 0.0, "maximum": 65535.0},
	"int32":   {"type": "integer", "minimum": -2147483648.0, "maximum": 2147483647.0},
	"rune":    {"type": "integer", "minimum": -2147483648.0, "maximum": 2147483647.0},
	"uint32":  {"type": "integer", "minimum": 0.0, "maximum": 4294967295.0},
	"float32": {"type": "number"},
	"float64": {"type": "number"},
	"any":     {},
}

// goStringTypes are standard library types jsonschema-go maps to string
// because they marshal as JSON strings.
var goStringTypes = map[string]bool{
	"time.Time": true, "slog.Level": true, "big.Int": true, "big.Rat": true, "big.Float": true,
}

// typeSchema infers a schema for a type expression; ok is false when the
// type cannot be resolved within the package.
func (c *goCtx) typeSchema(t ast.Expr, seen map[string]bool) (map[string]any, bool) {
	switch v := t.(type) {
	case *ast.Ident:
		return c.identSchema(v.Name, seen)
	case *ast.ParenExpr:
		return c.typeSchema(v.X, seen)
	case *ast.StarExpr:
		return c.pointerSchema(v.X, seen)
	case *ast.ArrayType:
		return c.arraySchema(v, seen), true
	case *ast.MapType:
		return map[string]any{"type": "object", "additionalProperties": c.typeSchemaOrEmpty(v.Value, seen)}, true
	case *ast.StructType:
		return c.structSchema(v, seen), true
	case *ast.InterfaceType:
		return map[string]any{}, true
	case *ast.SelectorExpr:
		return selectorSchema(v)
	}
	return nil, false
}

func (c *goCtx) typeSchemaOrEmpty(t ast.Expr, seen map[string]bool) map[string]any {
	if s, ok := c.typeSchema(t, seen); ok {
		return s
	}
	return map[string]any{}
}

func (c *goCtx) identSchema(name string, seen map[string]bool) (map[string]any, bool) {
	if s, ok := goBasicSchemas[name]; ok {
		return maps.Clone(s), true
	}
	t, ok := c.pkg.types[name]
	if !ok || seen[name] {
		return nil, false
	}
	seen[name] = true
	defer delete(seen, name)
	return c.typeSchema(t, seen)
}

func selectorSchema(sel *ast.SelectorExpr) (map[string]any, bool) {
	x, ok := sel.X.(*ast.Ident)
	if ok && goStringTypes[x.Name+"."+sel.Sel.Name] {
		return map[string]any{"type": "string"}, true
	}
	return nil, false
}

// pointerSchema allows null alongside the pointee's type; pointers do not
// change whether a field is required.
func (c *goCtx) pointerSchema(x ast.Expr, seen map[string]bool) (map[string]any, bool) {
	s, ok := c.typeSchema(x, seen)
	if typ, isStr := s["type"].(string); ok && isStr {
		s["type"] = []any{"null", typ}
	}
	return s, ok
}

// arraySchema follows jsonschema-go: slices may be null, fixed-size
// arrays pin their length.
func (c *goCtx) arraySchema(v *ast.ArrayType, seen map[string]bool) map[string]any {
	items := c.typeSchemaOrEmpty(v.Elt, seen)
	if v.Len == nil {
		return map[string]any{"type": []any{"null", "array"}, "items": items}
	}
	s := map[string]any{"type": "array", "items": items}
	if n, ok := c.eval(v.Len, nil); ok {
		s["minItems"], s["maxItems"] = n, n
	}
	return s
}

// goObject accumulates a struct's properties and required names.
type goObject struct {
	props    map[string]any
	required []any
}

func (c *goCtx) structSchema(st *ast.StructType, seen map[string]bool) map[string]any {
	obj := &goObject{props: map[string]any{}}
	c.collectFields(st, seen, obj)
	s := map[string]any{"type": "object", "additionalProperties": false}
	if len(st.Fields.List) > 0 {
		s["properties"] = obj.props
	}
	if len(obj.required) > 0 {
		s["required"] = obj.required
	}
	return s
}

// collectFields visits embedded structs first so the outer struct's own
// fields win name clashes, as with encoding/json's promotion rules.
func (c *goCtx) collectFields(st *ast.StructType, seen map[string]bool, obj *goObject) {
	for _, f := range st.Fields.List {
		if len(f.Names) == 0 {
			c.collectEmbedded(f.Type, seen, obj)
		}
	}
	for _, f := range st.Fields.List {
		tag := fieldTag(f)
		for _, n := range f.Names {
			c.addField(n.Name, f.Type, tag, seen, obj)
		}
	}
}

func (c *goCtx) collectEmbedded(t ast.Expr, seen map[string]bool, obj *goObject) {
	id, ok := stripStar(t).(*ast.Ident)
	if !ok || seen[id.Name] {
		return
	}
	st, ok := c.pkg.types[id.Name].(*ast.StructType)
	if !ok {
		return
	}
	seen[id.Name] = true
	defer delete(seen, id.Name)
	c.collectFields(st, seen, obj)
}

func (c *goCtx) addField(name string, typ ast.Expr, tag reflect.StructTag, seen map[string]bool, obj *goObject) {
	if !ast.IsExported(name) {
		return
	}
	jsonName, opts, skip := jsonFieldName(tag, name)
	if skip {
		return
	}
	s := c.typeSchemaOrEmpty(typ, seen)
	if desc := jsonschemaDescription(tag); desc != "" {
		s["description"] = desc
	}
	obj.props[jsonName] = s
	if !opts["omitempty"] && !opts["omitzero"] {
		obj.required = appendUniqueName(obj.required, jsonName)
	}
}

func fieldTag(f *ast.Field) reflect.StructTag {
	if f.Tag == nil {
		return ""
	}
	s, err := strconv.Unquote(f.Tag.Value)
	if err != nil {
		return ""
	}
	return reflect.StructTag(s)
}

// jsonFieldName applies encoding/json's tag rules: "-" omits the field,
// "-," names it "-", and an empty name keeps the Go field name.
func jsonFieldName(tag reflect.StructTag, goName string) (name string, opts map[string]bool, skip bool) {
	v, ok := tag.Lookup("json")
	if !ok {
		return goName, nil, false
	}
	name, rest, found := strings.Cut(v, ",")
	if name == "-" && !found {
		return "", nil, true
	}
	if name == "" {
		name = goName
	}
	opts = map[string]bool{}
	for _, o := range strings.Split(rest, ",") {
		opts[o] = true
	}
	return name, opts, false
}

// goTagWordPrefix is jsonschema-go's reserved "WORD=" tag form, which it
// rejects rather than treating as a description.
var goTagWordPrefix = regexp.MustCompile(`^[^ \t\n]*=`)

func jsonschemaDescription(tag reflect.StructTag) string {
	desc := tag.Get("jsonschema")
	if goTagWordPrefix.MatchString(desc) {
		return ""
	}
	return desc
}
