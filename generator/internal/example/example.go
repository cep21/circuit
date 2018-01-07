package example

import (
	"net/http"
	"context"
)

type MyInterface interface {
	Hello(name string)
}

type FullExample struct {
	// Embed struct
	http.Client
	// embed private interface
	MyInterface
	// Embed handler
	http.Handler
	// private
	aThing int
	//public
	AThing float64
}

func (w *FullExample) PointerRecv() {
	w.ServeHTTP(nil, nil)
}

func (w FullExample) NonPointerRecv() {
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
