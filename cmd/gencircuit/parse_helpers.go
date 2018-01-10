package main

import (
	"go/types"
	"go/token"
	"go/ast"
	"go/build"
	"go/parser"
	"fmt"
	"path/filepath"
	"go/importer"
)

type pkg struct {
	srcDir string
	fset  *token.FileSet
	files map[string]*file

	typesPkg  *types.Package
	typesInfo *types.Info
	buildPkg *build.Package
}

type fnk struct {
	*types.Func
}

type parm struct {
	*types.Var
}

func importForType(t types.Type) string {
	_, ok := t.Underlying().(*types.Basic)
	if ok {
		return ""
	}
	asP, ok := t.Underlying().(*types.Pointer)
	if ok {
		return importForType(asP.Elem())
	}
	asNamed, ok := t.(*types.Named)
	if ok {
		if asNamed.Obj().Pkg() == nil {
			return ""
		}
		return asNamed.Obj().Pkg().Path()
	}
	i := t.Underlying().(*types.Interface)
	return i.String()
}

func (p *parm) Import() string {
	return importForType(p.Var.Type())
}

func (f *fnk) Params() []parm {
	sig := f.Type().(*types.Signature)
	tups := sig.Params()
	var ret []parm
	for i:=0;i<tups.Len();i++ {
		tup := tups.At(i)
		ret = append(ret, parm{
			Var: tup,
		})
	}
	return ret
}

func (f *fnk) Results() []parm {
	sig := f.Type().(*types.Signature)
	tups := sig.Results()
	var ret []parm
	for i:=0;i<tups.Len();i++ {
		tup := tups.At(i)
		ret = append(ret, parm{
			Var: tup,
		})
	}
	return ret
}

func (p *pkg) namedMethods(namedType *types.Named) (retFunc []fnk) {
	defer func() {
		// Any names that appear twice, we actually only want the first one
		actualRet := make([]fnk, 0, len(retFunc))
		for i:=0;i<len(retFunc);i++ {
			ignore := false
			for j:=0;j<i;j++ {
				if retFunc[i].Name() == retFunc[j].Name() {
					ignore = true
					break
				}
			}
			if !ignore {
				actualRet = append(actualRet, retFunc[i])
			}
		}
		retFunc = actualRet
	}()
	fmt.Println("NamedType", namedType)
	fmt.Println("Pos is", namedType.Obj())
	fmt.Println("Named type obj name is ", namedType.Obj().Name())
	fmt.Println("Obj package", namedType.Obj().Pkg().Complete())
	fmt.Println("Filename is ", p.fset.Position(namedType.Obj().Pos()).Filename)
	objBuildPkg, err := build.Import(namedType.Obj().Pkg().Path(), p.srcDir, 0)
	if err != nil {
		fmt.Println(err)
		//panic(err)
	}
	fmt.Println(objBuildPkg.Name)
	under := namedType.Underlying()
	ret := make([]fnk, 0)
	for i :=0;i< namedType.NumMethods();i++ {
		method := namedType.Method(i)
		if !method.Exported() {
			continue
		}
		ret = append(ret, fnk{method})
	}
	if asI, ok := under.(*types.Interface); ok {
		for i :=0;i< asI.NumMethods();i++ {
			method := asI.Method(i)
			if !method.Exported() {
				continue
			}
			ret = append(ret, fnk{method})
		}
		return ret
	}
	if asS, ok := under.(*types.Struct); ok {
		for i :=0;i< asS.NumFields();i++ {
			field := asS.Field(i)
			if !field.Anonymous() {
				continue
			}
			named := field.Type().(*types.Named)
			ret = append(ret, p.namedMethods(named)...)
		}
		return ret
	}
	return nil
}

func (p *pkg) Methods(elem string) (retFunc []fnk) {
	obj := p.typesPkg.Scope().Lookup(elem)
	ab := obj.Type().(*types.Named)
	for fileName, f := range p.files {
		if obj := f.f.Scope.Lookup(elem); obj != nil {
			fmt.Println("A", obj.Decl, "B", obj.Kind, "C", obj.Name, "D", obj.Data, "E", obj.Type)
			fmt.Println("Found the object in ", fileName)
		}
	}
	return p.namedMethods(ab)
}

func (p *pkg) Populate(srcDir string) error {
	p.srcDir = srcDir
	if p.fset == nil {
		p.fset = token.NewFileSet()
	}
	if p.files == nil {
		p.files = make(map[string]*file)
	}
	pkgImport, err := build.ImportDir(srcDir, 0)
	if err != nil {
		return err
	}
	p.buildPkg = pkgImport
	var pkgName string
	for _, fileName := range p.buildPkg.GoFiles {
		fullFilename := filepath.Join(srcDir, fileName)
		f, err := parser.ParseFile(p.fset, fullFilename, nil, parser.ParseComments)
		if err != nil {
			return err
		}
		if pkgName == "" {
			pkgName = f.Name.Name
		} else if f.Name.Name != pkgName {
			return fmt.Errorf("%s is in package %s, not %s", fullFilename, f.Name.Name, pkgName)
		}
		p.files[fullFilename] = &file{
			pkg:      p,
			f:        f,
			fset:     p.fset,
			src:      nil,
			filename: fullFilename,
		}
	}
	config := &types.Config{
		// By setting a no-op error reporter, the type checker does as much work as possible.
		Error:    func(error) {},
		Importer: importer.For("source", nil),
	}
	info := &types.Info{
		Types:  make(map[ast.Expr]types.TypeAndValue),
		Defs:   make(map[*ast.Ident]types.Object),
		Uses:   make(map[*ast.Ident]types.Object),
		Scopes: make(map[ast.Node]*types.Scope),
		Implicits: make(map[ast.Node]types.Object),
		Selections: make(map[*ast.SelectorExpr]*types.Selection),
		InitOrder: make([]*types.Initializer, 0),
	}
	var anyFile *file
	var astFiles []*ast.File
	for _, f := range p.files {
		anyFile = f
		astFiles = append(astFiles, f.f)
	}
	pkg, err := config.Check(anyFile.f.Name.Name, p.fset, astFiles, info)
	p.typesPkg = pkg
	p.typesInfo = info
	return err
}

type file struct {
	pkg      *pkg
	f        *ast.File
	fset     *token.FileSet
	src      []byte
	filename string
}
