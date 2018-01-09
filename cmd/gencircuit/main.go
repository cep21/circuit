package main

import (
	"bytes"
	"errors"
	"flag"
	"fmt"
	"go/ast"
	"go/build"
	"go/parser"
	"go/printer"
	"go/token"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"text/template"

	"golang.org/x/tools/imports"
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
	decl := f.Decls[1].(*ast.GenDecl)        // var i io.Reader
	spec := decl.Specs[0].(*ast.ValueSpec)   // i io.Reader
	sel, ok := spec.Type.(*ast.SelectorExpr) // io.Reader
	for !ok {
		starSel, ok2 := spec.Type.(*ast.StarExpr) // *io.Reader
		if !ok2 {
			panic("Unexpected")
		}
		sel, ok = starSel.X.(*ast.SelectorExpr) // io.Reader
	}
	id = sel.Sel.Name // Reader
	return path, id, nil
}

// Pkg is a parsed build.Package.
type Pkg struct {
	*build.Package
	*token.FileSet
}

// typeSpec locates the *ast.TypeSpec for type id in the import path.
func typeSpec(path string, id string, srcDir string) (Pkg, *ast.File, *ast.TypeSpec, error) {
	pkg, err := build.Import(path, srcDir, 0)
	if err != nil {
		return Pkg{}, nil, nil, fmt.Errorf("couldn't find package %s: %v", path, err)
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
				return Pkg{Package: pkg, FileSet: fset}, f, spec, nil
			}
		}
	}
	return Pkg{}, nil, nil, fmt.Errorf("type %s not found in %s", id, path)
}

type foundFunc struct {
	FuncDecl *ast.FuncDecl
	File     *ast.File
	FilePath string
	Pkg      *build.Package
}

// typeSpec locates the *ast.TypeSpec for type id in the import path.
func structMethods(path string, id string, srcDir string) ([]foundFunc, error) {
	pkg, err := build.Import(path, srcDir, 0)
	if err != nil {
		return nil, fmt.Errorf("couldn't find package %s: %v", path, err)
	}

	fset := token.NewFileSet() // share one fset across the whole package
	var fullMethodSet []foundFunc
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
			fullMethodSet = append(fullMethodSet, foundFunc{
				FuncDecl: decl,
				File:     f,
				Pkg:      pkg,
				FilePath: filepath.Join(pkg.Dir, file),
			})
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
		params = []Param{{Type: typ}}
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
	Name     string
	Params   []Param
	Res      []Param
	File     *ast.File
	FilePath string
	Pkg      *build.Package
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
	return fmt.Sprintf("(file=%s) func (?) %s(%s) (%s)", f.FilePath, f.Name, strings.Join(totalParams, ", "), strings.Join(totalRes, ", "))
}

func (f *Func) RequiredImports() []string {
	nameToImport := map[string]string{}
	nameToImport[f.Pkg.Name] = ""
	for _, i := range f.File.Imports {
		pathImport := strings.Trim(i.Path.Value, `"`)
		pkg, err := build.Import(pathImport, f.FilePath, 0)
		if err != nil {
			// If you can't find the import, it's probably not required ...
			continue
		}
		nameToImport[pkg.Name] = pathImport
	}
	thingsToImport := make(map[string]struct{})
	for _, r := range f.Res {
		p := r.TypePkg()
		if p == "" {
			continue
		}
		importPath := nameToImport[p]
		if importPath == "" {
			continue
		}
		thingsToImport[importPath] = struct{}{}
	}
	for _, r := range f.Params {
		p := r.TypePkg()
		if p == "" {
			continue
		}
		importPath := nameToImport[p]
		if importPath == "" {
			continue
		}
		thingsToImport[importPath] = struct{}{}
	}
	ret := make([]string, 0, len(thingsToImport))
	for k := range thingsToImport {
		ret = append(ret, k)
	}
	return ret
}

// Param represents a parameter in a function or method signature.
type Param struct {
	Name string
	Type string
}

func (p *Param) TypePkg() string {
	r := strings.Trim(p.Type, "*")
	parts := strings.Split(r, ".")
	if len(parts) <= 1 {
		return ""
	}
	if len(parts) == 2 {
		return parts[0]
	}
	panic("strange name" + p.Type)
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

func GetPkg(iface string, srcDir string) (Pkg, error) {
	// Locate the interface.
	path, id, err := findInterface(iface, srcDir)
	if err != nil {
		return Pkg{}, err
	}

	// Parse the package and find the interface declaration.
	p, _, _, err := typeSpec(path, id, srcDir)
	if err != nil {
		return Pkg{}, fmt.Errorf("interface %s not found: %s", iface, err)
	}
	return p, nil
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
	p, fileTypeFoundIn, spec, err := typeSpec(path, id, srcDir)
	if err != nil {
		return nil, fmt.Errorf("interface %s not found: %s", iface, err)
	}
	var fns []Func

	if idecl, ok := spec.Type.(*ast.InterfaceType); ok {
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
			fn.File = fileTypeFoundIn
			idx := 0
			for i, p := range fn.Params {
				if p.Name == "" {
					fn.Params[i].Name = fmt.Sprintf("r_%d", idx)
				}
			}
			fn.FilePath = fileTypeFoundIn.Name.Name
			fn.Pkg = p.Package
			fns = append(fns, fn)
		}
	} else if st, ok := spec.Type.(*ast.StructType); ok {
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

		for _, methodDef := range methods {
			fn := p.funcsig(&ast.Field{
				Names: []*ast.Ident{
					methodDef.FuncDecl.Name,
				},
				Type: methodDef.FuncDecl.Type,
			})
			fn.File = methodDef.File
			fn.FilePath = methodDef.FilePath
			fn.Pkg = methodDef.Pkg
			fns = append(fns, fn)
		}
	} else {
		return nil, fmt.Errorf("not an interface or struct: %s", iface)
	}

	retFns := make([]Func, 0, len(fns))
	// Remove duplicate function names, keeping the later one.  This happens with embeds
	for i := 0; i < len(fns); i++ {
		shouldRemove := false
		for j := i + 1; j < len(fns); j++ {
			if fns[i].Name == fns[j].Name {
				shouldRemove = true
				break
			}
		}
		if !shouldRemove {
			retFns = append(retFns, fns[i])
		}
	}

	return retFns, nil
}

type Generator2 struct {
	out              io.Writer
	errOut           io.Writer
	osExit           func(int)
	FlagSet          *flag.FlagSet
	Params           []string
	CurrentDirectory string
	ToGenerate       string
}

var instance = Generator2{
	out:    os.Stdout,
	errOut: os.Stderr,
	osExit: os.Exit,
	Params: os.Args,
}

const funcBodyText = `{{ range .CodeResults }}
  var {{ .Name }} {{ .Type }}
  {{- end }}
  err = z.Circuit_{{ .Name }}.Run({{ .Ctx }}, func(ctx context.Context) error {
    {{ .AllResults }} = z.Embed.{{.Name}}({{ .ParamNames }})
    return err
  }, c.Circuit_{{.Name}}Fallback)
  return {{ .AllResults }}
`

var funcBodyTemplate = template.Must(template.New("file").Parse(funcBodyText))

const templateText = `

package {{ .Package }}

{{ define "Method" -}}
func (z *Circuit{{ .CleanName }}) {{ .Name }}({{ .Params }}) {{ .Returns }} {
 {{ .Body }}
}
{{- end -}}

{{- define "StructTypes" }}
  Circuit_{{ .Name }} *circuit.Circuit
  Circuit_{{ .Name }}Fallback func(ctx context.Context, err error) error
{{ end -}}

{{ range .Imports -}}
import "{{ . }}"
{{ end }}

{{- $className := .EmbedType }}
type Circuit{{ .CleanName }} struct {
	Embed {{ .EmbedType }}
{{ range .CircuitTypeFuncs }}{{ template "StructTypes" . }}{{ end }}
}
{{ range .Func -}}
{{ template "Method" . }}
{{ end -}}

type Circuit{{ .CleanName }}Factory struct {
  Prefix string
  Manager *circuit.Manager
}

func (z * Circuit{{ .CleanName }}Factory) New(embed {{ .EmbedType }}) (*Circuit{{ .CleanName}}, error) {
  var err error
  ret := &Circuit{{ .EmbedType}} {
    Embed: embed,
  }
  {{- $x := . -}}
  {{ range .CircuitTypeFuncs }}
  ret.Circuit_{{ .Name }}, err = z.Manager.CreateCircuit(z.Prefix + "{{ $x.EmbedType }}-{{.Name}}")
  if err != nil {
    return nil, err
  }
  {{- end }}
  return ret, nil
}
`

var fileTemplate = template.Must(template.New("file").Parse(templateText))

type StructTypesData struct {
	Name string
}

type fileData struct {
	Package   string
	EmbedType string
	Func      []funcData
}

func (f *fileData) CircuitTypeFuncs() []StructTypesData {
	ret := make([]StructTypesData, 0, len(f.Func))
	for _, f := range f.Func {
		if len(f.Res) == 0 {
			continue
		}
		lastResult := f.Res[len(f.Res)-1]
		if lastResult.Type != "error" {
			continue
		}
		ret = append(ret, StructTypesData{
			Name: f.Name,
		})
	}
	return ret
}

func (f *fileData) CleanName() string {
	n := strings.Trim(f.EmbedType, "*")
	return strings.Replace(n, ".", "_", -1)
}

func (f *funcData) CleanName() string {
	n := strings.Trim(f.EmbedType, "*")
	return strings.Replace(n, ".", "_", -1)
}

func (f *fileData) StructName() string {
	return "Circuit" + f.EmbedType
}

func (f *fileData) Imports() []string {
	toImport := make(map[string]struct{})
	for _, fnc := range f.Func {
		for _, i := range fnc.RequiredImports() {
			toImport[i] = struct{}{}
		}
	}
	var ret []string
	for k := range toImport {
		ret = append(ret, k)
	}
	return ret
}

type funcData struct {
	Func
	EmbedType string
}

type codeResult struct {
	Name string
	Type string
}

func (f *funcData) ParamNames() string {
	ret := make([]string, 0, len(f.Func.Params))
	for _, p := range f.Func.Params {
		ret = append(ret, p.Name)
	}
	return strings.Join(ret, ", ")
}

func (f *funcData) Ctx() string {
	if len(f.Func.Params) == 0 {
		return "context.TODO()"
	}
	first := f.Func.Params[0]
	if first.Type == "context.Context" {
		return first.Name
	}
	return "context.TODO()"
}

func (f *funcData) AllResults() string {
	codeResults := f.CodeResults()
	ret := make([]string, 0, len(codeResults))
	for _, c := range codeResults {
		ret = append(ret, c.Name)
	}
	return strings.Join(ret, ", ")
}

func (f *funcData) CodeResults() []codeResult {
	ret := make([]codeResult, 0, len(f.Res))
	for idx, r := range f.Res {
		if r.Type == "error" {
			ret = append(ret, codeResult{
				Name: "err",
				Type: "error",
			})
			continue
		}
		ret = append(ret, codeResult{
			Name: "v" + strconv.Itoa(idx),
			Type: r.Type,
		})
	}
	return ret
}

func (f *funcData) Params() string {
	ret := make([]string, 0, len(f.Func.Params))
	for _, p := range f.Func.Params {
		ret = append(ret, strings.TrimSpace(p.Name+" "+p.Type))
	}
	return strings.Join(ret, ", ")
}

func (f *funcData) ParamsNames() string {
	ret := make([]string, 0, len(f.Func.Params))
	for _, p := range f.Func.Params {
		ret = append(ret, strings.TrimSpace(p.Name))
	}
	return strings.Join(ret, ", ")
}

func (f *funcData) canTakeCircuit() bool {
	if len(f.Func.Res) == 0 {
		return false
	}
	last := f.Func.Res[len(f.Func.Res)-1]
	return last.Type == "error"
}

func (f *funcData) hasReturn() bool {
	return len(f.Func.Res) != 0
}

func (f *funcData) Body() string {
	if !f.hasReturn() {
		return "  z.Embed." + f.Name + "(" + f.ParamsNames() + ")"
	}
	if !f.canTakeCircuit() {
		return "  return z.Embed." + f.Name + "(" + f.ParamsNames() + ")"
	}
	buf := bytes.Buffer{}
	if err := funcBodyTemplate.Execute(&buf, f); err != nil {
		panic(err)
	}
	return buf.String()
}

func (f *funcData) Returns() string {
	ret := make([]string, 0, len(f.Func.Res))
	for _, p := range f.Func.Res {
		ret = append(ret, strings.TrimSpace(p.Name+" "+p.Type))
	}
	toRet := strings.Join(ret, ", ")
	if len(f.Func.Res) <= 1 {
		return toRet
	}
	return "(" + toRet + ")"
}

func (f *funcData) StructName() string {
	return "Circuit" + f.EmbedType
}

func (g *Generator2) parseFlags() error {
	g.FlagSet = flag.NewFlagSet(g.Params[0], flag.ContinueOnError)
	g.FlagSet.SetOutput(g.errOut)
	if err := g.FlagSet.Parse(g.Params[1:]); err != nil {
		return err
	}
	if g.CurrentDirectory == "" {
		d, err := os.Getwd()
		if err != nil {
			return err
		}
		g.CurrentDirectory = d
	}
	if len(g.Params) != 2 {
		return errors.New("please pass a single parameter: which object to wrap")
	}
	g.ToGenerate = g.Params[1]
	return nil
}

func (g *Generator2) main() {
	if err := g.parseFlags(); err != nil {
		fmt.Fprintf(g.errOut, "Unable to load package: %s", err)
		os.Exit(1)
		return
	}
	totalPath := g.CurrentDirectory
	toGen := g.ToGenerate

	funcs, err := funcs(toGen, totalPath)
	if err != nil {
		fmt.Fprintf(g.errOut, "Unable to load functions: %s", err)
		os.Exit(1)
		return
	}
	p, err := GetPkg(toGen, totalPath)
	if err != nil {
		fmt.Fprintf(g.errOut, "Unable to load package: %s", err)
		os.Exit(1)
		return
	}

	buf := bytes.Buffer{}
	fd := &fileData{
		Package:   p.Name,
		EmbedType: toGen,
		Func:      makeFuncs(funcs, toGen),
	}
	err = fileTemplate.Execute(&buf, fd)
	if err != nil {
		panic(err)
	}
	str := buf.String()
	imp, err := imports.Process(totalPath+"/__mything__.go", []byte(str), nil)
	if err != nil {
		fmt.Fprintf(g.errOut, number(str))
		panic(err)
	}
	fset := token.NewFileSet()
	st, err := parser.ParseFile(fset, totalPath+"/__mything__.go", imp, 0)
	if err != nil {
		fmt.Fprintf(g.errOut, number(str))
		panic(err)
	}
	buf2 := bytes.Buffer{}
	err2 := printer.Fprint(&buf2, fset, st)
	if err2 != nil {
		fmt.Fprintf(g.errOut, number(str))
		panic(err2)
	}
	fmt.Fprintf(g.out, buf2.String())
}

func number(s string) string {
	buf := bytes.Buffer{}
	parts := strings.Split(s, "\n")
	for i := 0; i < len(parts); i++ {
		fmt.Fprintf(&buf, "%d %s\n", i, parts[i])
	}
	return buf.String()
}

func makeFuncs(funcs []Func, embedType string) []funcData {
	ret := make([]funcData, 0, len(funcs))
	for _, f := range funcs {
		ret = append(ret, funcData{
			Func:      f,
			EmbedType: embedType,
		})
	}
	return ret
}

func main() {
	instance.main()
}
