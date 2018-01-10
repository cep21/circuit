package example_anothername

import (
	"context"
	io2 "io"
	"net/http"
	"github.com/cep21/circuit/cmd/gencircuit/internal/example/renamed"
	"net/url"
)

type MyInterface interface {
	EmbedI
	Hello(name string)
	io2.Closer
}

type EmbedI interface {
	Embed()
}

type MyThing struct {
	Age int
}

type FullExample struct {
	// Embed struct
	http.Client
	// embed private interface
	MyInterface
	// Embed handler
	http.Handler
	renamedpkg.RenamedIface
	// private
	aThing int
	//public
	AThing float64
	X struct {
		http.Header
	}
}

func (w *FullExample) RequiresImport(url url.URL) {

}

func (w *FullExample) Get(url string) (resp *http.Response, err error) {
	return w.Client.Get(url)
}

func (w *FullExample) Post() {
}

func (w *FullExample) ReturnMyThing() MyThing {
	return MyThing{}
}

func (w *FullExample) ReturnsString(name renamedpkg.Name) string {
	return ""
}

func (w *FullExample) PointerRecv() {
	w.ServeHTTP(nil, nil)
}

func (w FullExample) NonPointerRecv() {
}

func (w *FullExample) RenamedImport(rw io2.Reader) {

}

func (w *FullExample) WithCtx(ctx context.Context) {

}

func (w *FullExample) WithCtxErr(ctx context.Context) error {
	return nil
}

func (w *FullExample) WithCtxNoErr(ctx context.Context) {

}

func (w *FullExample) ErrNoCtx() error {
	return nil
}
