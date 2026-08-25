// Package pointless implements an analyzer that suggests values instead of
// pointers for small structs.
package pointless

import (
	"fmt"
	"go/ast"
	"go/token"
	"go/types"
	"strings"

	"golang.org/x/tools/go/analysis"
	"golang.org/x/tools/go/analysis/passes/inspect"
	"golang.org/x/tools/go/ast/inspector"
	"golang.org/x/tools/go/types/typeutil"
)

const doc = `suggests values instead of pointers for small structs

pointless reports pointer receivers, pointer return types, and slices of
pointers that could be values: the struct is small, copying it is cheap, and
nothing relies on pointer semantics such as mutation through the pointer,
identity, or nil.

Whether a method writes to its receiver, and whether a function writes through
or retains a pointer parameter, is analyzed across packages and shared as facts.`

// Settings configures the analyzer.
type Settings struct {
	// Threshold is the largest struct size in bytes that is still suggested
	// as a value.
	Threshold int
	// Receivers selects how pointer receivers are reported: "type" reports a
	// type once when none of its pointer-receiver methods needs the pointer;
	// "method" reports each such method.
	Receivers string
	// Returns enables the check of pointer return types.
	Returns bool
	// Slices enables the check of slices of pointers.
	Slices bool
}

const (
	// ReceiversType reports a type once when all of its pointer-receiver
	// methods can use value receivers.
	ReceiversType = "type"
	// ReceiversMethod reports every pointer-receiver method that can use a
	// value receiver.
	ReceiversMethod = "method"
)

// DefaultSettings returns the default settings.
func DefaultSettings() Settings {
	return Settings{
		Threshold: 256,
		Receivers: ReceiversType,
		Returns:   true,
		Slices:    false,
	}
}

// NewAnalyzer returns the pointless analyzer configured with s.
func NewAnalyzer(s Settings) *analysis.Analyzer {
	a := &analysis.Analyzer{
		Name:      "pointless",
		Doc:       doc,
		URL:       "https://github.com/go-by-value/pointless",
		Requires:  []*analysis.Analyzer{inspect.Analyzer},
		FactTypes: []analysis.Fact{(*writeFact)(nil)},
	}
	a.Flags.IntVar(&s.Threshold, "threshold", s.Threshold, "largest struct size in bytes suggested as a value")
	a.Flags.StringVar(&s.Receivers, "receivers", s.Receivers, `report pointer receivers per "type" or per "method"`)
	a.Flags.BoolVar(&s.Returns, "returns", s.Returns, "check pointer return types")
	a.Flags.BoolVar(&s.Slices, "slices", s.Slices, "check slices of pointers")
	a.Run = func(pass *analysis.Pass) (any, error) {
		return run(pass, s)
	}

	return a
}

// effect is a set of flags describing what a function does with the memory a
// pointer refers to.
type effect uint8

const (
	// effectDirect: writes to storage owned by the pointee (its fields, nested
	// structs, and arrays).
	effectDirect effect = 1 << iota
	// effectShared: writes to memory reachable from the pointee but shared with
	// copies of it (through pointer, slice, or map fields).
	effectShared
	// effectEscape: the pointer is retained, converted, or passed to code we
	// cannot see.
	effectEscape
)

func (e effect) String() string {
	if e == 0 {
		return "none"
	}
	var parts []string
	if e&effectDirect != 0 {
		parts = append(parts, "direct")
	}
	if e&effectShared != 0 {
		parts = append(parts, "shared")
	}
	if e&effectEscape != 0 {
		parts = append(parts, "escape")
	}

	return strings.Join(parts, "+")
}

// needsPointer reports whether a value copy could not stand in for the
// pointer: the function writes to the pointee itself or retains the pointer.
func (e effect) needsPointer() bool {
	return e&(effectDirect|effectEscape) != 0
}

// writeFact records what a function does with its pointer receiver and its
// pointer parameters. Parameters that are not pointers always have no effect.
type writeFact struct {
	Recv   effect
	Params []effect
}

func (writeFact) AFact() {}

func (f writeFact) String() string {
	parts := make([]string, 0, 1+len(f.Params))
	parts = append(parts, "recv="+f.Recv.String())
	for i, p := range f.Params {
		parts = append(parts, fmt.Sprintf("p%d=%s", i, p))
	}

	return strings.Join(parts, " ")
}

func (f writeFact) param(i int) effect {
	if i < 0 || i >= len(f.Params) {
		return effectEscape
	}

	return f.Params[i]
}

func (f writeFact) isZero() bool {
	if f.Recv != 0 {
		return false
	}
	for _, p := range f.Params {
		if p != 0 {
			return false
		}
	}

	return true
}

func (f writeFact) equal(g writeFact) bool {
	if f.Recv != g.Recv || len(f.Params) != len(g.Params) {
		return false
	}
	for i := range f.Params {
		if f.Params[i] != g.Params[i] {
			return false
		}
	}

	return true
}

type siteKind uint8

const (
	// siteRead: the root is read.
	siteRead siteKind = iota
	// siteWrite: an assignment or ++/-- targets storage reached from the root.
	siteWrite
	// siteMethodCall: a pointer-receiver method is called on storage reached
	// from the root.
	siteMethodCall
	// siteAddrCall: the address of storage reached from the root is passed to
	// a call.
	siteAddrCall
	// siteArgCall: the root itself, a pointer, is passed to a call.
	siteArgCall
	// siteEscape: the root, a pointer, is stored, returned, reassigned,
	// converted, or otherwise leaves our sight.
	siteEscape
)

// site is one use of a pointer root variable.
type site struct {
	kind siteKind
	// shared is true when the path from the root to the used storage goes
	// through memory shared with copies of the pointee.
	shared bool
	// callee is the called function for call kinds; nil when unknown.
	callee *types.Func
	// param is the parameter index for siteAddrCall and siteArgCall.
	param int
}

// funcInfo is a function with pointer roots whose fact we compute.
type funcInfo struct {
	decl *ast.FuncDecl
	fn   *types.Func
	// index maps each root to its parameter index; the receiver is -1.
	index map[types.Object]int
	sites map[types.Object][]site
	fact  writeFact
}

// sliceInfo is a local variable of type []*T under consideration.
type sliceInfo struct {
	v            *types.Var
	elem         *types.Named
	disqualified bool
	// elemSites collects the uses of range variables over the slice.
	elemSites []site
}

type root struct {
	fn    *funcInfo
	slice *sliceInfo
}

type analyzer struct {
	pass     *analysis.Pass
	settings Settings
	funcs    map[*types.Func]*funcInfo
	decls    []*funcInfo
	slices   map[types.Object]*sliceInfo
	sliceOrd []*sliceInfo
	roots    map[types.Object]root
	// generated holds the names of files marked "Code generated ... DO NOT
	// EDIT."; suggestions in them are not actionable.
	generated map[string]bool
}

// reportf reports a diagnostic unless pos is in a generated file.
func (a *analyzer) reportf(pos token.Pos, format string, args ...any) {
	if a.generated[a.pass.Fset.File(pos).Name()] {
		return
	}
	a.pass.Reportf(pos, format, args...)
}

func run(pass *analysis.Pass, s Settings) (any, error) {
	insp, ok := pass.ResultOf[inspect.Analyzer].(*inspector.Inspector)
	if !ok {
		return nil, fmt.Errorf("unexpected result type for %s", inspect.Analyzer.Name)
	}
	if s.Receivers != ReceiversType && s.Receivers != ReceiversMethod {
		return nil, fmt.Errorf("invalid receivers setting %q (want %q or %q)", s.Receivers, ReceiversType, ReceiversMethod)
	}

	a := analyzer{
		pass:      pass,
		settings:  s,
		funcs:     map[*types.Func]*funcInfo{},
		slices:    map[types.Object]*sliceInfo{},
		roots:     map[types.Object]root{},
		generated: map[string]bool{},
	}
	for _, f := range pass.Files {
		if ast.IsGenerated(f) {
			a.generated[pass.Fset.File(f.Pos()).Name()] = true
		}
	}
	a.collectFuncs(insp)
	if s.Slices {
		a.collectSlices(insp)
	}
	a.collectSites(insp)
	a.computeFacts()

	a.checkReceivers()
	if s.Returns {
		a.checkReturns()
	}
	if s.Slices {
		a.checkSlices()
	}

	return nil, nil
}

// --- Collection ---

func (a *analyzer) collectFuncs(insp *inspector.Inspector) {
	insp.Preorder([]ast.Node{(*ast.FuncDecl)(nil)}, func(n ast.Node) {
		decl, ok := n.(*ast.FuncDecl)
		if !ok {
			return
		}
		fn, ok := a.pass.TypesInfo.Defs[decl.Name].(*types.Func)
		if !ok {
			return
		}
		sig, ok := fn.Type().(*types.Signature)
		if !ok {
			return
		}

		fi := funcInfo{
			decl:  decl,
			fn:    fn,
			index: map[types.Object]int{},
			sites: map[types.Object][]site{},
			fact:  writeFact{Params: make([]effect, sig.Params().Len())},
		}
		a.decls = append(a.decls, &fi)
		if decl.Body == nil {
			return
		}

		if recv := sig.Recv(); recv != nil && isPointer(recv.Type()) {
			fi.index[recv] = -1
		}
		for i := range sig.Params().Len() {
			p := sig.Params().At(i)
			if isPointer(p.Type()) {
				fi.index[p] = i
			}
		}
		if len(fi.index) == 0 {
			return
		}

		a.funcs[fn] = &fi
		for obj := range fi.index {
			a.roots[obj] = root{fn: &fi}
		}
	})
}

// collectSlices registers local variables of type []*T whose element type is
// eligible, and the value variables of range loops over them.
func (a *analyzer) collectSlices(insp *inspector.Inspector) {
	nodeFilter := []ast.Node{
		(*ast.ValueSpec)(nil),
		(*ast.AssignStmt)(nil),
		(*ast.RangeStmt)(nil),
	}
	insp.Preorder(nodeFilter, func(n ast.Node) {
		switch n := n.(type) {
		case *ast.ValueSpec:
			a.collectSliceVars(n.Names, n.Values)
		case *ast.AssignStmt:
			if n.Tok == token.DEFINE && len(n.Lhs) == len(n.Rhs) {
				a.collectSliceVars(identsOf(n.Lhs), n.Rhs)
			}
		case *ast.RangeStmt:
			a.collectSliceRange(n)
		}
	})
}

func (a *analyzer) collectSliceVars(names []*ast.Ident, values []ast.Expr) {
	for i, name := range names {
		v, ok := a.pass.TypesInfo.Defs[name].(*types.Var)
		if !ok || v.Parent() == nil || v.Parent() == a.pass.Pkg.Scope() {
			continue
		}
		elem := a.pointerSliceElem(v.Type())
		if elem == nil || !a.eligibleElem(elem) || !a.sameModule(elem) {
			continue
		}

		si := sliceInfo{v: v, elem: elem}
		if i < len(values) && !a.isFreshSlice(values[i], elem) {
			si.disqualified = true
		}
		a.slices[v] = &si
		a.sliceOrd = append(a.sliceOrd, &si)
	}
}

func (a *analyzer) collectSliceRange(rs *ast.RangeStmt) {
	ident, ok := rs.X.(*ast.Ident)
	if !ok {
		return
	}
	si, ok := a.slices[a.pass.TypesInfo.Uses[ident]]
	if !ok {
		return
	}
	if rs.Tok != token.DEFINE {
		si.disqualified = true

		return
	}
	value, ok := rs.Value.(*ast.Ident)
	if !ok || value.Name == "_" {
		return
	}
	v, ok := a.pass.TypesInfo.Defs[value].(*types.Var)
	if !ok {
		return
	}
	a.roots[v] = root{slice: si}
}

func (a *analyzer) collectSites(insp *inspector.Inspector) {
	insp.WithStack([]ast.Node{(*ast.Ident)(nil)}, func(n ast.Node, push bool, stack []ast.Node) bool {
		if !push {
			return false
		}
		ident, ok := n.(*ast.Ident)
		if !ok {
			return false
		}
		obj := a.pass.TypesInfo.Uses[ident]
		if obj == nil {
			return false
		}

		if si, tracked := a.slices[obj]; tracked {
			if !a.sliceUseAllowed(stack, si.elem) {
				si.disqualified = true
			}

			return false
		}

		r, ok := a.roots[obj]
		if !ok {
			return false
		}
		s := a.classify(stack)
		if r.fn != nil {
			r.fn.sites[obj] = append(r.fn.sites[obj], s)
		} else {
			r.slice.elemSites = append(r.slice.elemSites, s)
		}

		return false
	})
}

// classify describes how the pointer identifier at the top of stack is used.
func (a *analyzer) classify(stack []ast.Node) site {
	info := a.pass.TypesInfo

	i := len(stack) - 1
	loc, _ := stack[i].(ast.Expr)
	derefs := 0
	shared := false
	plain := true
	var method *types.Func

walk:
	for i > 0 {
		switch p := stack[i-1].(type) {
		case *ast.ParenExpr:
			loc = p
		case *ast.SelectorExpr:
			if p.X != loc {
				break walk
			}
			sel := info.Selections[p]
			if sel == nil {
				break walk
			}
			d, ok := selectionDerefs(sel)
			if !ok {
				shared = true
			}
			derefs += d
			plain = false
			loc = p
			if sel.Kind() != types.FieldVal {
				method, _ = sel.Obj().(*types.Func)
				i--

				break walk
			}
		case *ast.IndexExpr:
			if p.X != loc {
				break walk
			}
			switch under(info.TypeOf(p.X)).(type) {
			case *types.Array:
			case *types.Pointer:
				derefs++
			default:
				shared = true
			}
			plain = false
			loc = p
		case *ast.StarExpr:
			derefs++
			plain = false
			loc = p
		default:
			break walk
		}
		i--
	}

	s := site{kind: siteRead, shared: shared || derefs > 1}

	parent := stack[i-1]
	if method != nil {
		call, ok := parent.(*ast.CallExpr)
		if !ok || call.Fun != loc {
			if hasPointerReceiver(method) {
				s.kind = siteEscape
			}

			return s
		}
		if !hasPointerReceiver(method) {
			return s
		}
		s.kind = siteMethodCall
		s.callee = method.Origin()

		return s
	}

	switch p := parent.(type) {
	case *ast.AssignStmt:
		if p.Tok != token.DEFINE && containsExpr(p.Lhs, loc) {
			if plain {
				s.kind = siteEscape
			} else {
				s.kind = siteWrite
			}

			return s
		}
		if plain {
			s.kind = siteEscape
		}
	case *ast.IncDecStmt:
		if !plain {
			s.kind = siteWrite
		}
	case *ast.RangeStmt:
		if p.Tok == token.ASSIGN && (p.Key == loc || p.Value == loc) && !plain {
			s.kind = siteWrite
		}
	case *ast.UnaryExpr:
		if p.Op != token.AND {
			return s
		}
		if plain {
			s.kind = siteEscape

			return s
		}
		call, ok := stack[i-2].(*ast.CallExpr)
		idx := argIndex(call, p)
		if !ok || idx < 0 {
			s.kind = siteEscape

			return s
		}
		s.kind = siteAddrCall
		s.callee, s.param = a.calleeParam(call, idx)
	case *ast.CallExpr:
		if !plain {
			return s
		}
		idx := argIndex(p, loc)
		if idx < 0 {
			s.kind = siteEscape

			return s
		}
		s.kind = siteArgCall
		s.callee, s.param = a.calleeParam(p, idx)
	default:
		// Comparing the pointer itself (identity, or a nil check) and every
		// other use of the bare pointer depend on it being a pointer.
		if plain {
			s.kind = siteEscape
		}
	}

	return s
}

// calleeParam resolves the function called by call and the index of the
// parameter that receives argument idx. The function is nil when unknown.
func (a *analyzer) calleeParam(call *ast.CallExpr, idx int) (*types.Func, int) {
	fn, ok := typeutil.Callee(a.pass.TypesInfo, call).(*types.Func)
	if !ok {
		return nil, 0
	}
	sig, ok := fn.Type().(*types.Signature)
	if !ok {
		return nil, 0
	}

	// In a method expression call T.M(recv, args...), the first argument is
	// the receiver.
	if sel, ok := call.Fun.(*ast.SelectorExpr); ok {
		if s := a.pass.TypesInfo.Selections[sel]; s != nil && s.Kind() == types.MethodExpr {
			idx--
			if idx < 0 {
				return nil, 0
			}
		}
	}

	n := sig.Params().Len()
	if sig.Variadic() && idx >= n-1 {
		idx = n - 1
	}
	if idx >= n {
		return nil, 0
	}

	return fn.Origin(), idx
}

// --- Facts ---

// computeFacts derives the write fact of every function in the package. Facts
// depend on the facts of callees within the package, so iterate to a fixpoint;
// effects only grow, so the iteration terminates.
func (a *analyzer) computeFacts() {
	for changed := true; changed; {
		changed = false
		for _, fi := range a.funcs {
			f := writeFact{Params: make([]effect, len(fi.fact.Params))}
			for obj, sites := range fi.sites {
				var e effect
				for _, s := range sites {
					e |= a.effectOf(s)
				}
				if i := fi.index[obj]; i < 0 {
					f.Recv = e
				} else {
					f.Params[i] = e
				}
			}
			if !fi.fact.equal(f) {
				fi.fact = f
				changed = true
			}
		}
	}

	for fn, fi := range a.funcs {
		if !fi.fact.isZero() {
			a.pass.ExportObjectFact(fn, &fi.fact)
		}
	}
}

func (a *analyzer) effectOf(s site) effect {
	switch s.kind {
	case siteWrite:
		if s.shared {
			return effectShared
		}

		return effectDirect
	case siteMethodCall:
		if s.shared {
			return effectShared
		}
		f := a.factOf(s.callee)
		if f == nil {
			return effectEscape
		}

		return f.Recv
	case siteAddrCall:
		if s.shared {
			return effectShared
		}
		f := a.factOf(s.callee)
		if f == nil {
			return effectEscape
		}

		return f.param(s.param)
	case siteArgCall:
		f := a.factOf(s.callee)
		if f == nil {
			return effectEscape
		}

		return f.param(s.param)
	case siteEscape:
		return effectEscape
	default:
		return 0
	}
}

// factOf returns the write fact of fn, or nil when it is unknown.
func (a *analyzer) factOf(fn *types.Func) *writeFact {
	if fn == nil {
		return nil
	}
	if fi, ok := a.funcs[fn]; ok {
		return &fi.fact
	}

	var f writeFact
	if a.pass.ImportObjectFact(fn, &f) {
		return &f
	}

	return nil
}

// --- Receivers ---

// checkReceivers reports pointer-receiver methods that never need the
// pointer, per type or per method depending on the settings.
func (a *analyzer) checkReceivers() {
	type methodInfo struct {
		fi       *funcInfo
		canValue bool
	}
	byType := map[*types.Named][]methodInfo{}
	var order []*types.Named

	for _, fi := range a.decls {
		sig, ok := fi.fn.Type().(*types.Signature)
		if !ok || sig.Recv() == nil {
			continue
		}
		ptr, ok := under(sig.Recv().Type()).(*types.Pointer)
		if !ok {
			continue
		}
		named, ok := types.Unalias(ptr.Elem()).(*types.Named)
		if !ok {
			continue
		}
		if _, seen := byType[named]; !seen {
			order = append(order, named)
		}

		canValue := fi.decl.Body != nil && !fi.fact.Recv.needsPointer()
		byType[named] = append(byType[named], methodInfo{fi: fi, canValue: canValue})
	}

	for _, named := range order {
		size, ok := a.eligibleSize(named)
		if !ok {
			continue
		}
		methods := byType[named]

		if a.settings.Receivers == ReceiversMethod {
			for _, m := range methods {
				if !m.canValue {
					continue
				}
				a.reportf(m.fi.decl.Recv.Pos(),
					"%s can use a value receiver: %s is %d bytes and %s does not write to the receiver",
					m.fi.fn.Name(), named.Obj().Name(), size, m.fi.fn.Name())
			}

			continue
		}

		all := true
		for _, m := range methods {
			if !m.canValue {
				all = false

				break
			}
		}
		if !all {
			continue
		}
		a.reportf(named.Obj().Pos(),
			"methods of %s can use value receivers: %s is %d bytes and none of its pointer-receiver methods writes to the receiver",
			named.Obj().Name(), named.Obj().Name(), size)
	}
}

// --- Returns ---

// checkReturns reports functions that return *T where every return allocates
// a fresh T, so the caller could receive a T instead.
func (a *analyzer) checkReturns() {
	for _, fi := range a.decls {
		decl := fi.decl
		if decl.Body == nil || decl.Recv != nil || decl.Type.Results == nil {
			continue
		}

		results := a.resultTypeExprs(decl)
		returns := returnStmts(decl.Body)
		if len(returns) == 0 {
			continue
		}

		for i, expr := range results {
			named := a.pointerElem(a.pass.TypesInfo.TypeOf(expr))
			if named == nil || !a.eligibleElem(named) {
				continue
			}
			size, ok := a.eligibleSize(named)
			if !ok {
				continue
			}
			if !a.allReturnsFresh(returns, i, len(results), named) {
				continue
			}

			a.reportf(expr.Pos(),
				"%s can return %s instead of *%s: %s is %d bytes and every return allocates a new value",
				decl.Name.Name, named.Obj().Name(), named.Obj().Name(), named.Obj().Name(), size)
		}
	}
}

// resultTypeExprs returns one type expression per result, expanding grouped
// results such as (a, b *T).
func (a *analyzer) resultTypeExprs(decl *ast.FuncDecl) []ast.Expr {
	var out []ast.Expr
	for _, field := range decl.Type.Results.List {
		n := max(len(field.Names), 1)
		for range n {
			out = append(out, field.Type)
		}
	}

	return out
}

func (a *analyzer) allReturnsFresh(returns []*ast.ReturnStmt, idx, n int, named *types.Named) bool {
	for _, ret := range returns {
		if len(ret.Results) != n {
			return false
		}
		if !a.isFresh(ret.Results[idx], named) {
			return false
		}
	}

	return true
}

// --- Slices ---

// checkSlices reports local []*T variables and []*T results whose elements are
// always fresh allocations and never used as pointers.
func (a *analyzer) checkSlices() {
	for _, si := range a.sliceOrd {
		for _, s := range si.elemSites {
			if a.effectOf(s).needsPointer() {
				si.disqualified = true

				break
			}
		}
	}

	for _, si := range a.sliceOrd {
		if si.disqualified {
			continue
		}
		size, ok := a.eligibleSize(si.elem)
		if !ok {
			continue
		}
		name := si.elem.Obj().Name()
		a.reportf(si.v.Pos(),
			"%s can be []%s instead of []*%s: %s is %d bytes and the elements are never nil or used as pointers",
			si.v.Name(), name, name, name, size)
	}

	for _, fi := range a.decls {
		decl := fi.decl
		if decl.Body == nil || decl.Type.Results == nil {
			continue
		}
		results := a.resultTypeExprs(decl)
		returns := returnStmts(decl.Body)
		if len(returns) == 0 {
			continue
		}

		for i, expr := range results {
			named := a.pointerSliceElem(a.pass.TypesInfo.TypeOf(expr))
			if named == nil || !a.eligibleElem(named) || !a.sameModule(named) {
				continue
			}
			size, ok := a.eligibleSize(named)
			if !ok {
				continue
			}
			if !a.allReturnsFreshSlice(returns, i, len(results), named) {
				continue
			}

			name := named.Obj().Name()
			a.reportf(expr.Pos(),
				"%s can return []%s instead of []*%s: %s is %d bytes and the elements are never nil or used as pointers",
				decl.Name.Name, name, name, name, size)
		}
	}
}

func (a *analyzer) allReturnsFreshSlice(returns []*ast.ReturnStmt, idx, n int, named *types.Named) bool {
	for _, ret := range returns {
		if len(ret.Results) != n {
			return false
		}
		expr := ret.Results[idx]
		if ident, ok := expr.(*ast.Ident); ok {
			if si, ok := a.slices[a.pass.TypesInfo.Uses[ident]]; ok && !si.disqualified {
				continue
			}
		}
		if !a.isFreshSlice(expr, named) {
			return false
		}
	}

	return true
}

// sliceUseAllowed reports whether the use of a tracked slice variable at the
// top of stack is compatible with changing the variable to []T.
func (a *analyzer) sliceUseAllowed(stack []ast.Node, elem *types.Named) bool {
	ident := stack[len(stack)-1]
	parent := stack[len(stack)-2]

	switch p := parent.(type) {
	case *ast.IndexExpr:
		if p.X != ident {
			return true
		}

		return a.elementUseAllowed(stack[:len(stack)-1], elem)
	case *ast.CallExpr:
		return a.sliceCallAllowed(p, ident, elem)
	case *ast.RangeStmt:
		return p.X == ident
	case *ast.AssignStmt:
		if containsExpr(p.Lhs, ident) {
			return true
		}

		return false
	case *ast.ReturnStmt:
		return true
	case *ast.BinaryExpr:
		return isNil(p.X) || isNil(p.Y)
	default:
		return false
	}
}

// elementUseAllowed reports whether the use of s[i] at the top of stack is
// compatible with s becoming []T.
func (a *analyzer) elementUseAllowed(stack []ast.Node, elem *types.Named) bool {
	index := stack[len(stack)-1]
	parent := stack[len(stack)-2]

	switch p := parent.(type) {
	case *ast.SelectorExpr:
		if p.X != index {
			return false
		}
		sel := a.pass.TypesInfo.Selections[p]
		if sel == nil {
			return false
		}
		if sel.Kind() == types.FieldVal {
			return true
		}
		method, ok := sel.Obj().(*types.Func)
		if !ok {
			return false
		}
		if !hasPointerReceiver(method) {
			return true
		}
		f := a.factOf(method.Origin())

		return f != nil && f.Recv&effectEscape == 0
	case *ast.AssignStmt:
		if p.Tok == token.DEFINE || !containsExpr(p.Lhs, index) {
			return false
		}
		for i, lhs := range p.Lhs {
			if lhs == index {
				return i < len(p.Rhs) && a.isFresh(p.Rhs[i], elem)
			}
		}

		return false
	default:
		return false
	}
}

func (a *analyzer) sliceCallAllowed(call *ast.CallExpr, arg ast.Node, elem *types.Named) bool {
	fun, ok := call.Fun.(*ast.Ident)
	if !ok {
		return false
	}
	obj := a.pass.TypesInfo.Uses[fun]
	if obj == nil || obj != types.Universe.Lookup(fun.Name) {
		return false
	}

	switch fun.Name {
	case "len", "cap":
		return true
	case "append":
		if len(call.Args) == 0 || call.Args[0] != arg || call.Ellipsis.IsValid() {
			return false
		}
		for _, e := range call.Args[1:] {
			if !a.isFresh(e, elem) {
				return false
			}
		}

		return true
	default:
		return false
	}
}

// --- Type helpers ---

// eligibleElem reports whether T is a struct or array type that could be used
// as a value: it is not generic, has no pointer-receiver methods (so T and *T
// satisfy the same interfaces), and must not be copied.
func (a *analyzer) eligibleElem(named *types.Named) bool {
	if named.TypeParams().Len() > 0 || named.TypeArgs().Len() > 0 {
		return false
	}
	switch under(named).(type) {
	case *types.Struct, *types.Array:
	default:
		return false
	}
	if types.NewMethodSet(types.NewPointer(named)).Len() > types.NewMethodSet(named).Len() {
		return false
	}

	return !hasLock(named, map[types.Type]bool{})
}

// eligibleSize reports the size of a type and whether it is within the
// threshold and a plausible value type: not generic, safe to copy, not an
// error, and not a node of a linked structure.
func (a *analyzer) eligibleSize(named *types.Named) (int64, bool) {
	if named.TypeParams().Len() > 0 || named.TypeArgs().Len() > 0 {
		return 0, false
	}
	switch under(named).(type) {
	case *types.Struct, *types.Array:
	default:
		return 0, false
	}
	if hasLock(named, map[types.Type]bool{}) || implementsError(named) || selfReferential(named) {
		return 0, false
	}
	size := a.pass.TypesSizes.Sizeof(named)
	if size > int64(a.settings.Threshold) {
		return size, false
	}

	return size, true
}

func (a *analyzer) sameModule(named *types.Named) bool {
	pkg := named.Obj().Pkg()
	if pkg == nil {
		return false
	}
	if pkg == a.pass.Pkg {
		return true
	}
	if a.pass.Module == nil {
		return false
	}

	return pkg.Path() == a.pass.Module.Path || strings.HasPrefix(pkg.Path(), a.pass.Module.Path+"/")
}

// pointerElem returns T when t is *T for a named T.
func (a *analyzer) pointerElem(t types.Type) *types.Named {
	ptr, ok := types.Unalias(t).(*types.Pointer)
	if !ok {
		return nil
	}
	named, _ := types.Unalias(ptr.Elem()).(*types.Named)

	return named
}

// pointerSliceElem returns T when t is []*T for a named T.
func (a *analyzer) pointerSliceElem(t types.Type) *types.Named {
	slice, ok := types.Unalias(t).(*types.Slice)
	if !ok {
		return nil
	}

	return a.pointerElem(slice.Elem())
}

// isFresh reports whether expr allocates a new T: &T{...}, new(T), or a
// {...} element of a []*T literal, which elides the &T.
func (a *analyzer) isFresh(expr ast.Expr, named *types.Named) bool {
	switch e := ast.Unparen(expr).(type) {
	case *ast.CompositeLit:
		t := types.Unalias(a.pass.TypesInfo.TypeOf(e))
		if ptr, ok := t.(*types.Pointer); ok {
			t = types.Unalias(ptr.Elem())
		}

		return types.Identical(t, named)
	case *ast.UnaryExpr:
		lit, ok := e.X.(*ast.CompositeLit)
		if e.Op != token.AND || !ok {
			return false
		}

		return types.Identical(types.Unalias(a.pass.TypesInfo.TypeOf(lit)), named)
	case *ast.CallExpr:
		fun, ok := e.Fun.(*ast.Ident)
		if !ok || fun.Name != "new" || len(e.Args) != 1 {
			return false
		}
		if a.pass.TypesInfo.Uses[fun] != types.Universe.Lookup("new") {
			return false
		}

		return types.Identical(types.Unalias(a.pass.TypesInfo.TypeOf(e.Args[0])), named)
	default:
		return false
	}
}

// isFreshSlice reports whether expr is nil, make([]*T, ...), or a []*T
// literal whose elements are all fresh.
func (a *analyzer) isFreshSlice(expr ast.Expr, named *types.Named) bool {
	switch e := ast.Unparen(expr).(type) {
	case *ast.Ident:
		return isNil(e)
	case *ast.CallExpr:
		fun, ok := e.Fun.(*ast.Ident)

		return ok && fun.Name == "make" && a.pass.TypesInfo.Uses[fun] == types.Universe.Lookup("make")
	case *ast.CompositeLit:
		for _, elt := range e.Elts {
			if kv, ok := elt.(*ast.KeyValueExpr); ok {
				elt = kv.Value
			}
			if !a.isFresh(elt, named) {
				return false
			}
		}

		return true
	default:
		return false
	}
}

// hasLock reports whether t or anything it contains by value must not be
// copied, using the same rule as vet's copylocks: a Lock method with a
// pointer receiver.
func hasLock(t types.Type, seen map[types.Type]bool) bool {
	if seen[t] {
		return false
	}
	seen[t] = true

	if named, ok := types.Unalias(t).(*types.Named); ok {
		ms := types.NewMethodSet(types.NewPointer(named))
		if sel := ms.Lookup(nil, "Lock"); sel != nil {
			if fn, ok := sel.Obj().(*types.Func); ok && hasPointerReceiver(fn) {
				return true
			}
		}
	}

	switch u := under(t).(type) {
	case *types.Struct:
		for i := range u.NumFields() {
			if hasLock(u.Field(i).Type(), seen) {
				return true
			}
		}
	case *types.Array:
		return hasLock(u.Elem(), seen)
	}

	return false
}

// implementsError reports whether *T implements error. Error types are
// conventionally used through one receiver kind only, and errors.As depends
// on it, so pointless leaves them alone.
func implementsError(named *types.Named) bool {
	errType, ok := types.Universe.Lookup("error").Type().Underlying().(*types.Interface)
	if !ok {
		return false
	}

	return types.Implements(types.NewPointer(named), errType)
}

// selfReferential reports whether T has a field that refers to *T, directly
// or through slices, arrays, maps, or channels: such types are nodes of a
// linked structure and their pointers are identities.
func selfReferential(named *types.Named) bool {
	st, ok := under(named).(*types.Struct)
	if !ok {
		return false
	}
	for i := range st.NumFields() {
		if refersTo(st.Field(i).Type(), named, 0) {
			return true
		}
	}

	return false
}

func refersTo(t types.Type, named *types.Named, depth int) bool {
	if depth > 4 {
		return false
	}
	switch u := types.Unalias(t).(type) {
	case *types.Pointer:
		return types.Identical(types.Unalias(u.Elem()), named) || refersTo(u.Elem(), named, depth+1)
	case *types.Slice:
		return refersTo(u.Elem(), named, depth+1)
	case *types.Array:
		return refersTo(u.Elem(), named, depth+1)
	case *types.Map:
		return refersTo(u.Key(), named, depth+1) || refersTo(u.Elem(), named, depth+1)
	case *types.Chan:
		return refersTo(u.Elem(), named, depth+1)
	default:
		return false
	}
}

// selectionDerefs counts the pointer indirections on the path from the
// receiver of sel to the selected field or method. ok is false when the path
// goes through something other than structs.
func selectionDerefs(sel *types.Selection) (derefs int, ok bool) {
	t := sel.Recv()
	if p, isPtr := under(t).(*types.Pointer); isPtr {
		derefs++
		t = p.Elem()
	}

	index := sel.Index()
	for _, i := range index[:len(index)-1] {
		st, isStruct := under(t).(*types.Struct)
		if !isStruct {
			return derefs, false
		}
		t = st.Field(i).Type()
		if p, isPtr := under(t).(*types.Pointer); isPtr {
			derefs++
			t = p.Elem()
		}
	}

	return derefs, true
}

func hasPointerReceiver(fn *types.Func) bool {
	sig, ok := fn.Type().(*types.Signature)
	if !ok || sig.Recv() == nil {
		return false
	}

	return isPointer(sig.Recv().Type())
}

// returnStmts returns the return statements of body, excluding those inside
// function literals.
func returnStmts(body *ast.BlockStmt) []*ast.ReturnStmt {
	var out []*ast.ReturnStmt
	ast.Inspect(body, func(n ast.Node) bool {
		switch n := n.(type) {
		case *ast.FuncLit:
			return false
		case *ast.ReturnStmt:
			out = append(out, n)
		}

		return true
	})

	return out
}

func argIndex(call *ast.CallExpr, arg ast.Expr) int {
	if call == nil {
		return -1
	}
	for i, a := range call.Args {
		if a == arg {
			return i
		}
	}

	return -1
}

func containsExpr(list []ast.Expr, e ast.Node) bool {
	for _, x := range list {
		if x == e {
			return true
		}
	}

	return false
}

func identsOf(exprs []ast.Expr) []*ast.Ident {
	out := make([]*ast.Ident, 0, len(exprs))
	for _, e := range exprs {
		if id, ok := e.(*ast.Ident); ok {
			out = append(out, id)
		}
	}

	return out
}

func isNil(e ast.Expr) bool {
	id, ok := ast.Unparen(e).(*ast.Ident)

	return ok && id.Name == "nil" && id.Obj == nil
}

func under(t types.Type) types.Type {
	if t == nil {
		return nil
	}

	return types.Unalias(t).Underlying()
}

func isPointer(t types.Type) bool {
	_, ok := under(t).(*types.Pointer)

	return ok
}
