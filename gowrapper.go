package hystrix

import (
	"context"

	"github.com/cep21/hystrix/faststats"
)

// goroutineWrapper contains logic to wrap normal run methods inside a goroutine so they can end early
// if the goroutine continues to run
type goroutineWrapper struct {
	skipCatchPanics faststats.AtomicBoolean
	lostErrors      func(err error, panics interface{})
}

func (g *goroutineWrapper) run(runFunc func(context.Context) error) func(context.Context) error {
	if runFunc == nil {
		return nil
	}
	return func(ctx context.Context) error {
		var panicResult chan interface{}
		if !g.skipCatchPanics.Get() {
			panicResult = make(chan interface{}, 1)
		}
		runFuncErr := make(chan error, 1)
		go func() {
			if panicResult != nil {
				defer func() {
					if r := recover(); r != nil {
						panicResult <- r
					}
				}()
			}
			runFuncErr <- runFunc(ctx)
		}()
		select {
		case <-ctx.Done():
			// runFuncErr is a lost error.
			if g.lostErrors != nil {
				go g.waitForErrors(runFuncErr, panicResult)
			}
			return ctx.Err()
		case err := <-runFuncErr:
			return err
		case panicVal := <-panicResult:
			panic(panicVal)
		}
	}
}

func (g *goroutineWrapper) fallback(runFunc func(context.Context, error) error) func(context.Context, error) error {
	if runFunc == nil {
		return nil
	}
	return func(ctx context.Context, err error) error {
		return g.run(func(funcCtx context.Context) error {
			return runFunc(funcCtx, err)
		})(ctx)
	}
}

func (g *goroutineWrapper) waitForErrors(runFuncErr chan error, panicResults chan interface{}) {
	select {
	case err := <-runFuncErr:
		g.lostErrors(err, nil)
	case panicResult := <-panicResults:
		g.lostErrors(nil, panicResult)
	}
	close(runFuncErr)
	close(panicResults)
}
