package codegraph

import (
	"crypto/sha1"
	"fmt"
	"go/ast"
	"go/importer"
	"go/parser"
	"go/token"
	"go/types"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/alterfo/kb/internal/store/graphstore"
)

const KindCode = "code"

const (
	TypePackage  = "code-package"
	TypeFunction = "code-function"
	TypeType     = "code-type"
	TypeMethod   = "code-method"
)

const (
	EdgeCalls      = "CALLS"
	EdgeImports    = "IMPORTS"
	EdgeImplements = "IMPLEMENTS"
	EdgeDeclares   = "DECLARES"
)

type builder struct {
	file       *ast.File
	fset       *token.FileSet
	src        []byte
	parsed     []parsedFile
	info       *types.Info
	checkedPkg *types.Package
	pkgPath    string
	entities   map[string]graphstore.Entity
	relations  map[string]graphstore.Relation
	funcIDs    map[string]string
	methodIDs  map[string]string
	aliasPkg   map[string]string
}

type File struct {
	Path string
	Src  []byte
}

type parsedFile struct {
	file *ast.File
	src  []byte
}

func Extract(filePath string, src []byte) ([]graphstore.Entity, []graphstore.Relation, error) {
	return ExtractFiles([]File{{Path: filePath, Src: src}})
}

func ExtractFiles(files []File) ([]graphstore.Entity, []graphstore.Relation, error) {
	if len(files) == 0 {
		return nil, nil, nil
	}
	fset := token.NewFileSet()
	b := &builder{
		fset:      fset,
		pkgPath:   filepath.ToSlash(files[0].Path),
		entities:  map[string]graphstore.Entity{},
		relations: map[string]graphstore.Relation{},
		funcIDs:   map[string]string{},
		methodIDs: map[string]string{},
		aliasPkg:  map[string]string{},
	}
	for _, f := range files {
		file, err := parser.ParseFile(fset, f.Path, f.Src, parser.SkipObjectResolution)
		if err != nil {
			continue
		}
		b.parsed = append(b.parsed, parsedFile{file: file, src: f.Src})
	}
	if len(b.parsed) == 0 {
		return nil, nil, nil
	}
	b.build()
	entities, relations := b.result()
	return entities, relations, nil
}

func (b *builder) build() {
	b.file = b.parsed[0].file
	b.src = b.parsed[0].src
	pkgID := entityID(b.localName(b.file.Name.Name), TypePackage)
	b.addEntity(graphstore.Entity{
		ID:          pkgID,
		Name:        b.file.Name.Name,
		Type:        TypePackage,
		Description: "package " + b.file.Name.Name,
	})
	for _, pf := range b.parsed {
		b.file = pf.file
		b.src = pf.src
		b.collectImports(pkgID)
		b.collectDecls(pkgID)
	}
	checked := b.typeCheck()
	for _, pf := range b.parsed {
		b.file = pf.file
		b.src = pf.src
		b.resolveCalls(checked)
	}
	b.resolveImplements(checked)
}

func (b *builder) collectImports(pkgID string) {
	for _, imp := range b.file.Imports {
		path, err := strconv.Unquote(imp.Path.Value)
		if err != nil || path == "" {
			continue
		}
		if alias := importAlias(imp, path); alias != "" {
			b.aliasPkg[alias] = path
		}
		impID := entityID(path, TypePackage)
		b.addEntity(graphstore.Entity{
			ID:          impID,
			Name:        path,
			Type:        TypePackage,
			Description: "imported package " + path,
		})
		b.addRelation(graphstore.Relation{
			ID:          relationID(pkgID, impID, EdgeImports),
			Src:         pkgID,
			Dst:         impID,
			Type:        EdgeImports,
			Description: "imports " + path,
			Weight:      1,
		})
	}
}

func (b *builder) collectDecls(pkgID string) {
	for _, decl := range b.file.Decls {
		switch d := decl.(type) {
		case *ast.FuncDecl:
			b.collectFunc(pkgID, d)
		case *ast.GenDecl:
			if d.Tok != token.TYPE {
				continue
			}
			for _, spec := range d.Specs {
				ts, ok := spec.(*ast.TypeSpec)
				if !ok {
					continue
				}
				id := entityID(b.localName(ts.Name.Name), TypeType)
				b.addEntity(graphstore.Entity{
					ID:          id,
					Name:        ts.Name.Name,
					Type:        TypeType,
					Description: b.sourceSlice(ts),
				})
				b.addRelation(graphstore.Relation{
					ID:          relationID(pkgID, id, EdgeDeclares),
					Src:         pkgID,
					Dst:         id,
					Type:        EdgeDeclares,
					Description: "package declares type " + ts.Name.Name,
					Weight:      1,
				})
			}
		}
	}
}

func (b *builder) collectFunc(pkgID string, d *ast.FuncDecl) {
	recv := recvTypeName(d.Recv)
	if recv == "" {
		id := entityID(b.localName(d.Name.Name), TypeFunction)
		b.funcIDs[d.Name.Name] = id
		b.addEntity(graphstore.Entity{
			ID:          id,
			Name:        d.Name.Name,
			Type:        TypeFunction,
			Description: b.sourceSlice(d),
		})
		b.addRelation(graphstore.Relation{
			ID:          relationID(pkgID, id, EdgeDeclares),
			Src:         pkgID,
			Dst:         id,
			Type:        EdgeDeclares,
			Description: "package declares function " + d.Name.Name,
			Weight:      1,
		})
		return
	}
	name := recv + "." + d.Name.Name
	id := entityID(b.localName(name), TypeMethod)
	b.methodIDs[name] = id
	b.addEntity(graphstore.Entity{
		ID:          id,
		Name:        name,
		Type:        TypeMethod,
		Description: b.sourceSlice(d),
	})
	typeID := entityID(b.localName(recv), TypeType)
	b.addRelation(graphstore.Relation{
		ID:          relationID(typeID, id, EdgeDeclares),
		Src:         typeID,
		Dst:         id,
		Type:        EdgeDeclares,
		Description: "type declares method " + name,
		Weight:      1,
	})
}

func (b *builder) typeCheck() bool {
	conf := types.Config{
		Importer: importer.ForCompiler(b.fset, "source", nil),
		Error:    func(error) {},
	}
	info := &types.Info{
		Types: map[ast.Expr]types.TypeAndValue{},
		Defs:  map[*ast.Ident]types.Object{},
		Uses:  map[*ast.Ident]types.Object{},
	}
	files := make([]*ast.File, 0, len(b.parsed))
	for _, pf := range b.parsed {
		files = append(files, pf.file)
	}
	pkg, err := conf.Check(b.pkgPath, b.fset, files, info)
	if err != nil {
		return false
	}
	b.info = info
	b.checkedPkg = pkg
	return true
}

func (b *builder) resolveCalls(checked bool) {
	var cur *ast.FuncDecl
	var curEnd token.Pos
	ast.Inspect(b.file, func(n ast.Node) bool {
		if n == nil {
			return false
		}
		if cur != nil && n.Pos() >= curEnd {
			cur = nil
		}
		switch v := n.(type) {
		case *ast.FuncDecl:
			cur = v
			curEnd = v.End()
		case *ast.CallExpr:
			if cur == nil {
				return true
			}
			srcID := b.enclosingID(cur)
			if srcID == "" {
				return true
			}
			if checked {
				b.addCallTyped(v, srcID)
			} else {
				b.addCallSyntactic(v, srcID)
			}
		}
		return true
	})
}

func (b *builder) enclosingID(decl *ast.FuncDecl) string {
	if recv := recvTypeName(decl.Recv); recv != "" {
		return b.methodIDs[recv+"."+decl.Name.Name]
	}
	return b.funcIDs[decl.Name.Name]
}

func (b *builder) addCallTyped(call *ast.CallExpr, srcID string) {
	f, ok := calleeFunc(b.info, call)
	if !ok || f.Pkg() == nil {
		return
	}
	recvName := ""
	if sig, ok := f.Type().(*types.Signature); ok && sig.Recv() != nil {
		recvName = namedTypeName(sig.Recv().Type())
	}
	if f.Pkg().Path() == b.pkgPath {
		if recvName != "" {
			if id := b.methodIDs[recvName+"."+f.Name()]; id != "" {
				b.addRelation(graphstore.Relation{
					ID:          relationID(srcID, id, EdgeCalls),
					Src:         srcID,
					Dst:         id,
					Type:        EdgeCalls,
					Description: "calls " + recvName + "." + f.Name(),
					Weight:      1,
				})
			}
			return
		}
		if id := b.funcIDs[f.Name()]; id != "" {
			b.addRelation(graphstore.Relation{
				ID:          relationID(srcID, id, EdgeCalls),
				Src:         srcID,
				Dst:         id,
				Type:        EdgeCalls,
				Description: "calls " + f.Name(),
				Weight:      1,
			})
		}
		return
	}
	if recvName != "" {
		name := f.Pkg().Path() + "." + recvName + "." + f.Name()
		id := entityID(name, TypeMethod)
		b.addEntity(graphstore.Entity{
			ID:          id,
			Name:        name,
			Type:        TypeMethod,
			Description: "imported from " + f.Pkg().Path(),
		})
		b.addRelation(graphstore.Relation{
			ID:          relationID(srcID, id, EdgeCalls),
			Src:         srcID,
			Dst:         id,
			Type:        EdgeCalls,
			Description: "calls " + name,
			Weight:      1,
		})
		return
	}
	name := f.Pkg().Path() + "." + f.Name()
	id := entityID(name, TypeFunction)
	b.addEntity(graphstore.Entity{
		ID:          id,
		Name:        name,
		Type:        TypeFunction,
		Description: "imported from " + f.Pkg().Path(),
	})
	b.addRelation(graphstore.Relation{
		ID:          relationID(srcID, id, EdgeCalls),
		Src:         srcID,
		Dst:         id,
		Type:        EdgeCalls,
		Description: "calls " + name,
		Weight:      1,
	})
}

func (b *builder) addCallSyntactic(call *ast.CallExpr, srcID string) {
	switch fun := call.Fun.(type) {
	case *ast.Ident:
		if id := b.funcIDs[fun.Name]; id != "" {
			b.addRelation(graphstore.Relation{
				ID:          relationID(srcID, id, EdgeCalls),
				Src:         srcID,
				Dst:         id,
				Type:        EdgeCalls,
				Description: "calls " + fun.Name,
				Weight:      1,
			})
		}
	case *ast.SelectorExpr:
		if x, ok := fun.X.(*ast.Ident); ok {
			if path := b.aliasPkg[x.Name]; path != "" {
				name := path + "." + fun.Sel.Name
				id := entityID(name, TypeFunction)
				b.addEntity(graphstore.Entity{
					ID:          id,
					Name:        name,
					Type:        TypeFunction,
					Description: "imported from " + path,
				})
				b.addRelation(graphstore.Relation{
					ID:          relationID(srcID, id, EdgeCalls),
					Src:         srcID,
					Dst:         id,
					Type:        EdgeCalls,
					Description: "calls " + name,
					Weight:      1,
				})
			}
		}
	}
}

type ifaceInfo struct {
	name  string
	iface *types.Interface
}

func (b *builder) resolveImplements(checked bool) {
	if !checked {
		return
	}
	typeSpecs := b.allTypeSpecs()
	if len(typeSpecs) == 0 {
		return
	}

	var ifaces []ifaceInfo
	for name := range typeSpecs {
		obj := b.checkedPkg.Scope().Lookup(name)
		if obj == nil {
			continue
		}
		if iface, ok := obj.Type().Underlying().(*types.Interface); ok && iface.NumMethods() > 0 {
			ifaces = append(ifaces, ifaceInfo{name: name, iface: iface})
		}
	}
	if errObj := types.Universe.Lookup("error"); errObj != nil {
		if iface, ok := errObj.Type().Underlying().(*types.Interface); ok && iface.NumMethods() > 0 {
			ifaces = append(ifaces, ifaceInfo{name: "error", iface: iface})
		}
	}
	if len(ifaces) == 0 {
		return
	}

	for name := range typeSpecs {
		obj := b.checkedPkg.Scope().Lookup(name)
		if obj == nil {
			continue
		}
		t := obj.Type()
		if _, isIface := t.Underlying().(*types.Interface); isIface {
			continue
		}
		srcID := entityID(b.localName(name), TypeType)
		for _, ii := range ifaces {
			dstID := entityID(b.localName(ii.name), TypeType)
			if srcID == dstID {
				continue
			}
			if types.Implements(t, ii.iface) || types.Implements(types.NewPointer(t), ii.iface) {
				b.addRelation(graphstore.Relation{
					ID:          relationID(srcID, dstID, EdgeImplements),
					Src:         srcID,
					Dst:         dstID,
					Type:        EdgeImplements,
					Description: "implements " + ii.name,
					Weight:      1,
				})
			}
		}
	}
}

func (b *builder) allTypeSpecs() map[string]*ast.TypeSpec {
	typeSpecs := map[string]*ast.TypeSpec{}
	for _, pf := range b.parsed {
		for _, decl := range pf.file.Decls {
			gd, ok := decl.(*ast.GenDecl)
			if !ok || gd.Tok != token.TYPE {
				continue
			}
			for _, spec := range gd.Specs {
				if ts, ok := spec.(*ast.TypeSpec); ok {
					typeSpecs[ts.Name.Name] = ts
				}
			}
		}
	}
	return typeSpecs
}

func (b *builder) addEntity(e graphstore.Entity) {
	if e.ID == "" || e.Name == "" {
		return
	}
	if prev, ok := b.entities[e.ID]; ok {
		if prev.Description == "" {
			prev.Description = e.Description
		}
		b.entities[e.ID] = prev
		return
	}
	b.entities[e.ID] = e
}

func (b *builder) addRelation(r graphstore.Relation) {
	if r.ID == "" || r.Src == "" || r.Dst == "" || r.Src == r.Dst {
		return
	}
	if prev, ok := b.relations[r.ID]; ok {
		prev.Weight += r.Weight
		if prev.Description == "" {
			prev.Description = r.Description
		}
		b.relations[r.ID] = prev
		return
	}
	b.relations[r.ID] = r
}

func (b *builder) sourceSlice(n ast.Node) string {
	start := b.fset.Position(n.Pos()).Offset
	end := b.fset.Position(n.End()).Offset
	if start < 0 || end > len(b.src) || start >= end {
		return ""
	}
	return strings.TrimSpace(string(b.src[start:end]))
}

func (b *builder) result() ([]graphstore.Entity, []graphstore.Relation) {
	entities := make([]graphstore.Entity, 0, len(b.entities))
	for _, e := range b.entities {
		entities = append(entities, e)
	}
	sort.Slice(entities, func(i, j int) bool { return entities[i].ID < entities[j].ID })

	relations := make([]graphstore.Relation, 0, len(b.relations))
	for _, r := range b.relations {
		relations = append(relations, r)
	}
	sort.Slice(relations, func(i, j int) bool { return relations[i].ID < relations[j].ID })
	return entities, relations
}

// localName qualifies a bare symbol name with the file's package directory
// so entities from different packages (the normal multi-package corpus
// case) never collide in the shared graph store. Imported symbols are
// already keyed by their package path; local symbols need the same
// disambiguation.
func (b *builder) localName(name string) string {
	if b.pkgPath == "" {
		return name
	}
	return filepath.ToSlash(filepath.Dir(b.pkgPath)) + "." + name
}

var nonAlnumRe = regexp.MustCompile(`[^a-z0-9]+`)

func slug(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	s = nonAlnumRe.ReplaceAllString(s, "-")
	return strings.Trim(s, "-")
}

func normalize(s string) string {
	return strings.ToLower(strings.TrimSpace(s))
}

func idHash(parts ...string) string {
	h := sha1.New()
	for _, p := range parts {
		fmt.Fprintf(h, "%d:%s;", len(p), p)
	}
	return fmt.Sprintf("%x", h.Sum(nil)[:4])
}

func entityID(name, typ string) string {
	return slug(normalize(name)+"|"+normalize(typ)) + "-" + idHash(normalize(name), normalize(typ))
}

func relationID(src, dst, typ string) string {
	return slug(src+"|"+normalize(typ)+"|"+dst) + "-" + idHash(src, normalize(typ), dst)
}

func recvTypeName(recv *ast.FieldList) string {
	if recv == nil || len(recv.List) == 0 {
		return ""
	}
	t := recv.List[0].Type
	for {
		switch v := t.(type) {
		case *ast.Ident:
			return v.Name
		case *ast.StarExpr:
			t = v.X
		case *ast.ParenExpr:
			t = v.X
		case *ast.IndexExpr:
			t = v.X
		case *ast.IndexListExpr:
			t = v.X
		default:
			return ""
		}
	}
}

func namedTypeName(t types.Type) string {
	for {
		switch v := t.(type) {
		case *types.Named:
			return v.Obj().Name()
		case *types.Alias:
			return v.Obj().Name()
		case *types.Pointer:
			t = v.Elem()
		default:
			return ""
		}
	}
}

func calleeFunc(info *types.Info, call *ast.CallExpr) (*types.Func, bool) {
	switch fun := call.Fun.(type) {
	case *ast.Ident:
		f, ok := info.Uses[fun].(*types.Func)
		return f, ok
	case *ast.SelectorExpr:
		f, ok := info.Uses[fun.Sel].(*types.Func)
		return f, ok
	}
	return nil, false
}

func importAlias(imp *ast.ImportSpec, path string) string {
	if imp.Name != nil {
		if imp.Name.Name == "_" || imp.Name.Name == "." {
			return ""
		}
		return imp.Name.Name
	}
	base := path
	if i := strings.LastIndex(base, "/"); i >= 0 {
		base = base[i+1:]
	}
	return base
}
