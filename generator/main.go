package main

import (
	"go/build"
	"path/filepath"
	"strings"
	"go/ast"
	"go/token"
	"go/parser"
	"fmt"
	"bytes"
	"strconv"
	"go/printer"
	"golang.org/x/tools/imports"
	"os"
	"text/template"
)


// findInterface returns the import path and identifier of an interface.
// For example, given "http.ResponseWriter", findInterface returns
// "net/http", "ResponseWriter".
// If a fully qualified interface is given, such as "net/http.ResponseWriter",
// it simply parses the input.
func findInterface(iface string, srcDir string) (path string, id string, err error) {
	if len(strings.Fields(iface)) != 1 {
		return "", "", fmt.Errorf("couldn't parse interface: %s", iface)
	}

	srcPath := filepath.Join(srcDir, "__go_impl__.go")

	if slash := strings.LastIndex(iface, "/"); slash > -1 {
		// package path provided
		dot := strings.LastIndex(iface, ".")
		// make sure iface does not end with "/" (e.g. reject net/http/)
		if slash+1 == len(iface) {
			return "", "", fmt.Errorf("interface name cannot end with a '/' character: %s", iface)
		}
		// make sure iface does not end with "." (e.g. reject net/http.)
		if dot+1 == len(iface) {
			return "", "", fmt.Errorf("interface name cannot end with a '.' character: %s", iface)
		}
		// make sure iface has exactly one "." after "/" (e.g. reject net/http/httputil)
		if strings.Count(iface[slash:], ".") != 1 {
			return "", "", fmt.Errorf("invalid interface name: %s", iface)
		}
		return iface[:dot], iface[dot+1:], nil
	}

	src := []byte("package hack\n" + "var i " + iface)
	// If we couldn't determine the import path, goimports will
	// auto fix the import path.
	imp, err := imports.Process(srcPath, src, nil)
	if err != nil {
		return "", "", fmt.Errorf("couldn't parse interface: %s (%s)", iface, err)
	}

	// imp should now contain an appropriate import.
	// Parse out the import and the identifier.
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, srcPath, imp, 0)
	if err != nil {
		panic(err)
	}
	if len(f.Imports) == 0 {
		return "", "", fmt.Errorf("unrecognized interface: %s", iface)
	}
	raw := f.Imports[0].Path.Value   // "io"
	path, err = strconv.Unquote(raw) // io
	if err != nil {
		panic(err)
	}
	decl := f.Decls[1].(*ast.GenDecl)      // var i io.Reader
	spec := decl.Specs[0].(*ast.ValueSpec) // i io.Reader
	sel := spec.Type.(*ast.SelectorExpr)   // io.Reader
	id = sel.Sel.Name                      // Reader
	return path, id, nil
}

// Pkg is a parsed build.Package.
type Pkg struct {
	*build.Package
	*token.FileSet
}

// typeSpec locates the *ast.TypeSpec for type id in the import path.
func typeSpec(path string, id string, srcDir string) (Pkg, *ast.TypeSpec, error) {
	pkg, err := build.Import(path, srcDir, 0)
	if err != nil {
		return Pkg{}, nil, fmt.Errorf("couldn't find package %s: %v", path, err)
	}

	fset := token.NewFileSet() // share one fset across the whole package
	for _, file := range pkg.GoFiles {
		f, err := parser.ParseFile(fset, filepath.Join(pkg.Dir, file), nil, 0)
		if err != nil {
			continue
		}

		for _, decl := range f.Decls {
			decl, ok := decl.(*ast.GenDecl)
			if !ok || decl.Tok != token.TYPE {
				continue
			}
			for _, spec := range decl.Specs {
				spec := spec.(*ast.TypeSpec)
				if spec.Name.Name != id {
					continue
				}
				return Pkg{Package: pkg, FileSet: fset}, spec, nil
			}
		}
	}
	return Pkg{}, nil, fmt.Errorf("type %s not found in %s", id, path)
}


// typeSpec locates the *ast.TypeSpec for type id in the import path.
func structMethods(path string, id string, srcDir string) ([]*ast.FuncDecl, error) {
	pkg, err := build.Import(path, srcDir, 0)
	if err != nil {
		return nil, fmt.Errorf("couldn't find package %s: %v", path, err)
	}

	fset := token.NewFileSet() // share one fset across the whole package
	var fullMethodSet []*ast.FuncDecl
	for _, file := range pkg.GoFiles {
		f, err := parser.ParseFile(fset, filepath.Join(pkg.Dir, file), nil, 0)
		if err != nil {
			continue
		}

		for _, decl := range f.Decls {
			decl, ok := decl.(*ast.FuncDecl)
			if !ok || decl.Recv == nil || !decl.Name.IsExported() {
				continue
			}

			if len(decl.Recv.List) != 1 {
				fmt.Println(len(decl.Recv.List), decl.Name.Name)
				panic("logic error.  I didn't expect this to be non zero length")
			}
			firstType := decl.Recv.List[0].Type
			ident, ok := firstType.(*ast.Ident)
			if !ok {
				starExpr, ok := firstType.(*ast.StarExpr)
				if !ok {
					panic("Logic error: should be a star expr or ident")
				}
				ident, ok = starExpr.X.(*ast.Ident)
				if !ok {
					panic("Logic error: I expect an ident or star expression")
				}
			}
			if ident.Name != id {
				continue
			}
			fullMethodSet = append(fullMethodSet, decl)
		}
	}
	return fullMethodSet, nil
}

// gofmt pretty-prints e.
func (p Pkg) gofmt(e ast.Expr) string {
	var buf bytes.Buffer
	printer.Fprint(&buf, p.FileSet, e)
	return buf.String()
}

// fullType returns the fully qualified type of e.
// Examples, assuming package net/http:
// 	fullType(int) => "int"
// 	fullType(Handler) => "http.Handler"
// 	fullType(io.Reader) => "io.Reader"
// 	fullType(*Request) => "*http.Request"
func (p Pkg) fullType(e ast.Expr) string {
	ast.Inspect(e, func(n ast.Node) bool {
		switch n := n.(type) {
		case *ast.Ident:
			// Using typeSpec instead of IsExported here would be
			// more accurate, but it'd be crazy expensive, and if
			// the type isn't exported, there's no point trying
			// to implement it anyway.
			if n.IsExported() {
				n.Name = p.Package.Name + "." + n.Name
			}
		case *ast.SelectorExpr:
			return false
		}
		return true
	})
	return p.gofmt(e)
}

func (p Pkg) params(field *ast.Field) []Param {
	var params []Param
	typ := p.fullType(field.Type)
	for _, name := range field.Names {
		params = append(params, Param{Name: name.Name, Type: typ})
	}
	// Handle anonymous params
	if len(params) == 0 {
		params = []Param{Param{Type: typ}}
	}
	return params
}

// Method represents a method signature.
type Method struct {
	Recv string
	Func
}

// Func represents a function signature.
type Func struct {
	Name   string
	Params []Param
	Res    []Param
}

func (f Func) String() string {
	totalParams := []string{}
	for _, p := range f.Params {
		totalParams = append(totalParams, p.String())
	}
	totalRes := []string{}
	for _, p := range f.Res {
		totalRes = append(totalParams, p.String())
	}
	return fmt.Sprintf("func (?) %s(%s) (%s)", f.Name, strings.Join(totalParams, ", "), strings.Join(totalRes, ", "))
}

// Param represents a parameter in a function or method signature.
type Param struct {
	Name string
	Type string
}

func (p Param) String() string {
	return fmt.Sprintf("%s %s", p.Name, p.Type)
}

func (p Pkg) fillFunction(fn *Func, typ *ast.FuncType) {
	if typ.Params != nil {
		for _, field := range typ.Params.List {
			fn.Params = append(fn.Params, p.params(field)...)
		}
	}
	if typ.Results != nil {
		for _, field := range typ.Results.List {
			fn.Res = append(fn.Res, p.params(field)...)
		}
	}
}

func (p Pkg) funcsig(f *ast.Field) Func {
	fn := Func{Name: f.Names[0].Name}
	typ := f.Type.(*ast.FuncType)
	p.fillFunction(&fn, typ)
	return fn
}

// funcs returns the set of methods required to implement iface.
// It is called funcs rather than methods because the
// function descriptions are functions; there is no receiver.
func funcs(iface string, srcDir string) ([]Func, error) {
	// Locate the interface.
	path, id, err := findInterface(iface, srcDir)
	if err != nil {
		return nil, err
	}

	// Parse the package and find the interface declaration.
	p, spec, err := typeSpec(path, id, srcDir)
	if err != nil {
		return nil, fmt.Errorf("interface %s not found: %s", iface, err)
	}
	var fns []Func

	if idecl, ok := spec.Type.(*ast.InterfaceType);ok {
		methods := idecl.Methods
		if methods == nil {
			return nil, fmt.Errorf("empty interface: %s", iface)
		}

		for _, fndecl := range methods.List {
			if len(fndecl.Names) == 0 {
				// Embedded interface: recurse
				embedded, err := funcs(p.fullType(fndecl.Type), srcDir)
				if err != nil {
					return nil, err
				}
				fns = append(fns, embedded...)
				continue
			}

			fn := p.funcsig(fndecl)
			fns = append(fns, fn)
		}
	} else if st, ok := spec.Type.(*ast.StructType); ok  {
		for _, f := range st.Fields.List {
			if len(f.Names) == 0 {
				// Embedded.  Recurse
				embedded, err := funcs(p.fullType(f.Type), srcDir)
				if err != nil {
					return nil, err
				}
				fns = append(fns, embedded...)
				continue
			}
		}
		methods, err := structMethods(path, id, srcDir)
		if err != nil {
			return nil, err
		}
		if methods == nil {
			return nil, fmt.Errorf("empty struct: %s", iface)
		}

		for _, method := range methods {
			fn := p.funcsig(&ast.Field{
				Names: []*ast.Ident{
					method.Name,
				},
				Type: method.Type,
			})
			fns = append(fns, fn)
		}
	} else {
		return nil, fmt.Errorf("not an interface or struct: %s", iface)
	}

	return fns, nil
}

type Generator struct {
	trimPrefix  string
	lineComment bool
	buf bytes.Buffer
}

var instance = Generator {}

const exampleFile = `
package XYZ

type CircuitName struct {
	Embed *Name

	GetCircuit *circuit.Circuit
	_GetCircuitFallback
}

func (c *CircuitName) PointerRecv() {
	c.Embed.PointerRecv()
}

func (c *CircuitName) Get(url string) (*http.Response, error) {
	var resp *http.Response
	err := c._GetCircuit.Do(context.TODO(), func(ctx context.Context) error {
		var err error
		resp, err = c.Embed.Get(url)
		return err
	})
	return resp, err
}
`

const templateText = `
package {{ .Package }}

type Circuit{{ .EmbedType}} struct {
	Embed *{{ .EmbedType }}
}

`

var fileTemplate = template.Must(template.New("file").Parse(templateText))

func (g *Generator) main() {
	funcs, err := funcs("example.FullExample", "C:/Users/jack/go/src/github.com/cep21/circuit/generator/internal/example")
	//funcs, err := funcs("metriceventstream.MetricEventStream", "C:/Users/jack/go/src/github.com/cep21/circuit/metriceventstream")
	//funcs, err := funcs("hystrix.Name", "C:/Users/jack/go/src/github.com/cep21/circuit")
	if err != nil {
		fmt.Fprintf(os.Stderr, "Unable to load functions: %s", err)
		os.Exit(1)
		return
	}
	for _, f := range funcs {
		fmt.Println(f.Name, len(f.Params), len(f.Res), f)

	}
}

func main () {
	instance.main()
}


